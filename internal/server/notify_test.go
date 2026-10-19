package server

import (
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
