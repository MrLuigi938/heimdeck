package integrations

import (
	"net/http"
	"testing"
	"time"
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

// Audit 2026-11-09, H3: fetches share their client (and its connections);
// the one for external accounts still only reaches the public internet.
func TestSharedClients(t *testing.T) {
	if NewClient(false) != NewClient(false) || NewClientFor(true, false) != NewClient(true) {
		t.Fatal("a new client for every fetch")
	}
	if NewClientFor(false, true) == NewClient(false) || NewClient(true) == NewClient(false) {
		t.Fatal("clients of different kinds are shared")
	}
	if _, err := NewClientFor(false, true).Get("http://127.0.0.1:1/"); !HomeNetworkBlocked(err) {
		t.Fatalf("the external client reached the home network: %v", err)
	}
}

// H12: results nobody asked for in a day go; logins are cleared once a day.
func TestSweepCaches(t *testing.T) {
	now := time.Now()
	remembered.Store("old", rememberedValue{at: now.Add(-25 * time.Hour), got: now.Add(-25 * time.Hour), val: 1})
	remembered.Store("fresh", rememberedValue{at: now, got: now, val: 1})
	sessions.Store("qbt", "cookie")
	ReceiveHook("k-gone", HookMessage{Title: "x"})
	ReceiveHook("k-kept", HookMessage{Title: "y"})
	KeepHooks(map[string]bool{"k-kept": true})
	if len(hookMessages("k-gone")) != 0 || len(hookMessages("k-kept")) != 1 {
		t.Fatal("webhook messages")
	}
	Sweep(now)                     // the first sweep only starts the day
	Sweep(now.Add(25 * time.Hour)) // a day later the logins go
	if _, ok := remembered.Load("old"); ok {
		t.Fatal("an old result stayed")
	}
	if _, ok := sessions.Load("qbt"); ok {
		t.Fatal("the login stayed")
	}
	remembered.Delete("fresh")
}
