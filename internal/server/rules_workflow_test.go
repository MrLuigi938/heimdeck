package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/store"
)

// Workflows: several triggers, conditions on other tiles, if/else, waiting,
// a dry run, and a draft written by the AI; rules from before are read as
// workflows.
func TestWorkflows(t *testing.T) {
	var used atomic.Int32
	used.Store(95)
	var restarted atomic.Int32
	nas := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			restarted.Add(1)
		}
		fmt.Fprintf(w, `{"used": "%d %%"}`, used.Load())
	}))
	defer nas.Close()
	var draftAnswer atomic.Value
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, draftAnswer.Load().(string))
	}))
	defer model.Close()
	tg := newFakeTelegram(t)
	old := telegramAPI
	telegramAPI = tg.URL
	t.Cleanup(func() { telegramAPI = old })

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
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	luis := newTestClient(t, srv.URL)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "en"})
	_, g := luis.call("POST", "/api/groups", map[string]any{"name": "Server"})
	_, nasTile := luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "NAS", "url": nas.URL, "integration": "customapi", "showWidget": true,
		"config": map[string]string{"stats": "Used = used", "buttons": "Restart = POST /restart"}})
	id := int64(nasTile["id"].(float64))
	luis.call("PUT", "/api/notify", map[string]any{"telegramToken": "123456:AAH" + strings.Repeat("z", 32)})
	luis.call("PUT", "/api/settings", map[string]any{"telegramChat": "42"})
	luis.call("PUT", "/api/ai", map[string]any{"enabled": true, "url": model.URL + "/v1", "model": "m"})
	var tiles []ruleTile
	getJSON(luis, "/api/rules/tiles", &tiles)
	button := tiles[0].Actions[0]

	// A rule from before workflows is read as a workflow.
	st.SaveUserSettings(1, map[string]string{rulesKey: `[{"id":"old","name":"Old","on":true,"trigger":{"kind":"time","time":"03:00"},"actions":[{"kind":"notify","text":"hi"}]}]`})
	if r := app.rulesOf(1)[0]; len(r.Triggers) != 1 || r.Triggers[0].Time != "03:00" || len(r.Steps) != 1 || r.Trigger != nil || r.Actions != nil {
		t.Fatalf("old rule: %+v", r)
	}
	st.SaveUserSettings(1, map[string]string{rulesKey: `[]`})

	// Two triggers, a condition, and if/else in the steps.
	app.mu.Lock()
	app.latest[id] = TileStatus{Online: true}
	app.mu.Unlock()
	cond := map[string]any{"kind": "value", "tile": id, "stat": "Used", "op": ">", "value": "90 %"}
	code, saved := luis.call("POST", "/api/rules", map[string]any{"name": "Night check", "on": true, "noAsk": true,
		"triggers": []any{map[string]any{"kind": "time", "time": "02:00"}, map[string]any{"kind": "value", "tile": id, "stat": "Used", "op": ">", "value": "99 %"}},
		"conds":    []any{map[string]any{"kind": "online", "tile": id}},
		"steps": []any{map[string]any{"kind": "if", "if": cond,
			"then": []any{map[string]any{"kind": "button", "tile": id, "action": button.ID, "params": button.Params}, map[string]any{"kind": "notify", "text": "Full – restarted"}},
			"else": []any{map[string]any{"kind": "notify", "text": "All fine"}}}}})
	if code != 200 {
		t.Fatalf("save: %d %v", code, saved)
	}
	wf := app.rulesOf(1)[0]
	two := time.Date(2026, 11, 9, 2, 0, 5, 0, time.Local)
	app.mu.Lock()
	app.latest[id] = TileStatus{Online: false}
	app.mu.Unlock()
	if _, fire := app.ruleDue(1, wf, two); fire {
		t.Fatal("fired although the condition (NAS online) does not hold")
	}
	app.mu.Lock()
	app.latest[id] = TileStatus{Online: true}
	app.mu.Unlock()
	if _, fire := app.ruleDue(1, wf, two.Add(24*time.Hour)); !fire {
		t.Fatal("the time trigger did not fire with the condition holding")
	}
	e := app.runRule(ctx, 1, wf, "It is 02:00", runAuto)
	if !e.OK || restarted.Load() != 1 || !strings.Contains(e.Result, "✅ Restart · NAS") {
		t.Fatalf("then branch: %+v (restarted %d)", e, restarted.Load())
	}
	tg.waitSent(t, "then message", func(c tgSent) bool { return strings.Contains(fmt.Sprint(c.Body["text"]), "Full – restarted") })
	used.Store(50)
	app.forget(id)
	if e := app.runRule(ctx, 1, wf, "test", runAuto); restarted.Load() != 1 || !strings.Contains(e.Result, "✉") {
		t.Fatalf("else branch: %+v", e)
	}
	tg.waitSent(t, "else message", func(c tgSent) bool { return strings.Contains(fmt.Sprint(c.Body["text"]), "All fine") })

	// The dry run says what would happen and does nothing.
	var dry struct {
		Lines []string `json:"lines"`
		Conds bool     `json:"conds"`
	}
	app.mu.Lock()
	app.latest[id] = TileStatus{Online: true} // as the minute check would set it again
	app.mu.Unlock()
	b, _ := json.Marshal(wf)
	req, _ := http.NewRequest("POST", srv.URL+"/api/rules/dryrun", strings.NewReader(string(b)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lang", "en")
	resp, err := luis.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	json.NewDecoder(resp.Body).Decode(&dry)
	resp.Body.Close()
	joined := strings.Join(dry.Lines, "\n")
	if !dry.Conds || !strings.Contains(joined, "✔") || !strings.Contains(joined, "❓") || !strings.Contains(joined, "All fine") || restarted.Load() != 1 {
		t.Fatalf("dry run: %+v (restarted %d)", dry, restarted.Load())
	}

	// A scene that waits runs in the background; a second start while it waits is skipped.
	_, waiting := luis.call("POST", "/api/rules", map[string]any{"name": "Slow", "on": true, "triggers": []any{map[string]any{"kind": "manual"}},
		"steps": []any{map[string]any{"kind": "wait", "minutes": 5}, map[string]any{"kind": "notify", "text": "later"}}})
	if code, res := luis.call("POST", fmt.Sprintf("/api/rules/%s/run", waiting["id"]), nil); code != 200 || !strings.Contains(fmt.Sprint(res["result"]), "Started") {
		t.Fatalf("start: %d %v", code, res)
	}
	time.Sleep(100 * time.Millisecond)
	var list struct {
		Running map[string]bool `json:"running"`
	}
	getJSON(luis, "/api/rules", &list)
	if !list.Running[waiting["id"].(string)] {
		t.Fatalf("not shown as running: %v", list.Running)
	}
	slow, _ := app.sceneOf(1, "Slow")
	if e := app.runRule(ctx, 1, slow, "again", runAuto); !strings.Contains(e.Result, "already running") {
		t.Fatalf("second start: %+v", e)
	}
	if code, _ := luis.call("POST", "/api/rules", map[string]any{"name": "x", "on": true, "triggers": []any{map[string]any{"kind": "manual"}},
		"steps": []any{map[string]any{"kind": "wait", "minutes": 5000}}}); code != 400 {
		t.Fatal("a wait over 24 hours was accepted")
	}

	// The AI drafts a workflow from a sentence; nothing is saved.
	draftAnswer.Store("Here you go:\n```json\n" + fmt.Sprintf(`{"name":"Full NAS","triggers":[{"kind":"value","tile":%d,"stat":"Used","op":">","value":"90 %%"}],"steps":[{"kind":"notify","text":"NAS full"},{"kind":"button","tile":%d,"button":0},{"kind":"scene","scene":"slow"}]}`, id, id) + "\n```")
	before := len(app.rulesOf(1))
	code, res := luis.call("POST", "/api/rules/draft", map[string]any{"text": "When the NAS is over 90 % full, tell me and restart it, then run Slow"})
	if code != 200 || res["problem"] != "" {
		t.Fatalf("draft: %d %v", code, res)
	}
	steps := res["rule"].(map[string]any)["steps"].([]any)
	if steps[1].(map[string]any)["label"] != "Restart · NAS" || steps[2].(map[string]any)["label"] != "Slow" || len(app.rulesOf(1)) != before {
		t.Fatalf("draft steps: %v", steps)
	}
	draftAnswer.Store(fmt.Sprintf(`{"name":"Bad","triggers":[{"kind":"value","tile":%d,"stat":"Nope","op":">","value":"1"}],"steps":[{"kind":"notify","text":"x"}]}`, id))
	if code, res := luis.call("POST", "/api/rules/draft", map[string]any{"text": "something odd"}); code != 200 || res["problem"] == "" {
		t.Fatalf("a draft with a problem: %d %v", code, res)
	}
	draftAnswer.Store("Sorry, I can't.")
	if code, _ := luis.call("POST", "/api/rules/draft", map[string]any{"text": "x"}); code != 502 {
		t.Fatal("an answer without JSON was taken")
	}
	_ = io.Discard
}
