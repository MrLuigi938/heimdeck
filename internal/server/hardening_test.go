package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// A login written into a camera address ends up encrypted, and is not in the
// answers to the browser or in the backup.
func TestCameraLoginLeavesTheAddress(t *testing.T) {
	st, base := multiUserServer(t)
	luis := newTestClient(t, base)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "de"})
	_, g := luis.call("POST", "/api/groups", map[string]any{"name": "Kameras"})
	_, created := luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "Eingang", "integration": "camera", "showWidget": true,
		"config": map[string]string{"rtsp": "rtsp://kamera:geheim-pw@192.168.1.50:554/live"}})
	saved, err := st.Tile(int64(created["id"].(float64)))
	if err != nil {
		t.Fatal(err)
	}
	if saved.Config["rtsp"] != "rtsp://192.168.1.50:554/live" || saved.Config["username"] != "kamera" || saved.Secrets["password"] != "geheim-pw" {
		t.Fatalf("config %v secrets %v", saved.Config, saved.Secrets)
	}
	for _, path := range []string{"/api/dashboard", "/api/export"} {
		req, _ := http.NewRequest("GET", base+path, nil)
		resp, err := luis.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if strings.Contains(string(body), "geheim-pw") {
			t.Fatalf("password in %s", path)
		}
	}
}

// Wrong passwords for one account do not lock out the others behind the same
// address (a reverse proxy), and do lock out further guesses for that account.
func TestLoginLockIsPerAccount(t *testing.T) {
	_, base := multiUserServer(t)
	luis := newTestClient(t, base)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "de"})
	luis.call("POST", "/api/users", map[string]string{"username": "anna", "password": "anna-pw-123"})
	attacker := newTestClient(t, base)
	last := 0
	for i := 0; i < 6; i++ {
		last, _ = attacker.call("POST", "/api/login", map[string]string{"username": "luis", "password": "falsch"})
	}
	if last != 429 {
		t.Fatalf("account not locked after 6 tries: %d", last)
	}
	if code, _ := attacker.call("POST", "/api/login", map[string]string{"username": "LUIS", "password": "admin-pw-123"}); code != 429 {
		t.Fatalf("a different spelling of the name gets around the lock: %d", code)
	}
	anna := newTestClient(t, base)
	if code, res := anna.call("POST", "/api/login", map[string]string{"username": "anna", "password": "anna-pw-123"}); code != 200 {
		t.Fatalf("anna locked out by someone guessing luis: %d %v", code, res)
	}
}

// Changing a password ends the other sessions of that account.
func TestPasswordChangeEndsOtherSessions(t *testing.T) {
	_, base := multiUserServer(t)
	here := newTestClient(t, base)
	here.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "de"})
	stolen := newTestClient(t, base)
	stolen.call("POST", "/api/login", map[string]string{"username": "luis", "password": "admin-pw-123"})
	if code, _ := stolen.call("GET", "/api/dashboard", nil); code != 200 {
		t.Fatalf("second session: %d", code)
	}
	if code, res := here.call("PUT", "/api/password", map[string]string{"current": "admin-pw-123", "new": "neues-passwort-1"}); code != 200 {
		t.Fatalf("change: %d %v", code, res)
	}
	if code, _ := stolen.call("GET", "/api/dashboard", nil); code != 401 {
		t.Fatalf("other session still works: %d", code)
	}
	if code, _ := here.call("GET", "/api/dashboard", nil); code != 200 {
		t.Fatalf("this session ended: %d", code)
	}
}

// The stored AI key goes only to the server it was entered for.
func TestAIKeyStaysWithItsServer(t *testing.T) {
	var got []string
	listen := func() *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			got = append(got, r.Header.Get("Authorization"))
			json.NewEncoder(w).Encode(map[string]any{"data": []any{}})
		}))
	}
	real, thief := listen(), listen()
	defer real.Close()
	defer thief.Close()
	_, base := multiUserServer(t)
	luis := newTestClient(t, base)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "de"})
	luis.call("PUT", "/api/ai", map[string]any{"enabled": true, "url": real.URL + "/v1", "model": "m", "apiKey": "sk-geheim"})

	luis.call("POST", "/api/ai/models", map[string]any{"url": real.URL + "/v1"})
	if got[len(got)-1] != "Bearer sk-geheim" {
		t.Fatalf("own server without the key: %q", got)
	}
	got = nil
	luis.call("POST", "/api/ai/models", map[string]any{"url": thief.URL + "/v1"})
	if len(got) != 1 || got[0] != "" {
		t.Fatalf("key sent to another server: %q", got)
	}
	_, res := luis.call("PUT", "/api/ai", map[string]any{"enabled": true, "url": thief.URL + "/v1", "model": "m"})
	if res["keySet"] != false {
		t.Fatalf("key kept for another server: %v", res)
	}
}

// The app page allows only its own scripts.
func TestAppContentSecurityPolicy(t *testing.T) {
	if !strings.Contains(appCSP, "script-src 'self'") || strings.Contains(appCSP, "'unsafe-inline'; img") == false || strings.Contains(fmt.Sprint(appCSP), "script-src 'self' 'unsafe") {
		t.Fatal(appCSP)
	}
}
