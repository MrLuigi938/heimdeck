package server

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/heimdeck/heimdeck/internal/integrations"
)

// The status check opens a fresh connection every time (a stale kept
// connection must not make a site look down), counts a redirect as an
// answer, and leaves the tiles' shared client as it is.
func TestPingFreshConnections(t *testing.T) {
	var conns atomic.Int32
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			return
		}
		if r.URL.Path == "/" && r.Header.Get("User-Agent") == "Go-http-client/1.1" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		http.Redirect(w, r, "/login", http.StatusFound)
	}))
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	for range 3 {
		if c := ping(context.Background(), srv.URL, false, false); !c.Online {
			t.Fatal("site reported down")
		}
	}
	if n := conns.Load(); n != 3 {
		t.Errorf("connections: got %d, want 3 (one fresh per check)", n)
	}
	if integrations.NewClientFor(false, false).CheckRedirect == nil {
		t.Error("the shared client lost its redirect rule")
	}
	shared := integrations.NewClientFor(false, false)
	resp, err := shared.Get(srv.URL + "/app")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/login" {
		t.Error("the shared client stopped following redirects")
	}
}
