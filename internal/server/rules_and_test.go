package server

import (
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/store"
)

// Triggers joined by "and": the workflow starts when one happens while the
// others hold – at 20:00 and the NAS is down – not on each one alone.
func TestTriggersJoinedByAnd(t *testing.T) {
	dir := t.TempDir()
	box, _ := secret.Open(dir)
	st, err := store.Open(filepath.Join(dir, "test.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	app := New(st, auth.New(st, false), nil, nil)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	luis := newTestClient(t, srv.URL)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "en"})
	_, g := luis.call("POST", "/api/groups", map[string]any{"name": "Server"})
	_, nas := luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "NAS", "url": "http://nas.local", "monitor": true})
	id := int64(nas["id"].(float64))
	app.mu.Lock()
	app.latest[id] = TileStatus{Online: false}
	app.mu.Unlock()
	users, _ := st.Users()
	user := users[0].ID

	at := time.Date(2026, 11, 10, 20, 0, 0, 0, time.Local)
	r := rule{ID: "and", Name: "Night and down", On: true, All: true, Cooldown: 60,
		Triggers: []ruleTrigger{{Kind: "down", Tile: id, For: 1}, {Kind: "time", Time: "20:00"}}}
	round := func(now time.Time) (string, bool) {
		app.rules.mu.Lock()
		app.rules.since, app.rules.last = now.Add(-time.Minute), now
		app.rules.mu.Unlock()
		return app.ruleDue(user, r, now)
	}
	if _, fire := round(at.Add(-5 * time.Minute)); fire {
		t.Fatal("fired on the outage alone")
	}
	if _, fire := round(at.Add(-2 * time.Minute)); fire {
		t.Fatal("fired on the outage alone, later")
	}
	why, fire := round(at.Add(20 * time.Second))
	if !fire || why == "" {
		t.Fatalf("at 20:00 with the NAS down: %v %q", fire, why)
	}
	if _, again := round(at.Add(3 * time.Minute)); again {
		t.Fatal("fired again without 20:00")
	}

	// The same workflow with "or" fires on the outage alone.
	r.All, r.ID = false, "or"
	round(at.Add(-5 * time.Minute))
	if _, fire := round(at.Add(-3 * time.Minute)); !fire {
		t.Fatal("\"or\" did not fire on the outage")
	}

	// Saving: "and" with only one trigger is just one trigger; "by hand" cannot be joined by "and".
	body := map[string]any{"name": "x", "on": true, "all": true, "triggers": []any{map[string]any{"kind": "manual"}, map[string]any{"kind": "time", "time": "07:00"}},
		"steps": []any{map[string]any{"kind": "notify", "text": "hi"}}}
	if code, _ := luis.call("POST", "/api/rules", body); code != 400 {
		t.Fatalf("\"and\" with a scene trigger: %d", code)
	}
	body["triggers"] = []any{map[string]any{"kind": "time", "time": "07:00"}}
	if code, saved := luis.call("POST", "/api/rules", body); code != 200 || saved["all"] == true {
		t.Fatalf("\"and\" with one trigger: %d %v", code, saved)
	}
}
