package server

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/store"
)

func testServer(t *testing.T) *Server {
	t.Helper()
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
	return New(st, nil, nil, nil)
}

func TestOutageNotifications(t *testing.T) {
	var mu sync.Mutex
	var got []string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		if r.URL.Path == "/topic" {
			got = append(got, "ntfy:"+r.Header.Get("Title")+"|"+string(b)+"|"+r.Header.Get("Authorization"))
		} else {
			var m struct {
				Embeds []struct{ Title string } `json:"embeds"`
			}
			json.Unmarshal(b, &m)
			got = append(got, "discord:"+m.Embeds[0].Title)
		}
		mu.Unlock()
	}))
	defer hook.Close()

	s := testServer(t)
	sealed, _ := s.store.SealSecret([]byte(`{"discordWebhook":"` + hook.URL + `/discord","ntfyToken":"tk_1"}`))
	s.store.SaveSettings(map[string]string{notifySecretsKey: sealed, "ntfyUrl": hook.URL + "/topic", "notifyAfter": "2", "language": "de"})
	tiles := []store.Tile{{ID: 1, Name: "Radarr", URL: "http://radarr:7878", ExternalURL: "https://radarr.example.de"}}

	round := func(online, ext bool) map[int64]TileStatus {
		latest := map[int64]TileStatus{1: {Online: online, Ext: &Check{Online: ext}}}
		s.trackOutages(tiles, latest, time.Now())
		return latest
	}
	wait := func(n int) {
		for i := 0; i < 50; i++ {
			mu.Lock()
			l := len(got)
			mu.Unlock()
			if l >= n {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		t.Fatalf("expected %d messages, got %v", n, got)
	}

	if l := round(false, true); l[1].Alert != "" {
		t.Fatal("alert after the first failed check")
	}
	if l := round(false, true); l[1].Alert != "int" {
		t.Fatalf("alert %q", l[1].Alert)
	}
	wait(2)
	round(false, true) // still down: no new message
	round(true, true)
	wait(4)
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 4 {
		t.Fatalf("messages: %v", got)
	}
	all := strings.Join(got, "\n")
	for _, want := range []string{"discord:🔴 Radarr ist nicht erreichbar", "discord:✅ Radarr ist wieder erreichbar", "Bearer tk_1", "Ausfall nach"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in\n%s", want, all)
		}
	}
}

func TestTelegram(t *testing.T) {
	const token = "123456789:AAH1234567890abcdefghijklmnopqrstu"
	var got struct {
		path string
		body map[string]any
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got.path = r.URL.Path
		json.NewDecoder(r.Body).Decode(&got.body)
		if got.body["chat_id"] != "-100123" {
			w.WriteHeader(400)
		}
	}))
	defer api.Close()
	old := telegramAPI
	telegramAPI = api.URL
	defer func() { telegramAPI = old }()

	m := notification{Down: true, Title: "Radarr <intern> ist nicht erreichbar", Body: "Seit 12:00", URL: "http://radarr:7878"}
	if err := sendTelegram(context.Background(), token, "-100123", m); err != nil {
		t.Fatal(err)
	}
	text, _ := got.body["text"].(string)
	if got.path != "/bot"+token+"/sendMessage" || got.body["parse_mode"] != "HTML" || !strings.Contains(text, "<b>Radarr &lt;intern&gt;") || !strings.HasPrefix(text, "🔴") {
		t.Fatalf("path %s body %v", got.path, got.body)
	}
	if err := sendTelegram(context.Background(), token, "42", m); err == nil || !strings.Contains(err.Error(), "Chat-ID") {
		t.Fatalf("wrong chat: %v", err)
	}

	// Tokens are checked before they are stored, and never sent back.
	s := testServer(t)
	put := func(body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("PUT", "/api/notify", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		http.HandlerFunc(s.handleSaveNotify).ServeHTTP(rec, req)
		return rec
	}
	if rec := put(`{"telegramToken":"nope"}`); rec.Code != 400 {
		t.Fatalf("invalid token accepted: %d", rec.Code)
	}
	if rec := put(`{"telegramToken":"` + token + `"}`); rec.Code != 200 || strings.Contains(rec.Body.String(), token) || !strings.Contains(rec.Body.String(), `"telegram":true`) {
		t.Fatalf("save: %d %s", rec.Code, rec.Body.String())
	}
	if s.notifySecrets().TelegramToken != token {
		t.Fatal("token not stored")
	}
}
