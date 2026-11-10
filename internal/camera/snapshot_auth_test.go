package camera

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// Audit 2026-11-09, H5: the password goes out only after the camera asks,
// and to a camera that wants digest never readable as basic.
func TestSnapshotSendsLoginOnlyAsAsked(t *testing.T) {
	for _, scheme := range []string{"Digest", "Basic"} {
		var mu sync.Mutex
		var seen []string
		cam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			mu.Lock()
			seen = append(seen, r.Header.Get("Authorization"))
			mu.Unlock()
			if !strings.HasPrefix(r.Header.Get("Authorization"), scheme+" ") {
				if scheme == "Digest" {
					w.Header().Set("WWW-Authenticate", `Digest realm="cam", nonce="abc", qop="auth"`)
				} else {
					w.Header().Set("WWW-Authenticate", `Basic realm="cam"`)
				}
				w.WriteHeader(401)
				return
			}
			w.Write([]byte{0xff, 0xd8, 0xff})
		}))
		data, _, err := Snapshot(context.Background(), http.DefaultClient, cam.URL+"/snap.jpg", "admin", "pw")
		cam.Close()
		if err != nil || len(data) != 3 {
			t.Fatalf("%s: %v", scheme, err)
		}
		if len(seen) != 2 || seen[0] != "" || !strings.HasPrefix(seen[1], scheme+" ") {
			t.Fatalf("%s: logins sent %q", scheme, seen)
		}
	}
}
