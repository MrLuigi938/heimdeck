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

// 📋 gives the AI what the user's own tiles show – not the values they hid,
// not hidden groups, not other people's tiles – and only when the admin
// allows it.
func TestAIDashboardContext(t *testing.T) {
	var got map[string]any
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = nil
		json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Okay.\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer model.Close()
	nas := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"free": "1.8 TB", "pin": "4711"}`)
	}))
	defer nas.Close()

	_, base := multiUserServer(t)
	luis := newTestClient(t, base)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "de"})
	luis.call("POST", "/api/users", map[string]string{"username": "anna", "password": "anna-pw-123"})
	anna := newTestClient(t, base)
	anna.call("POST", "/api/login", map[string]string{"username": "anna", "password": "anna-pw-123"})

	_, g := luis.call("POST", "/api/groups", map[string]any{"name": "Server"})
	luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "NAS", "url": nas.URL, "integration": "customapi", "showWidget": true,
		"config": map[string]string{"stats": "Frei = free\nPIN = pin"}, "hide": []string{"PIN"}})
	_, hidden := luis.call("POST", "/api/groups", map[string]any{"name": "Tresor"})
	luis.call("PUT", fmt.Sprintf("/api/groups/%v", hidden["id"]), map[string]any{"name": "Tresor", "visibility": "hidden"})
	luis.call("POST", "/api/tiles", map[string]any{"groupId": hidden["id"], "name": "Geheimes Wiki", "url": "http://wiki.local", "integration": "link"})
	// A service that is down: its address (with a key) stays out.
	luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "Sonarr", "url": "http://127.0.0.1:1/?apikey=geheim123", "integration": "sonarr", "showWidget": true,
		"config": map[string]string{"apiKey": "geheim123"}})
	_, ag := anna.call("POST", "/api/groups", map[string]any{"name": "Annas Sachen"})
	anna.call("POST", "/api/tiles", map[string]any{"groupId": ag["id"], "name": "Annas Kalender", "url": "http://cal.local", "integration": "link"})

	ask := func(dashboard bool) (string, http.Header) {
		body := fmt.Sprintf(`{"dashboard":%v,"messages":[{"role":"user","text":"Wie viel Platz hat das NAS?"}]}`, dashboard)
		req, _ := http.NewRequest("POST", base+"/api/ai/chat", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := luis.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
		return fmt.Sprint(got["messages"].([]any)[0]), resp.Header
	}

	// Not allowed by the admin: nothing goes along, even when asked.
	luis.call("PUT", "/api/ai", map[string]any{"enabled": true, "url": model.URL + "/v1", "model": "m"})
	if sys, hdr := ask(true); strings.Contains(sys, "NAS") || hdr.Get("X-Dashboard-Tiles") != "" {
		t.Fatalf("dashboard sent without the admin's consent: %s", sys)
	}
	luis.call("PUT", "/api/ai", map[string]any{"enabled": true, "url": model.URL + "/v1", "model": "m", "dashboard": true})
	_, dash := luis.call("GET", "/api/dashboard", nil)
	if dash["ai"].(map[string]any)["dashboard"] != true {
		t.Fatalf("📋 not offered: %v", dash["ai"])
	}
	if sys, _ := ask(false); strings.Contains(sys, "1.8 TB") {
		t.Fatal("dashboard sent without 📋")
	}
	sys, hdr := ask(true)
	if !strings.Contains(sys, "NAS") || !strings.Contains(sys, "Frei 1.8 TB") || hdr.Get("X-Dashboard-Tiles") == "" {
		t.Fatalf("tile data missing: %s", sys)
	}
	for _, leak := range []string{"4711", "Geheimes Wiki", "Tresor", "Annas", nas.URL, "geheim123", "127.0.0.1"} {
		if strings.Contains(sys, leak) {
			t.Fatalf("%q leaked into the AI's context: %s", leak, sys)
		}
	}
}
