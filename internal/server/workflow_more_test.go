package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/store"
)

// New entries and finished ones, a value that stays past its limit, calling
// an address, channels per message – and workflows that travel: shared as a
// file, in the backup, from a template.
func TestWorkflowsMore(t *testing.T) {
	var mu sync.Mutex
	queue := []string{"Dune", "Andor"}
	cpu := "95"
	svc := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		var items []string
		for _, q := range queue {
			items = append(items, fmt.Sprintf(`{"name": %q}`, q))
		}
		fmt.Fprintf(w, `{"cpu": %s, "queue": [%s]}`, cpu, strings.Join(items, ","))
	}))
	defer svc.Close()
	var hookBody atomic.Value
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		hookBody.Store(r.Method + " " + string(b))
		w.WriteHeader(204)
	}))
	defer hook.Close()

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
	_, tile := luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "Downloads", "url": svc.URL, "integration": "customapi", "showWidget": true,
		"config": map[string]string{"stats": "CPU = cpu", "list": "queue", "item": "title = name"}})
	id := int64(tile["id"].(float64))
	users, _ := st.Users()
	user := users[0].ID
	setQueue := func(q ...string) {
		mu.Lock()
		queue = q
		mu.Unlock()
		app.forget(id) // the next look reads the service again
	}

	// ---- a download leaves the queue ----
	r := rule{ID: "done", Name: "Done", On: true, Cooldown: 5, Triggers: []ruleTrigger{{Kind: "item", Tile: id, Gone: true}},
		Steps: []ruleAction{{Kind: "notify", Text: "Finished: {Eintrag}"}}}
	now := time.Now()
	if _, fire := app.ruleDue(user, r, now); fire {
		t.Fatal("fired on what was there before")
	}
	setQueue("Andor")
	why, fire := app.ruleDue(user, r, now.Add(time.Minute))
	if !fire || !strings.Contains(why, "Dune") {
		t.Fatalf("finished download: %v %q", fire, why)
	}
	e := app.runRule(t.Context(), user, r, why, runDry)
	app.rules.mu.Lock()
	app.rules.state[stateKey(user, r.ID)].entry = "Dune"
	app.rules.mu.Unlock()
	run := &wfRun{s: app, ctx: t.Context(), user: user, lang: "en", r: r, why: why, ok: true}
	run.entry = "Dune"
	if got := run.fill("Finished: {Eintrag}"); got != "Finished: Dune" || !e.OK {
		t.Fatalf("{Eintrag}: %q %+v", got, e)
	}
	// A list empty for one round is a hiccup; empty in the next one too, the last download finished.
	setQueue()
	if _, fire := app.ruleDue(user, r, now.Add(3*time.Minute)); fire {
		t.Fatal("a list empty for one round counted as finished")
	}
	setQueue("Andor")
	if _, fire := app.ruleDue(user, r, now.Add(4*time.Minute)); fire {
		t.Fatal("the hiccup was over, nothing finished")
	}
	setQueue()
	app.ruleDue(user, r, now.Add(5*time.Minute))
	if why, fire := app.ruleDue(user, r, now.Add(6*time.Minute)); !fire || !strings.Contains(why, "Andor") {
		t.Fatalf("the last download finished: %v %q", fire, why)
	}

	// ---- something new ----
	setQueue("Andor")
	n := rule{ID: "new", Name: "New", On: true, Triggers: []ruleTrigger{{Kind: "item", Tile: id, Match: "found"}}, Steps: []ruleAction{{Kind: "notify", Text: "x"}}}
	app.ruleDue(user, n, now)
	setQueue("Andor", "Foundation", "Silo")
	if why, fire := app.ruleDue(user, n, now.Add(time.Minute)); !fire || !strings.Contains(why, "Foundation") || strings.Contains(why, "Silo") {
		t.Fatalf("new entry with a filter: %v %q", fire, why)
	}

	// ---- a value for X minutes ----
	v := rule{ID: "cpu", Name: "CPU", On: true, Cooldown: 5, Triggers: []ruleTrigger{{Kind: "value", Tile: id, Stat: "CPU", Op: ">", Value: "90", For: 5}},
		Steps: []ruleAction{{Kind: "notify", Text: "hot"}}}
	if _, fire := app.ruleDue(user, v, now); fire {
		t.Fatal("fired before the five minutes")
	}
	if _, fire := app.ruleDue(user, v, now.Add(3*time.Minute)); fire {
		t.Fatal("fired after three minutes")
	}
	if why, fire := app.ruleDue(user, v, now.Add(6*time.Minute)); !fire || !strings.Contains(why, "for 6 min") {
		t.Fatalf("after six minutes: %v %q", fire, why)
	}

	// ---- calling an address ----
	h := rule{ID: "hook", Name: "Hook", On: true, Triggers: []ruleTrigger{{Kind: "manual"}},
		Steps: []ruleAction{{Kind: "http", Method: "POST", URL: hook.URL + "/api/webhook/abc", Text: `{"cpu": "{Downloads · CPU}"}`}}}
	if code, res := luis.call("POST", "/api/rules", h); code != 200 {
		t.Fatalf("saving the address step: %d %v", code, res)
	}
	e = app.runRule(t.Context(), user, h, "", runByHand)
	if got, _ := hookBody.Load().(string); !e.OK || got != `POST {"cpu": "95"}` {
		t.Fatalf("called: %+v %q", e, got)
	}
	if code, _ := luis.call("POST", "/api/rules", map[string]any{"name": "x", "triggers": []any{map[string]any{"kind": "manual"}},
		"steps": []any{map[string]any{"kind": "http", "url": "file:///etc/passwd"}}}); code != 400 {
		t.Fatal("a file address was accepted")
	}
	// An external account only reaches the internet.
	luis.call("POST", "/api/users", map[string]any{"username": "guest", "password": "guest-pw-123", "external": true})
	users, _ = st.Users()
	guest := users[1].ID
	if u, _ := st.User(guest); !u.External {
		u.External = true
		st.UpdateUser(u)
	}
	if _, err := app.callURL(t.Context(), guest, "GET", hook.URL, ""); err == nil {
		t.Fatal("an external account reached the home network")
	}

	// ---- channels ----
	m := notification{Only: []string{"telegram"}}
	if !m.to("telegram") || m.to("discord") || !(notification{}).to("ntfy") {
		t.Fatal("channels")
	}

	// ---- sharing ----
	var saved []rule
	saved = app.rulesOf(user)
	var hookID string
	for _, ru := range saved {
		if ru.Name == "Hook" {
			hookID = ru.ID
		}
	}
	resp, err := luis.c.Get(srv.URL + "/api/rules/" + hookID + "/export")
	if err != nil {
		t.Fatal(err)
	}
	var file map[string]any
	json.NewDecoder(resp.Body).Decode(&file)
	resp.Body.Close()
	b, _ := json.Marshal(file)
	if strings.Contains(string(b), fmt.Sprintf(`"tile":%d`, id)) || !strings.Contains(string(b), `"heimdeckWorkflow":1`) {
		t.Fatalf("shared file: %s", b)
	}
	// The tile comes back under its name with another number: the workflow finds it.
	luis.call("DELETE", fmt.Sprintf("/api/tiles/%d", id), nil)
	_, tile2 := luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "downloads", "url": svc.URL, "integration": "customapi", "showWidget": true,
		"config": map[string]string{"stats": "CPU = cpu", "list": "queue", "item": "title = name"}})
	id2 := int64(tile2["id"].(float64))
	_, imp := luis.call("POST", "/api/rules/import", file)
	if imp["problem"] != "" || !strings.Contains(fmt.Sprint(imp["rule"]), "/api/webhook/abc") {
		t.Fatalf("import: %v", imp)
	}
	if code, _ := luis.call("POST", "/api/rules/import", map[string]any{"nope": 1}); code != 400 {
		t.Fatal("a file that is no workflow was accepted")
	}

	// ---- templates ----
	var tpls []ruleTemplate
	getJSON(luis, "/api/rules/templates", &tpls)
	if len(tpls) < 6 {
		t.Fatalf("templates: %d", len(tpls))
	}

	// ---- the backup ----
	luis.call("POST", "/api/rules", rule{Name: "New in queue", On: true, Triggers: []ruleTrigger{{Kind: "item", Tile: id2}}, Steps: []ruleAction{{Kind: "notify", Text: "{Eintrag}"}}})
	resp, _ = luis.c.Get(srv.URL + "/api/export")
	var backup map[string]any
	json.NewDecoder(resp.Body).Decode(&backup)
	resp.Body.Close()
	if wfs, _ := backup["workflows"].([]any); len(wfs) < 2 {
		t.Fatalf("workflows in the backup: %v", backup["workflows"])
	}
	// Restored on a fresh Heimdeck: tiles first, then the workflows find them.
	dir2 := t.TempDir()
	box2, _ := secret.Open(dir2)
	st2, _ := store.Open(filepath.Join(dir2, "test.db"), box2)
	t.Cleanup(func() { st2.Close() })
	app2 := New(st2, auth.New(st2, false), nil, nil)
	srv2 := httptest.NewServer(app2.Handler())
	t.Cleanup(srv2.Close)
	other := newTestClient(t, srv2.URL)
	other.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "en"})
	code, res := other.call("POST", "/api/import", backup)
	if code != 200 || res["workflows"] == float64(0) {
		t.Fatalf("restore: %d %v", code, res)
	}
	u2, _ := st2.Users()
	restored := app2.rulesOf(u2[0].ID)
	found := false
	for _, ru := range restored {
		if ru.Name == "New in queue" && ru.On && ru.Triggers[0].Tile != 0 && ru.Triggers[0].Tile != id2 || ru.Name == "New in queue" && ru.On && ru.Triggers[0].Tile != 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("restored workflows: %+v", restored)
	}
}
