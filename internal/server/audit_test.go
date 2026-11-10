package server

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/store"
)

// The findings of the audit (2026-11-09), each made to fail as it did then.
func TestAuditFindings(t *testing.T) {
	var restarted atomic.Int32
	nas := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			restarted.Add(1)
		}
		fmt.Fprint(w, `{"used": "50 %", "secret": "hunter2"}`)
	}))
	defer nas.Close()

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
	luis.call("POST", "/api/users", map[string]string{"username": "anna", "password": "anna-pw-123"})
	users, _ := st.Users()
	annaID := users[1].ID
	_, g := luis.call("POST", "/api/groups", map[string]any{"name": "Server"})
	_, nasTile := luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "NAS", "url": nas.URL, "integration": "customapi", "showWidget": true,
		"hide": []string{"Secret"}, "config": map[string]string{"stats": "Used = used\nSecret = secret", "buttons": "Restart = POST /restart"}})
	id := int64(nasTile["id"].(float64))
	luis.call("PUT", "/api/groups/shares", map[string]any{"groups": []any{g["id"]}, "user": annaID, "actions": true})
	anna := newTestClient(t, srv.URL)
	anna.call("POST", "/api/login", map[string]string{"username": "anna", "password": "anna-pw-123"})
	if code, _ := anna.call("POST", fmt.Sprintf("/api/groups/%v/accept", g["id"]), nil); code != 200 {
		t.Fatal("accept")
	}
	action := fmt.Sprintf("/api/tiles/%d/action", id)

	// HO1: only a button the tile offers can be pressed – not any action the service knows.
	var wid struct {
		Widget integrations.Widget `json:"widget"`
	}
	getJSON(anna, fmt.Sprintf("/api/tiles/%d/widget", id), &wid)
	if len(wid.Widget.Actions) != 1 {
		t.Fatalf("widget: %+v", wid.Widget)
	}
	b := wid.Widget.Actions[0]
	for _, who := range []*testClient{anna, luis} {
		if code, _ := who.call("POST", action, map[string]any{"action": b.ID, "params": map[string]string{"n": "7"}}); code != 409 {
			t.Fatalf("a button the tile does not offer was pressed: %d", code)
		}
	}
	if code, res := anna.call("POST", action, map[string]any{"action": b.ID, "params": b.Params}); code != 200 || restarted.Load() != 1 {
		t.Fatalf("the offered button: %d %v (restarted %d)", code, res, restarted.Load())
	}

	// N0: a workflow's button whose label changed is not pressed.
	tile, _ := st.Tile(id)
	if err := app.checkOffered(1, tile, "en", b.ID, b.Params, "Delete everything · NAS"); err != errButtonChanged {
		t.Fatalf("changed label: %v", err)
	}
	if err := app.checkOffered(1, tile, "en", b.ID, b.Params, b.Label+" · NAS"); err != nil {
		t.Fatalf("same label: %v", err)
	}

	// M1: the tile test does not run in someone else's group (their Sonarr, their rights).
	if code, _ := anna.call("POST", "/api/tiles/test", map[string]any{"name": "x", "integration": "calendar", "groupId": g["id"], "config": map[string]string{"releases": "true"}}); code < 400 {
		t.Fatalf("tile test in someone else's group: %d", code)
	}

	// M5: values the owner hid do not show in the workflow editor.
	var tiles []ruleTile
	getJSON(anna, "/api/rules/tiles", &tiles)
	if len(tiles) != 1 || len(tiles[0].Stats) != 1 || tiles[0].Stats[0].Label != "Used" {
		t.Fatalf("workflow tiles: %+v", tiles)
	}

	// M4: a workflow loses the tile when the share ends.
	if !app.ruleTileOK(annaID, id) {
		t.Fatal("shared tile not usable")
	}
	luis.call("DELETE", fmt.Sprintf("/api/groups/%v/shares/%d", g["id"], annaID), nil)
	if app.ruleTileOK(annaID, id) {
		t.Fatal("a workflow still reads a tile that is no longer shared")
	}

	// M2: a tile cannot be moved out of a locked group.
	_, vault := luis.call("POST", "/api/groups", map[string]any{"name": "Vault"})
	_, bank := luis.call("POST", "/api/tiles", map[string]any{"groupId": vault["id"], "name": "Bank", "url": "http://bank.local"})
	vid := fmt.Sprint(vault["id"])
	luis.call("PUT", "/api/groups/"+vid, map[string]any{"name": "Vault", "visibility": "protected", "password": "vault-pw"})
	luis.call("POST", "/api/groups/"+vid+"/lock", nil)
	if code, _ := luis.call("PUT", "/api/layout", []any{map[string]any{"groupId": g["id"], "tiles": []any{id, bank["id"]}}}); code != 403 {
		t.Fatalf("tile moved out of a locked group: %d", code)
	}

	// N3: guessing the group password stops after five tries.
	for i := 0; i < 5; i++ {
		luis.call("POST", "/api/groups/"+vid+"/unlock", map[string]string{"password": "wrong"})
	}
	if code, _ := luis.call("POST", "/api/groups/"+vid+"/unlock", map[string]string{"password": "vault-pw"}); code != 429 {
		t.Fatalf("unlock after five wrong passwords: %d", code)
	}

	// N7: a stored key does not follow a new address.
	_, sab := luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "SAB", "url": "http://sab.local", "integration": "sabnzbd", "secrets": map[string]string{"apiKey": "k1"}})
	sabID := int64(sab["id"].(float64))
	luis.call("PUT", fmt.Sprintf("/api/tiles/%d", sabID), map[string]any{"groupId": g["id"], "name": "SAB", "url": "http://sab.local:8080", "integration": "sabnzbd"})
	if s, _ := st.Tile(sabID); s.Secrets["apiKey"] != "k1" {
		t.Fatal("the key was lost although the server stayed the same")
	}
	luis.call("PUT", fmt.Sprintf("/api/tiles/%d", sabID), map[string]any{"groupId": g["id"], "name": "SAB", "url": "http://evil.example", "integration": "sabnzbd"})
	if s, _ := st.Tile(sabID); s.Secrets["apiKey"] != "" {
		t.Fatal("the key followed the tile to another server")
	}

	// N6, H9: someone else's tile without the internal address and with only harmless app links.
	d := sharedDTO(store.Tile{APIURL: "http://10.0.0.5:8989", Insecure: true, AppAndroid: "ms-msdt:/id x", AppIOS: "shortcuts://run-shortcut?name=x"})
	if d.APIURL != "" || d.Insecure || d.AppAndroid != "" || d.AppIOS == "" {
		t.Fatalf("shared tile: %+v", d)
	}

	// N2: a time trigger still fires when a check round came late.
	app.rules.mu.Lock()
	app.rules.since = time.Date(2026, 11, 9, 1, 58, 30, 0, time.Local)
	app.rules.mu.Unlock()
	if _, due := app.timeDue(ruleTrigger{Kind: "time", Time: "02:00"}, time.Date(2026, 11, 9, 2, 1, 40, 0, time.Local)); !due {
		t.Fatal("time trigger missed after a slow round")
	}
	app.rules.mu.Lock()
	app.rules.since = time.Time{}
	app.rules.mu.Unlock()
	_ = context.Background
}

