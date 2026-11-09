package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/camera"
	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/store"
)

// Scenes start by hand – in the settings, with /scene in Telegram, from a
// scene tile or from another rule – and count as the confirmation of their
// buttons; camera events and webhook messages trigger rules.
func TestScenesAndEvents(t *testing.T) {
	var restarted atomic.Int32
	nas := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			restarted.Add(1)
		}
		fmt.Fprint(w, `{"used": "50 %"}`)
	}))
	defer nas.Close()
	cam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte{0xff, 0xd8, 0xff, 0xe0})
	}))
	defer cam.Close()

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
	go app.TelegramBots(ctx)

	luis := newTestClient(t, srv.URL)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "en"})
	_, g := luis.call("POST", "/api/groups", map[string]any{"name": "Server"})
	_, nasTile := luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "NAS", "url": nas.URL, "integration": "customapi", "showWidget": true,
		"config": map[string]string{"stats": "Used = used", "buttons": "Restart = POST /restart"}})
	nasID := int64(nasTile["id"].(float64))
	_, camTile := luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "Door", "integration": "camera", "showWidget": true,
		"config": map[string]string{"snapshot": cam.URL + "/s.jpg", "still": "nur per Klick"}})
	camID := int64(camTile["id"].(float64))
	const hookKey = "abcdefghijklmnop1234"
	_, hookTile := luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "Backups", "integration": "webhook", "showWidget": true,
		"config": map[string]string{"hookKey": hookKey}})
	hookID := int64(hookTile["id"].(float64))
	luis.call("PUT", "/api/notify", map[string]any{"telegramToken": "123456:AAH" + strings.Repeat("y", 32)})
	luis.call("PUT", "/api/settings", map[string]any{"telegramChat": "42"})

	var tiles []ruleTile
	getJSON(luis, "/api/rules/tiles", &tiles)
	var restart ruleTile
	for _, x := range tiles {
		if x.ID == nasID {
			restart = x
		}
	}
	button := restart.Actions[0]
	save := func(body map[string]any) map[string]any {
		t.Helper()
		code, res := luis.call("POST", "/api/rules", body)
		if code != 200 {
			t.Fatalf("save %v: %d %v", body["name"], code, res)
		}
		return res
	}

	// A scene: its button asks on the dashboard, but starting the scene is the confirmation.
	movie := save(map[string]any{"name": "Movie night", "on": true, "trigger": map[string]any{"kind": "manual"},
		"actions": []any{map[string]any{"kind": "button", "tile": nasID, "action": button.ID, "params": button.Params}}})
	if code, res := luis.call("POST", fmt.Sprintf("/api/rules/%s/run", movie["id"]), nil); code != 200 || res["ok"] != true || restarted.Load() != 1 {
		t.Fatalf("start in the settings: %d %v, restarted %d", code, res, restarted.Load())
	}

	// /scene in Telegram – the bot runs for it even without the AI.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, running := app.botChats("123456:AAH" + strings.Repeat("y", 32)); running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no bot for the scenes")
		}
		time.Sleep(20 * time.Millisecond)
	}
	tg.waitSent(t, "command menu", func(c tgSent) bool {
		return c.Method == "setMyCommands" && strings.Contains(fmt.Sprint(c.Body["commands"]), "scene") && !strings.Contains(fmt.Sprint(c.Body["commands"]), "status")
	})
	tg.push(msgUpdate(42, "private", 42, "/scene movie night"))
	tg.waitSent(t, "scene ran", func(c tgSent) bool { return strings.HasPrefix(fmt.Sprint(c.Body["text"]), "🎬 Movie night") })
	if restarted.Load() != 2 {
		t.Fatalf("restarted %d", restarted.Load())
	}
	tg.push(msgUpdate(42, "private", 42, "Is everything fine?")) // no AI here: ignored
	// A group member who is not an admin may not start scenes.
	luis.call("PUT", "/api/settings", map[string]any{"telegramChat": "-77"})
	time.Sleep(300 * time.Millisecond)
	tg.push(msgUpdate(-77, "group", 5, "/scene"))
	tg.waitSent(t, "refusal", func(c tgSent) bool { return strings.Contains(fmt.Sprint(c.Body["text"]), "Only admins") })
	for _, c := range tg.sentTo(42) {
		if strings.Contains(fmt.Sprint(c.Body["text"]), "fine") {
			t.Fatal("answered a question without the AI")
		}
	}
	luis.call("PUT", "/api/settings", map[string]any{"telegramChat": "42"})

	// The scene tile on the dashboard.
	_, sceneTile := luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "Movie", "integration": "scene", "showWidget": true,
		"config": map[string]string{"scene": "movie night"}})
	sid := int64(sceneTile["id"].(float64))
	var widget struct {
		Widget struct {
			Actions []struct {
				ID, Confirm string
			} `json:"actions"`
		} `json:"widget"`
	}
	getJSON(luis, fmt.Sprintf("/api/tiles/%d/widget", sid), &widget)
	if len(widget.Widget.Actions) != 1 || !strings.Contains(widget.Widget.Actions[0].Confirm, "Movie night") {
		t.Fatalf("scene tile: %+v", widget)
	}
	if code, res := luis.call("POST", fmt.Sprintf("/api/tiles/%d/action", sid), map[string]any{"action": "run"}); code != 200 || restarted.Load() != 3 {
		t.Fatalf("scene tile pressed: %d %v, restarted %d", code, res, restarted.Load())
	}

	// A rule starting a scene; scenes starting each other stop.
	echo := save(map[string]any{"name": "Echo", "on": true, "trigger": map[string]any{"kind": "manual"},
		"actions": []any{map[string]any{"kind": "scene", "scene": movie["id"]}}})
	code, _ := luis.call("PUT", fmt.Sprintf("/api/rules/%s", movie["id"]), map[string]any{"name": "Movie night", "on": true, "trigger": map[string]any{"kind": "manual"},
		"actions": []any{map[string]any{"kind": "scene", "scene": echo["id"]}}})
	if code != 200 {
		t.Fatalf("loop save %d", code)
	}
	done := make(chan ruleLogEntry, 1)
	go func() {
		r, _ := app.sceneOf(1, "Echo")
		done <- app.runRule(ctx, 1, r, "test", runAuto)
	}()
	select {
	case e := <-done:
		if e.OK {
			t.Fatalf("a loop of scenes did not stop with a note: %+v", e)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("scenes starting each other did not stop")
	}

	// A camera event, with the camera's picture in Telegram.
	save(map[string]any{"name": "Doorbell", "on": true, "trigger": map[string]any{"kind": "camera", "tile": camID, "event": "Klingel"},
		"actions": []any{map[string]any{"kind": "notify", "text": "Someone is at the door", "camera": camID}}})
	var bell rule
	for _, r := range app.rulesOf(1) {
		if r.Name == "Doorbell" {
			bell = r
		}
	}
	now := time.Now()
	camera.AddEvent(camID, camera.Event{At: now, Kind: "Bewegung"})
	if _, fire := app.ruleDue(1, bell, now); fire {
		t.Fatal("fired for an event from before")
	}
	camera.AddEvent(camID, camera.Event{At: now, Kind: "Bewegung"})
	if _, fire := app.ruleDue(1, bell, now.Add(time.Minute)); fire {
		t.Fatal("fired for motion, not the doorbell")
	}
	camera.AddEvent(camID, camera.Event{At: now, Kind: "Klingel"})
	why, fire := app.ruleDue(1, bell, now.Add(2*time.Minute))
	if !fire || why != "Door: Doorbell" {
		t.Fatalf("doorbell: %q %v", why, fire)
	}
	if e := app.runRule(ctx, 1, bell, why, runAuto); !e.OK {
		t.Fatalf("doorbell run: %+v", e)
	}
	tg.waitSent(t, "camera picture", func(c tgSent) bool {
		return c.Method == "sendPhoto" && strings.Contains(fmt.Sprint(c.Body["caption"]), "Door")
	})

	// A webhook message containing the text.
	save(map[string]any{"name": "Backup failed", "on": true, "trigger": map[string]any{"kind": "webhook", "tile": hookID, "match": "failed"},
		"actions": []any{map[string]any{"kind": "notify", "text": "Look at the backup"}}})
	http.Post(srv.URL+"/hook/"+hookKey, "application/json", strings.NewReader(`{"title":"Backup done"}`))
	http.Post(srv.URL+"/hook/"+hookKey, "application/json", strings.NewReader(`{"title":"Backup FAILED on tank"}`))
	msg := tg.waitSent(t, "webhook rule", func(c tgSent) bool { return strings.Contains(fmt.Sprint(c.Body["text"]), "Look at the backup") })
	if !strings.Contains(fmt.Sprint(msg.Body["text"]), "Backup FAILED on tank") {
		t.Fatalf("webhook message: %v", msg.Body["text"])
	}
	time.Sleep(200 * time.Millisecond)
	n := 0
	tg.mu.Lock()
	for _, c := range tg.calls {
		if c.Method == "sendMessage" && strings.Contains(fmt.Sprint(c.Body["text"]), "Look at the backup") {
			n++
		}
	}
	tg.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d webhook notifications, want 1", n)
	}
}
