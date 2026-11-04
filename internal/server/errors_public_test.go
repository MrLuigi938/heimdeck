package server

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// When a service fails, the tile says so – without its address or key, also
// for error texts Heimdeck does not know. Someone it is shared with gets only
// that it is not available.
func TestTileErrorsHideAddressAndKey(t *testing.T) {
	// A service that hangs up mid-answer: Go's error quotes the whole address.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	hangup := "http://" + l.Addr().String()
	// A service that names its own address in the answer.
	talky := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(500)
		io.WriteString(w, "boom at http://10.1.2.3:8080/internal?X-Plex-Token=geheim123")
	}))
	defer talky.Close()

	_, base := multiUserServer(t)
	luis := newTestClient(t, base)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "de"})
	luis.call("POST", "/api/users", map[string]string{"username": "anna", "password": "anna-pw-123"})
	anna := newTestClient(t, base)
	anna.call("POST", "/api/login", map[string]string{"username": "anna", "password": "anna-pw-123"})

	_, dash := luis.call("GET", "/api/dashboard", nil)
	board := dash["boards"].([]any)[0].(map[string]any)["id"]
	_, g := luis.call("POST", "/api/groups", map[string]any{"name": "Dienste", "boardId": board})
	leaks := []string{"127.0.0.1", "10.1.2.3", "geheim123", "apikey", "X-Plex-Token", "http://", "dial tcp"}
	check := func(what, text string) {
		t.Helper()
		if text == "" || text == "<nil>" {
			t.Fatalf("%s: no message", what)
		}
		for _, leak := range leaks {
			if strings.Contains(text, leak) {
				t.Fatalf("%s: %q in %q", what, leak, text)
			}
		}
	}
	for _, url := range []string{hangup, talky.URL} {
		tile := map[string]any{"groupId": g["id"], "name": "Sab", "url": url, "integration": "sabnzbd", "showWidget": true, "config": map[string]string{"apikey": "geheim123"}}
		_, created := luis.call("POST", "/api/tiles", tile)
		path := fmt.Sprintf("/api/tiles/%v/widget", created["id"])
		_, w := luis.call("GET", path, nil)
		check("widget", fmt.Sprint(w["error"]))
		_, tested := luis.call("POST", "/api/tiles/test", tile)
		check("test", fmt.Sprint(tested["error"]))
		if code, res := luis.call("POST", fmt.Sprintf("/api/tiles/%v/action", created["id"]), map[string]any{"action": "pause"}); code >= 400 {
			check("action", fmt.Sprint(res["error"]))
		}
	}

	// Shared: not even the general reason.
	_, people := anna.call("GET", "/api/dashboard", nil)
	annaID := people["userId"]
	if code, res := luis.call("PUT", fmt.Sprintf("/api/boards/%v/shares/%v", board, annaID), map[string]any{"actions": false}); code != 200 {
		t.Fatalf("share: %d %v", code, res)
	}
	if code, res := anna.call("POST", fmt.Sprintf("/api/boards/%v/accept", board), map[string]any{}); code != 200 {
		t.Fatalf("accept: %d %v", code, res)
	}
	_, tiles := luis.call("GET", "/api/dashboard", nil)
	var id any
	for _, gr := range tiles["groups"].([]any) {
		for _, tl := range gr.(map[string]any)["tiles"].([]any) {
			id = tl.(map[string]any)["id"]
		}
	}
	_, w := anna.call("GET", fmt.Sprintf("/api/tiles/%v/widget", id), nil)
	if !strings.Contains(fmt.Sprint(w["error"]), "nicht verfügbar") && !strings.Contains(fmt.Sprint(w["error"]), "not available") {
		t.Fatalf("share viewer got %v", w)
	}
}