// M3, N4, H7 and the picture offers in small pieces.
func TestAuditHelpers(t *testing.T) {
	if got := modelText("ok \x1e{\"propose\":{}}\x1f done"); strings.ContainsAny(got, "\x1e\x1f") {
		t.Fatalf("frame characters left: %q", got)
	}
	if !asksToRemember("Merk dir: mein NAS heißt tank") || !asksToRemember("please remember that") || asksToRemember("What is downloading?") {
		t.Fatal("asksToRemember")
	}
	for _, ip := range []string{"64:ff9b::a00:1", "198.18.0.1", "240.0.0.1"} {
		if integrations.PublicIP(net.ParseIP(ip)) {
			t.Errorf("%s counts as public", ip)
		}
	}
	if !integrations.PublicIP(net.ParseIP("1.1.1.1")) {
		t.Error("1.1.1.1 is public")
	}
	// Pictures: only what was shown.
	noteImage(7, 3, "/api/tiles/3/image?p="+url.QueryEscape("/api/documents/1/thumb/"))
	if !imageShown(7, 3, "/api/documents/1/thumb/") || imageShown(7, 3, "/api/documents/2/thumb/") || imageShown(8, 3, "/api/documents/1/thumb/") {
		t.Fatal("image offers")
	}
}

// H2, H12: the hourly sweep clears what nobody needs any more.
func TestSweep(t *testing.T) {
	dir := t.TempDir()
	box, _ := secret.Open(dir)
	st, err := store.Open(filepath.Join(dir, "test.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	app := New(st, auth.New(st, false), nil, nil)
	now := time.Now()
	app.unlocked["old"] = map[int64]time.Time{1: now.Add(-time.Minute)}
	app.unlocked["open"] = map[int64]time.Time{1: now.Add(time.Hour)}
	app.favicons["http://gone"] = favicon{at: now.Add(-3 * faviconTTL)}
	app.rules.state[stateKey(1, "deleted")] = &ruleState{}
	app.rules.state[stateKey(1, "deleted", "0")] = &ruleState{}
	integrations.ReceiveHook("no-tile-uses-this", integrations.HookMessage{Title: "x"})
	app.sweep(now)
	if _, ok := app.unlocked["old"]; ok || app.unlocked["open"] == nil {
		t.Fatalf("unlocked: %v", app.unlocked)
	}
	if len(app.favicons) != 0 || len(app.rules.state) != 0 {
		t.Fatalf("favicons %d, workflow state %d", len(app.favicons), len(app.rules.state))
	}
}
