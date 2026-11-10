package integrations

import (
	"net/http"
	"testing"
)

// Audit 2026-11-09, H6: a service's key in its own header is not sent along
// when the service redirects to another server.
func TestKeysStayHomeOnRedirect(t *testing.T) {
	first, _ := http.NewRequest("GET", "http://sonarr.local/api", nil)
	next, _ := http.NewRequest("GET", "http://elsewhere.example/x", nil)
	next.Header.Set("X-Api-Key", "k")
	next.Header.Set("X-Plex-Token", "p")
	next.Header.Set("Accept", "application/json")
	if err := keepKeysHome(next, []*http.Request{first}); err != nil || next.Header.Get("X-Api-Key") != "" || next.Header.Get("X-Plex-Token") != "" || next.Header.Get("Accept") == "" {
		t.Fatalf("headers after the redirect: %v %v", err, next.Header)
	}
	same, _ := http.NewRequest("GET", "http://sonarr.local/other", nil)
	same.Header.Set("X-Api-Key", "k")
	keepKeysHome(same, []*http.Request{first})
	if same.Header.Get("X-Api-Key") != "k" {
		t.Fatal("the key was dropped on a redirect to the same server")
	}
}

// Audit 2026-11-09, M7: a login session or a looked-up result is shared only
// by tiles with the same keys, not by one that only names the same address.
func TestSessionsNeedTheSameKeys(t *testing.T) {
	a := &Instance{BaseURL: "http://qbt.local", Values: map[string]string{"username": "admin", "password": "secret"}}
	b := &Instance{BaseURL: "http://qbt.local", Values: map[string]string{"username": "admin", "password": ""}}
	c := &Instance{BaseURL: "http://qbt.local", Values: map[string]string{"username": "admin", "password": "secret"}}
	if sessionKey(a, "qbt") == sessionKey(b, "qbt") {
		t.Fatal("a tile without the password shares the session")
	}
	if sessionKey(a, "qbt") != sessionKey(c, "qbt") {
		t.Fatal("tiles with the same keys do not share the session")
	}
	ext := &Instance{BaseURL: "http://qbt.local", Values: a.Values, PublicOnly: true}
	if sessionKey(a, "qbt") == sessionKey(ext, "qbt") {
		t.Fatal("an external account shares the session")
	}
}
