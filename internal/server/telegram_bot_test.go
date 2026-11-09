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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/store"
)

// fakeTelegram is enough of the Bot API for the bot: it hands out queued
// updates and records what the bot sends.
type fakeTelegram struct {
	*httptest.Server
	mu      sync.Mutex
	queue   []map[string]any
	nextID  int64
	calls   []tgSent
	members map[int64]string // user → status in a group
}

type tgSent struct {
	Method string
	Body   map[string]any
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	f := &fakeTelegram{nextID: 100, members: map[int64]string{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		if strings.Contains(r.URL.Path, "/file/") {
			// A file the user sent: a photo or a text file.
			if strings.HasSuffix(r.URL.Path, ".txt") {
				io.WriteString(w, "Invoice 2026-11: 42 EUR, due on 30 November.")
				return
			}
			w.Write([]byte{0xff, 0xd8, 0xff, 0xe0, 'j', 'p', 'g'})
			return
		}
		var body map[string]any
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			// An upload (camera picture): its fields and the file's type.
			r.ParseMultipartForm(1 << 20)
			body = map[string]any{"chat_id": r.FormValue("chat_id"), "caption": r.FormValue("caption")}
			for field, files := range r.MultipartForm.File {
				body["file"] = field + ":" + files[0].Header.Get("Content-Type")
			}
		} else {
			json.NewDecoder(r.Body).Decode(&body)
		}
		ok := func(result any) { json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result}) }
		switch method {
		case "getFile":
			ok(map[string]any{"file_id": body["file_id"], "file_path": "files/" + fmt.Sprint(body["file_id"])})
		case "getMe":
			ok(map[string]any{"id": 1, "is_bot": true, "username": "heimbot"})
		case "getUpdates":
			if body["offset"] == float64(-1) {
				ok([]any{})
				return
			}
			f.mu.Lock()
			out := f.queue
			f.queue = nil
			f.mu.Unlock()
			if len(out) == 0 {
				time.Sleep(30 * time.Millisecond)
				out = []map[string]any{}
			}
			ok(out)
		case "getChatMember":
			f.mu.Lock()
			status := f.members[int64(body["user_id"].(float64))]
			f.mu.Unlock()
			ok(map[string]any{"status": firstNonEmptyStr(status, "member")})
		default:
			f.mu.Lock()
			f.calls = append(f.calls, tgSent{method, body})
			f.mu.Unlock()
			ok(true)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fakeTelegram) push(u map[string]any) {
	f.mu.Lock()
	f.nextID++
	u["update_id"] = f.nextID
	f.queue = append(f.queue, u)
	f.mu.Unlock()
}

// waitSent waits for a sent message that matches.
func (f *fakeTelegram) waitSent(t *testing.T, what string, match func(tgSent) bool) tgSent {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		for _, c := range f.calls {
			if match(c) {
				f.mu.Unlock()
				return c
			}
		}
		f.mu.Unlock()
		time.Sleep(20 * time.Millisecond)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	t.Fatalf("no %s; sent: %+v", what, f.calls)
	return tgSent{}
}

func (f *fakeTelegram) sentTo(chat float64) []tgSent {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []tgSent
	for _, c := range f.calls {
		if c.Method == "sendMessage" && c.Body["chat_id"] == chat {
			out = append(out, c)
		}
	}
	return out
}

func msgUpdate(chat int64, chatType string, from int64, text string) map[string]any {
	return map[string]any{"message": map[string]any{"message_id": 7, "text": text,
		"from": map[string]any{"id": from, "is_bot": false, "first_name": "Luis"},
		"chat": map[string]any{"id": chat, "type": chatType}}}
}

