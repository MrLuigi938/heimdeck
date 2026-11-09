package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/i18n"
	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/store"
)

func TestParseQuantity(t *testing.T) {
	for _, c := range []struct {
		in, lang string
		want     float64
	}{
		{"92 %", i18n.EN, 92}, {"1.8 TB", i18n.EN, 1.8e12}, {"1,8 TB", i18n.DE, 1.8e12}, {"11.250", i18n.DE, 11250},
		{"11,250", i18n.EN, 11250}, {"1.5", i18n.DE, 1.5}, {"1.500", i18n.DE, 1500}, {"1,234.5 W", i18n.EN, 1234.5}, {"-3 °C", i18n.DE, -3},
	} {
		q, ok := parseQuantity(c.in, c.lang)
		if !ok || q.v != c.want {
			t.Errorf("%q (%s) = %v %v, want %v", c.in, c.lang, q.v, ok, c.want)
		}
	}
	if holds, ok := compareValue("900 GB", "<", "1 TB", i18n.EN); !ok || !holds {
		t.Error("900 GB < 1 TB")
	}
	if holds, _ := compareValue("95 %", ">", "90 %", i18n.DE); !holds {
		t.Error("95 % > 90 %")
	}
	night := ruleOnly{From: "22:00", To: "06:00"}
	at := func(hm string) time.Time { tm, _ := time.Parse("15:04", hm); return tm }
	if !night.ok(at("23:30")) || !night.ok(at("05:59")) || night.ok(at("12:00")) {
		t.Error("time window over midnight")
	}
}

