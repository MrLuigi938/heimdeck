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

// Workflows from Telegram: drafted from a sentence, saved only on a tap,
// switched and changed from the list – and the AI watcher, which may only
// send messages.
func TestTelegramWorkflowsAndWatcher(t *testing.T) {
	nas := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"free": "0.4 TB"}`)
	}))
	defer nas.Close()

	var tileID atomic.Int64
	var watched atomic.Int32
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		json.NewDecoder(r.Body).Decode(&got)
		msgs := got["messages"].([]any)
		system := fmt.Sprint(msgs[0].(map[string]any)["content"])
		last := fmt.Sprint(msgs[len(msgs)-1].(map[string]any)["content"])
		say := func(text string) { fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, text) }
		id := tileID.Load()
		switch {
		case strings.Contains(system, "You watch a self-hosted home dashboard"):
			watched.Add(1)
			say(`{"alert": true, "reason": "The NAS has only 0.4 TB left."}`)
		case strings.Contains(last, "Change this workflow"):
			say(`{"name":"NAS full","triggers":[{"kind":"time","time":"08:00"}],"steps":[{"kind":"notify","text":"Check the NAS"}]}`)
		case strings.Contains(last, "restart"):
			say(fmt.Sprintf(`{"name":"Watch and restart","triggers":[{"kind":"ai","prompt":"anything odd","every":30}],"steps":[{"kind":"button","tile":%d,"button":0}]}`, id))
		default:
			say(fmt.Sprintf("Here it is:\n```json\n"+`{"name":"NAS full","triggers":[{"kind":"value","tile":%d,"stat":"Free","op":"<","value":"1 TB"}],"steps":[{"kind":"notify","text":"The NAS is nearly full"}]}`+"\n```", id))
		}
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
	go app.TelegramBots(ctx)

	luis := newTestClient(t, srv.URL)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "en"})
	_, g := luis.call("POST", "/api/groups", map[string]any{"name": "Server"})
	_, nasTile := luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "NAS", "url": nas.URL, "integration": "customapi", "showWidget": true,
		"config": map[string]string{"stats": "Free = free", "buttons": "Restart = POST /restart"}})
	tileID.Store(int64(nasTile["id"].(float64)))
	luis.call("PUT", "/api/ai", map[string]any{"enabled": true, "url": model.URL + "/v1", "model": "m", "dashboard": true})
	luis.call("PUT", "/api/notify", map[string]any{"telegramToken": "123456:AAH" + strings.Repeat("x", 32)})
	luis.call("PUT", "/api/settings", map[string]any{"telegramChat": "42", "telegramAI": "true"})
	tg.waitSent(t, "command menu", func(c tgSent) bool { return c.Method == "setMyCommands" })

	users, _ := st.Users()
	luisID := users[0].ID
	msgID := 600
	tap := func(data string) {
		msgID++
		tg.push(map[string]any{"callback_query": map[string]any{"id": fmt.Sprint("q", msgID), "data": data,
			"from":    map[string]any{"id": 42, "is_bot": false, "first_name": "Luis"},
			"message": map[string]any{"message_id": msgID, "chat": map[string]any{"id": 42, "type": "private"}}}})
	}
	sent := func(what, contains string) tgSent {
		return tg.waitSent(t, what, func(c tgSent) bool {
			return (c.Method == "sendMessage" || c.Method == "editMessageText") && strings.Contains(fmt.Sprint(c.Body["text"]), contains)
		})
	}
	button := func(c tgSent, prefix string) string {
		b, _ := json.Marshal(c.Body["reply_markup"])
		var kb struct {
			Rows [][]struct {
				Data string `json:"callback_data"`
			} `json:"inline_keyboard"`
		}
		json.Unmarshal(b, &kb)
		for _, row := range kb.Rows {
			for _, k := range row {
				if strings.HasPrefix(k.Data, prefix) {
					return k.Data
				}
			}
		}
		return ""
	}

	// A draft from a sentence: what it does and a dry run – nothing saved yet.
	tg.push(msgUpdate(42, "private", 42, "/workflow tell me when the NAS is nearly full"))
	draft := sent("draft", "Draft: NAS full")
	text := fmt.Sprint(draft.Body["text"])
	if !strings.Contains(text, "📈 NAS · Free &lt; 1 TB") || !strings.Contains(text, "Dry run now:") || !strings.Contains(text, "would send: The NAS is nearly full") {
		t.Fatalf("draft: %s", text)
	}
	if len(app.rulesOf(luisID)) != 0 {
		t.Fatal("the draft was saved before the tap")
	}
	save := button(draft, "ws:")
	tap(save)
	sent("saved", "Saved: NAS full")
	rules := app.rulesOf(luisID)
	if len(rules) != 1 || !rules[0].On || rules[0].NoAsk {
		t.Fatalf("saved: %+v", rules)
	}
	// Each draft is saved once.
	tap(save)
	tg.waitSent(t, "expired", func(c tgSent) bool {
		return c.Method == "answerCallbackQuery" && strings.Contains(fmt.Sprint(c.Body["text"]), "expired")
	})
	if len(app.rulesOf(luisID)) != 1 {
		t.Fatal("a draft was saved twice")
	}

	// The list: switch off, then change with a sentence.
	tg.push(msgUpdate(42, "private", 42, "/automations"))
	list := sent("list", "Your workflows:")
	tap(button(list, "wt:"))
	sent("switched", "⏸ NAS full")
	if app.rulesOf(luisID)[0].On {
		t.Fatal("not switched off")
	}
	tap(button(list, "we:"))
	sent("change question", "What should be different")
	tg.push(msgUpdate(42, "private", 42, "at 8 in the morning instead"))
	changed := sent("changed", "Changed: NAS full")
	tap(button(changed, "ws:"))
	sent("change saved", "Saved switched off: NAS full")
	rules = app.rulesOf(luisID)
	if len(rules) != 1 || rules[0].ID != list.Body["reply_markup"].(map[string]any)["inline_keyboard"].([]any)[0].([]any)[0].(map[string]any)["callback_data"].(string)[3:] ||
		rules[0].Triggers[0].Kind != "time" || rules[0].Triggers[0].Time != "08:00" || rules[0].On {
		t.Fatalf("changed: %+v", rules)
	}

	// A watcher with a button: shown with the problem, nothing to save.
	tg.push(msgUpdate(42, "private", 42, "/workflow watch everything and restart the NAS"))
	bad := sent("refused draft", "Watch and restart")
	if !strings.Contains(fmt.Sprint(bad.Body["text"]), "may only send messages") || button(bad, "ws:") != "" {
		t.Fatalf("watcher with a button: %v", bad.Body)
	}

	// The watcher: asked in the background, fires once on its answer.
	var ru rule
	ru.Name = "Watcher"
	ru.On = true
	ru.Triggers = []ruleTrigger{{Kind: "ai", Prompt: "Tell me when a disk is nearly full", Every: 15}}
	ru.Steps = []ruleAction{{Kind: "notify", Text: "Look"}}
	if code, res := luis.call("POST", "/api/rules", ru); code != 200 {
		t.Fatalf("watcher: %d %v", code, res)
	}
	ru.Steps = []ruleAction{{Kind: "button", Tile: tileID.Load(), Action: "a0"}}
	if code, _ := luis.call("POST", "/api/rules", ru); code != 400 {
		t.Fatalf("a watcher with a button was saved: %d", code)
	}
	var watcher rule
	for _, r := range app.rulesOf(luisID) {
		if r.Name == "Watcher" {
			watcher = r
		}
	}
	now := time.Now()
	if _, fire := app.ruleDue(luisID, watcher, now); fire {
		t.Fatal("fired before the AI answered")
	}
	deadline := time.Now().Add(5 * time.Second)
	for watched.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	why, fire := app.ruleDue(luisID, watcher, now.Add(30*time.Second))
	if !fire || !strings.Contains(why, "0.4 TB left") {
		t.Fatalf("watcher: %v %q", fire, why)
	}
	if _, again := app.ruleDue(luisID, watcher, now.Add(time.Minute)); again || watched.Load() != 1 {
		t.Fatalf("fired again or asked again too soon: %v %d", again, watched.Load())
	}
	// The dry run asks it once.
	var dry struct {
		Lines []string `json:"lines"`
	}
	b, _ := json.Marshal(watcher)
	var body map[string]any
	json.Unmarshal(b, &body)
	_, res := luis.call("POST", "/api/rules/dryrun", body)
	if !strings.Contains(fmt.Sprint(res["lines"]), "would report now: The NAS has only 0.4 TB left") {
		t.Fatalf("dry run: %v", res)
	}
	_ = dry
}