// The bot answers in the user's chat with the AI and its tools; an action is a
// button that runs once when tapped – and other chats get nothing.
func TestTelegramBot(t *testing.T) {
	var restarted atomic.Int32
	nas := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			restarted.Add(1)
		}
		io.WriteString(w, `{"free": "1.8 TB"}`)
	}))
	defer nas.Close()

	var mu sync.Mutex
	var streamed, sawPicture bool
	var lastSystem string
	cam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/jpeg")
		w.Write([]byte{0xff, 0xd8, 0xff, 0xe0, 'c', 'a', 'm'})
	}))
	defer cam.Close()
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		json.NewDecoder(r.Body).Decode(&got)
		msgs := got["messages"].([]any)
		mu.Lock()
		if got["stream"] == true {
			streamed = true
		}
		lastSystem = fmt.Sprint(msgs[0])
		mu.Unlock()
		tools, proposed := 0, false
		for _, m := range msgs {
			mm := m.(map[string]any)
			if mm["role"] == "tool" {
				tools++
				if mm["name"] == "propose_action" {
					proposed = true
				}
			}
		}
		call := func(id, name, args string) string {
			return fmt.Sprintf(`{"choices":[{"message":{"content":null,"tool_calls":[{"id":%q,"type":"function","function":{"name":%q,"arguments":%q}}]}}]}`, id, name, args)
		}
		say := func(text string) { fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, text) }
		last := msgs[len(msgs)-1].(map[string]any)
		lastText := fmt.Sprint(last["content"])
		switch {
		case strings.Contains(lastText, "situation report"):
			say("🟢 All is well. **NAS** has 1.8 TB free.")
		case strings.Contains(lastText, "stopped answering"):
			say("Proxmox shows VM 102 stopped.")
		case strings.Contains(lastText, "image_url"):
			mu.Lock()
			sawPicture = strings.Contains(lastText, "data:image/jpeg;base64,")
			mu.Unlock()
			say("It is a letter from the bank.")
		case strings.Contains(lastText, "Invoice 2026-11"):
			say("An invoice of 42 EUR, due on 30 November.")
		case tools == 0:
			io.WriteString(w, call("c1", "read_tile", `{"name":"NAS"}`))
		case !proposed:
			io.WriteString(w, call("c2", "propose_action", `{"action":"a1"}`))
		default:
			io.WriteString(w, `{"choices":[{"message":{"content":"**NAS** has 1.8 TB free. Tap the button to restart it."}}]}`)
		}
	}))
	defer model.Close()

	tg := newFakeTelegram(t)
	old := telegramAPI
	telegramAPI = tg.URL
	t.Cleanup(func() { telegramAPI = old })

	dir := t.TempDir()
	box, err := secret.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
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
	luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "NAS", "url": nas.URL, "integration": "customapi", "showWidget": true,
		"config": map[string]string{"stats": "Free = free", "buttons": "Restart = POST /restart"}})
	if code, res := luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "Front door", "integration": "camera", "showWidget": true,
		"config": map[string]string{"snapshot": cam.URL + "/snap.jpg", "still": "nur per Klick"}}); code != 200 {
		t.Fatalf("camera tile: %d %v", code, res)
	}
	luis.call("PUT", "/api/ai", map[string]any{"enabled": true, "url": model.URL + "/v1", "model": "m", "dashboard": true, "tools": true})
	luis.call("PUT", "/api/notify", map[string]any{"telegramToken": "123456:AAH" + strings.Repeat("x", 32)})
	if code, res := luis.call("PUT", "/api/settings", map[string]any{"telegramChat": "42", "telegramAI": "true"}); code != 200 {
		t.Fatalf("settings: %d %v", code, res)
	}
	tg.waitSent(t, "command menu", func(c tgSent) bool { return c.Method == "setMyCommands" })

	// A stranger writing to the bot gets nothing; the user's chat gets the answer.
	tg.push(msgUpdate(99, "private", 99, "Restart the NAS"))
	tg.push(msgUpdate(42, "private", 42, "Restart the NAS"))
	answer := tg.waitSent(t, "answer", func(c tgSent) bool { return c.Method == "sendMessage" && c.Body["chat_id"] == float64(42) })
	text := fmt.Sprint(answer.Body["text"])
	if !strings.Contains(text, "<b>NAS</b> has 1.8 TB free") || !strings.Contains(text, "read NAS") {
		t.Fatalf("answer: %q", text)
	}
	if len(tg.sentTo(99)) != 0 {
		t.Fatal("the bot answered a chat it does not belong to")
	}
	mu.Lock()
	if streamed || !strings.Contains(lastSystem, "Telegram") {
		t.Fatalf("model request: streamed %v, system %s", streamed, lastSystem)
	}
	mu.Unlock()
	if restarted.Load() != 0 {
		t.Fatal("the action ran without a tap")
	}

	// The proposal is a button; tapping it runs the action once.
	kb := answer.Body["reply_markup"].(map[string]any)["inline_keyboard"].([]any)
	button := kb[0].([]any)[0].(map[string]any)
	if !strings.Contains(fmt.Sprint(button["text"]), "Restart") {
		t.Fatalf("button: %v", button)
	}
	tap := func(data any) {
		tg.push(map[string]any{"callback_query": map[string]any{"id": "cb", "data": data,
			"from":    map[string]any{"id": 42, "is_bot": false, "first_name": "Luis"},
			"message": map[string]any{"message_id": 500, "chat": map[string]any{"id": 42, "type": "private"}}}})
	}
	// The tile asks before it runs: a question with Yes / Cancel first.
	tap(button["callback_data"])
	ask := tg.waitSent(t, "question", func(c tgSent) bool {
		return strings.Contains(fmt.Sprint(c.Body["text"]), "Restart") && c.Body["reply_markup"] != nil && c.Body["parse_mode"] == nil
	})
	yes := ask.Body["reply_markup"].(map[string]any)["inline_keyboard"].([]any)[0].([]any)[0].(map[string]any)["callback_data"]
	time.Sleep(100 * time.Millisecond)
	if restarted.Load() != 0 {
		t.Fatal("the action ran before the question was answered")
	}
	tap(yes)
	tg.waitSent(t, "confirmation", func(c tgSent) bool {
		return c.Method == "sendMessage" && strings.HasPrefix(fmt.Sprint(c.Body["text"]), "✅ Done: Restart")
	})
	if restarted.Load() != 1 {
		t.Fatalf("restarted %d times", restarted.Load())
	}
	tap(yes)
	tg.waitSent(t, "expired note", func(c tgSent) bool {
		return c.Method == "answerCallbackQuery" && strings.Contains(fmt.Sprint(c.Body["text"]), "expired")
	})
	time.Sleep(100 * time.Millisecond)
	if restarted.Load() != 1 {
		t.Fatal("a button ran twice")
	}

	// /new forgets the conversation; switching the AI off stops the bot.
	tg.push(msgUpdate(42, "private", 42, "/new"))
	tg.waitSent(t, "reset note", func(c tgSent) bool { return strings.Contains(fmt.Sprint(c.Body["text"]), "New conversation") })
	has := func(what, text string) tgSent {
		return tg.waitSent(t, what, func(c tgSent) bool {
			return strings.Contains(fmt.Sprint(c.Body["text"])+fmt.Sprint(c.Body["caption"]), text)
		})
	}

	// The situation report on request.
	tg.push(msgUpdate(42, "private", 42, "/status"))
	has("situation report", "All is well")

	// Memory: kept, handed to the AI, and forgotten with its button.
	tg.push(msgUpdate(42, "private", 42, "/remember My NAS is called tank"))
	has("remembered", "Remembered: My NAS is called tank")
	before := len(tg.sentTo(42))
	tg.push(msgUpdate(42, "private", 42, "How full is it?"))
	deadline := time.Now().Add(10 * time.Second)
	for len(tg.sentTo(42)) == before && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	if !strings.Contains(lastSystem, "My NAS is called tank") {
		t.Fatalf("memory not handed to the model: %s", lastSystem)
	}
	mu.Unlock()
	tg.push(msgUpdate(42, "private", 42, "/forget"))
	list := has("memory list", "This is what I remember")
	forget := list.Body["reply_markup"].(map[string]any)["inline_keyboard"].([]any)[0].([]any)[0].(map[string]any)
	tg.push(map[string]any{"callback_query": map[string]any{"id": "f1", "data": forget["callback_data"],
		"from":    map[string]any{"id": 42, "is_bot": false, "first_name": "Luis"},
		"message": map[string]any{"message_id": 502, "chat": map[string]any{"id": 42, "type": "private"}}}})
	has("forgotten", "Forgotten: My NAS is called tank")
	if len(app.memoryOf(1)) != 0 {
		t.Fatalf("memory left: %v", app.memoryOf(1))
	}

	// A photo goes to the model as a picture, a text file as text.
	photo := msgUpdate(42, "private", 42, "")
	msg := photo["message"].(map[string]any)
	delete(msg, "text")
	msg["photo"] = []any{map[string]any{"file_id": "small", "width": 90}, map[string]any{"file_id": "big", "width": 1280}}
	tg.push(photo)
	has("photo answer", "letter from the bank")
	mu.Lock()
	if !sawPicture {
		t.Fatal("the photo did not reach the model as a picture")
	}
	mu.Unlock()
	doc := msgUpdate(42, "private", 42, "")
	msg = doc["message"].(map[string]any)
	msg["text"], msg["caption"] = "", "what is this?"
	msg["document"] = map[string]any{"file_id": "inv.txt", "file_name": "inv.txt", "mime_type": "text/plain", "file_size": 60}
	tg.push(doc)
	has("file answer", "42 EUR")
	pdf := msgUpdate(42, "private", 42, "")
	msg = pdf["message"].(map[string]any)
	msg["document"] = map[string]any{"file_id": "x.pdf", "file_name": "x.pdf", "mime_type": "application/pdf", "file_size": 60}
	tg.push(pdf)
	has("pdf note", "read PDFs here yet")

	// The camera picture as a photo.
	tg.push(msgUpdate(42, "private", 42, "/camera"))
	shot := tg.waitSent(t, "camera picture", func(c tgSent) bool { return c.Method == "sendPhoto" })
	if !strings.Contains(fmt.Sprint(shot.Body["caption"]), "Front door") || shot.Body["file"] != "photo:image/jpeg" {
		t.Fatalf("camera picture: %v", shot.Body)
	}

	// The morning report comes at the time set, once a day.
	now := time.Now()
	luis.call("PUT", "/api/settings", map[string]any{"telegramReport": now.Format("15:04")})
	app.morningReports(ctx, now)
	app.morningReports(ctx, now)
	has("morning report", "Good morning!")
	time.Sleep(200 * time.Millisecond)
	n := 0
	tg.mu.Lock()
	for _, c := range tg.calls {
		if c.Method == "sendMessage" && fmt.Sprint(c.Body["chat_id"]) == "42" && strings.Contains(fmt.Sprint(c.Body["text"]), "Good morning") {
			n++
		}
	}
	tg.mu.Unlock()
	if n != 1 {
		t.Fatalf("%d morning reports", n)
	}
	if code, _ := luis.call("PUT", "/api/settings", map[string]any{"telegramReport": "7 Uhr"}); code != 400 {
		t.Fatal("a time that is not one was saved")
	}

	// Outages: several in one message, with the cause the AI sees on the dashboard.
	luis.call("PUT", "/api/settings", map[string]any{"notifyAI": "true"})
	msgs := []notification{
		{Down: true, Title: "Sonarr is not reachable", Body: "No answer since 14:02"},
		{Down: true, Title: "Radarr is not reachable", Body: "No answer since 14:02"},
	}
	why := app.explainOutages(1, "en", msgs)
	bundled := bundleNotifications("en", msgs)
	if why != "Proxmox shows VM 102 stopped." || len(bundled) != 1 || bundled[0].Title != "2 services are not reachable" || !strings.Contains(bundled[0].Body, "• Radarr") {
		t.Fatalf("outage: %q %+v", why, bundled)
	}

	// In a group the bot answers only when spoken to, and only admins may tap.
	luis.call("PUT", "/api/settings", map[string]any{"telegramChat": "-1001"})
	time.Sleep(200 * time.Millisecond)
	tg.push(msgUpdate(-1001, "supergroup", 7, "restart the NAS"))
	tg.push(msgUpdate(-1001, "supergroup", 7, "@heimbot restart the NAS"))
	groupAnswer := tg.waitSent(t, "group answer", func(c tgSent) bool { return c.Method == "sendMessage" && c.Body["chat_id"] == float64(-1001) })
	time.Sleep(200 * time.Millisecond)
	if n := len(tg.sentTo(-1001)); n != 1 {
		t.Fatalf("%d answers in the group, want 1 (only to the message that names the bot)", n)
	}
	gb := groupAnswer.Body["reply_markup"].(map[string]any)["inline_keyboard"].([]any)[0].([]any)[0].(map[string]any)
	tg.push(map[string]any{"callback_query": map[string]any{"id": "g1", "data": gb["callback_data"],
		"from":    map[string]any{"id": 7, "is_bot": false, "first_name": "Guest"},
		"message": map[string]any{"message_id": 501, "chat": map[string]any{"id": -1001, "type": "supergroup"}}}})
	tg.waitSent(t, "refusal", func(c tgSent) bool {
		return c.Method == "answerCallbackQuery" && strings.Contains(fmt.Sprint(c.Body["text"]), "Only admins")
	})
	if restarted.Load() != 1 {
		t.Fatal("a group member who is not an admin ran an action")
	}

	luis.call("PUT", "/api/settings", map[string]any{"telegramAI": "false"})
	deadline = time.Now().Add(3 * time.Second)
	for {
		if _, running := app.botChats("123456:AAH" + strings.Repeat("x", 32)); !running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the bot still runs after switching it off")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// In a group the bot answers only when spoken to, and only group admins may
// tap action buttons.
func TestTelegramGroupRules(t *testing.T) {
	b := &tgBot{name: "heimbot"}
	if cmd, rest := tgCommand("/web@heimbot what's new", b.name); cmd != "web" || rest != "what's new" {
		t.Fatalf("%q %q", cmd, rest)
	}
	if cmd, _ := tgCommand("/web@otherbot hi", b.name); cmd != "other" {
		t.Fatalf("a command for another bot: %q", cmd)
	}
	got := tgHTML("## Status\n**NAS** is `up <ok>` – [docs](https://x.y/?a=1&b=2)\n- one\n```\n<b>raw</b>\n```")
	want := "<b>Status</b>\n<b>NAS</b> is <code>up &lt;ok&gt;</code> – <a href=\"https://x.y/?a=1&amp;b=2\">docs</a>\n• one\n<pre>&lt;b&gt;raw&lt;/b&gt;</pre>"
	if got != want {
		t.Fatalf("tgHTML:\n%s\nwant\n%s", got, want)
	}
	parts := tgSplit(strings.Repeat("word ", 300)+"\n\n"+strings.Repeat("äöü ", 2000), 3800)
	for _, p := range parts {
		if len(p) > 3800 || !strings.HasPrefix(p, "word") && !strings.HasPrefix(p, "ä") && !strings.HasPrefix(p, "ö") && !strings.HasPrefix(p, "ü") {
			t.Fatalf("part of %d bytes starting %q", len(p), p[:10])
		}
	}
}