// Rules fire once when their trigger becomes true, run buttons with the
// user's rights, and ask in Telegram first when the button would ask.
func TestAutomations(t *testing.T) {
	var used atomic.Int32
	used.Store(80)
	var restarted atomic.Int32
	nas := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			restarted.Add(1)
		}
		fmt.Fprintf(w, `{"used": "%d %%"}`, used.Load())
	}))
	defer nas.Close()

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
	id := int64(nasTile["id"].(float64))
	luis.call("PUT", "/api/notify", map[string]any{"telegramToken": "123456:AAH" + strings.Repeat("x", 32)})
	luis.call("PUT", "/api/settings", map[string]any{"telegramChat": "42"})

	// The editor gets the tile with its value and button.
	var tiles []ruleTile
	if code := getJSON(luis, "/api/rules/tiles", &tiles); code != 200 || len(tiles) != 1 || len(tiles[0].Stats) != 1 || tiles[0].Stats[0].Value != "80 %" || len(tiles[0].Actions) != 1 {
		t.Fatalf("tiles: %d %+v", code, tiles)
	}
	button := tiles[0].Actions[0]

	// Checked on saving: no made-up buttons, no odd times.
	if code, _ := luis.call("POST", "/api/rules", map[string]any{"name": "x", "on": true, "trigger": map[string]any{"kind": "time", "time": "7 Uhr"},
		"actions": []any{map[string]any{"kind": "notify", "text": "hi"}}}); code != 400 {
		t.Fatal("a wrong time was accepted")
	}
	if code, _ := luis.call("POST", "/api/rules", map[string]any{"name": "x", "on": true, "trigger": map[string]any{"kind": "time", "time": "07:00"},
		"actions": []any{map[string]any{"kind": "button", "tile": id, "action": "made-up"}}}); code != 400 {
		t.Fatal("a button the tile does not have was accepted")
	}

	// Disk nearly full: a message and the restart, without asking.
	code, saved := luis.call("POST", "/api/rules", map[string]any{"name": "Disk full", "on": true, "noAsk": true, "cooldown": 60,
		"trigger": map[string]any{"kind": "value", "tile": id, "stat": "Used", "op": ">", "value": "90 %"},
		"actions": []any{map[string]any{"kind": "notify", "text": "The NAS is almost full."},
			map[string]any{"kind": "button", "tile": id, "action": button.ID, "params": button.Params}}})
	if code != 200 {
		t.Fatalf("save: %d %v", code, saved)
	}
	acts := saved["steps"].([]any)
	if b := acts[1].(map[string]any); b["label"] != "Restart · NAS" || b["confirm"] == "" {
		t.Fatalf("the button was not taken from the tile: %v", b)
	}
	rules := app.rulesOf(1)
	r := rules[0]
	now := time.Now()
	if _, fire := app.ruleDue(1, r, now); fire {
		t.Fatal("fired at 80 %")
	}
	used.Store(95)
	app.forget(id)
	why, fire := app.ruleDue(1, r, now.Add(time.Minute))
	if !fire || !strings.Contains(why, "NAS · Used: 95 % > 90 %") {
		t.Fatalf("did not fire at 95 %%: %q", why)
	}
	if _, fire := app.ruleDue(1, r, now.Add(2*time.Minute)); fire {
		t.Fatal("fired twice while the value stayed high")
	}
	used.Store(80)
	app.forget(id)
	app.ruleDue(1, r, now.Add(3*time.Minute))
	used.Store(96)
	app.forget(id)
	if _, fire := app.ruleDue(1, r, now.Add(4*time.Minute)); fire {
		t.Fatal("fired again within the waiting time")
	}
	if _, fire := app.ruleDue(1, r, now.Add(62*time.Minute)); !fire {
		t.Fatal("did not fire after the waiting time")
	}
	e := app.runRule(ctx, 1, r, why, runAuto)
	if !e.OK || restarted.Load() != 1 || !strings.Contains(e.Result, "✅ Restart · NAS") {
		t.Fatalf("run: %+v, restarted %d", e, restarted.Load())
	}
	has := func(what, text string) tgSent {
		return tg.waitSent(t, what, func(c tgSent) bool { return strings.Contains(fmt.Sprint(c.Body["text"]), text) })
	}
	has("notification", "The NAS is almost full.")

	// A time rule fires once in its minute; with the button's question first.
	code, saved = luis.call("POST", "/api/rules", map[string]any{"name": "Nightly restart", "on": true,
		"trigger": map[string]any{"kind": "time", "time": "02:00"},
		"actions": []any{map[string]any{"kind": "button", "tile": id, "action": button.ID, "params": button.Params}}})
	if code != 200 {
		t.Fatalf("save: %d %v", code, saved)
	}
	night := app.rulesOf(1)[1]
	two := time.Date(2026, 11, 9, 2, 0, 10, 0, time.Local)
	if _, fire := app.ruleDue(1, night, two); !fire {
		t.Fatal("time rule did not fire")
	}
	if _, fire := app.ruleDue(1, night, two.Add(30*time.Second)); fire {
		t.Fatal("time rule fired twice in its minute")
	}
	// The bot now listens for the answer, even without the AI.
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, running := app.botChats("123456:AAH" + strings.Repeat("x", 32)); running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the bot does not run for a rule that asks")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	// The workflow waits for the answer.
	done := make(chan ruleLogEntry, 1)
	go func() { done <- app.runRule(ctx, 1, night, "It is 02:00", runAuto) }()
	ask := has("question", "Should I run this?")
	if restarted.Load() != 1 {
		t.Fatal("ran without asking")
	}
	yes := ask.Body["reply_markup"].(map[string]any)["inline_keyboard"].([]any)[0].([]any)[0].(map[string]any)["callback_data"]
	// A stranger's chat cannot answer, the user's can – once.
	tap := func(chat int64) {
		tg.push(map[string]any{"callback_query": map[string]any{"id": "r", "data": yes,
			"from":    map[string]any{"id": chat, "is_bot": false, "first_name": "X"},
			"message": map[string]any{"message_id": 9, "chat": map[string]any{"id": chat, "type": "private"}}}})
	}
	tap(99)
	time.Sleep(200 * time.Millisecond)
	if restarted.Load() != 1 {
		t.Fatal("a stranger ran the rule's button")
	}
	tap(42)
	has("result", "✅ Restart · NAS")
	if e := <-done; !e.OK || !strings.Contains(e.Result, "✅ Restart · NAS") {
		t.Fatalf("after the answer: %+v", e)
	}
	if restarted.Load() != 2 {
		t.Fatalf("restarted %d", restarted.Load())
	}
	tap(42)
	has("expired", "expired")
	if restarted.Load() != 2 {
		t.Fatal("the confirmation ran twice")
	}

	// Down and back up.
	luis.call("PUT", fmt.Sprintf("/api/rules/%s", night.ID), map[string]any{"name": "NAS down", "on": true, "trigger": map[string]any{"kind": "down", "tile": id, "for": 2},
		"actions": []any{map[string]any{"kind": "notify", "text": "down"}}})
	down := app.rulesOf(1)[1]
	app.mu.Lock()
	app.latest[id] = TileStatus{Online: false}
	app.mu.Unlock()
	if _, fire := app.ruleDue(1, down, now); fire {
		t.Fatal("down fired before 2 minutes")
	}
	if why, fire := app.ruleDue(1, down, now.Add(2*time.Minute)); !fire || !strings.Contains(why, "NAS") {
		t.Fatalf("down did not fire: %q", why)
	}

	// "Run now" counts as the confirmation; the log keeps it all.
	if code, res := luis.call("POST", fmt.Sprintf("/api/rules/%s/run", r.ID), nil); code != 200 || res["ok"] != true {
		t.Fatalf("run now: %d %v", code, res)
	}
	var list struct {
		Rules []rule         `json:"rules"`
		Log   []ruleLogEntry `json:"log"`
	}
	getJSON(luis, "/api/rules", &list)
	if len(list.Rules) != 2 || len(list.Log) < 3 || list.Log[0].Why != "Started by hand" {
		t.Fatalf("list: %+v", list)
	}
	if code, _ := luis.call("DELETE", fmt.Sprintf("/api/rules/%s", r.ID), nil); code != 200 || len(app.rulesOf(1)) != 1 {
		t.Fatal("delete")
	}
}

// getJSON reads an answer that is not an object (a list) into out.
func getJSON(tc *testClient, path string, out any) int {
	resp, err := tc.c.Get(tc.base + path)
	if err != nil {
		tc.t.Fatal(err)
	}
	defer resp.Body.Close()
	json.NewDecoder(resp.Body).Decode(out)
	return resp.StatusCode
}
