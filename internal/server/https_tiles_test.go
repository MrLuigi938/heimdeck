package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/store"
)

// Proxmox and UniFi Protect with their own certificates (the usual case):
// they answer through Heimdeck with "do not check the certificate", also when
// the address first redirects, when other tiles share the connections, and
// after the tile is saved again without entering the key a second time.
func TestSelfSignedServicesAfter17(t *testing.T) {
	var seen []string
	svc := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+" "+r.Header.Get("Authorization")+r.Header.Get("X-API-KEY"))
		switch {
		case strings.HasPrefix(r.URL.Path, "/api2/json/") && r.Header.Get("Authorization") != "PVEAPIToken=root@pam!hd=s3cret":
			w.WriteHeader(401)
		case strings.HasPrefix(r.URL.Path, "/proxy/protect/") && r.Header.Get("X-API-KEY") != "protect-key":
			w.WriteHeader(401)
		case r.URL.Path == "/api2/json/version":
			io.WriteString(w, `{"data":{"version":"9.0.10"}}`)
		case strings.HasPrefix(r.URL.Path, "/api2/json/cluster/resources"):
			io.WriteString(w, `{"data":[{"type":"node","node":"pve","status":"online","maxcpu":8,"cpu":0.1,"mem":1,"maxmem":4},{"type":"qemu","vmid":100,"name":"vm","node":"pve","status":"running"}]}`)
		case strings.HasPrefix(r.URL.Path, "/api2/json/"):
			io.WriteString(w, `{"data":[]}`)
		case r.URL.Path == "/proxy/protect/integration/v1/meta/info":
			io.WriteString(w, `{"applicationVersion":"5.0"}`)
		case r.URL.Path == "/proxy/protect/integration/v1/cameras":
			io.WriteString(w, `[{"id":"c1","name":"Door","state":"CONNECTED"}]`)
		default:
			io.WriteString(w, `[]`)
		}
	}))
	defer svc.Close()

	dir := t.TempDir()
	box, _ := secret.Open(dir)
	st, err := store.Open(filepath.Join(dir, "test.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	app := New(st, auth.New(st, false), nil, nil)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	luis := newTestClient(t, srv.URL)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "en"})
	_, g := luis.call("POST", "/api/groups", map[string]any{"name": "Server"})

	make := func(name, integration string, secrets map[string]string) int64 {
		code, res := luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": name, "url": svc.URL, "integration": integration,
			"showWidget": true, "insecure": true, "secrets": secrets})
		if code != 200 {
			t.Fatalf("%s: %d %v", name, code, res)
		}
		return int64(res["id"].(float64))
	}
	pve := make("Proxmox", "proxmox", map[string]string{"token": "root@pam!hd=s3cret"})
	protect := make("Protect", "unifi-protect", map[string]string{"apiKey": "protect-key"})

	widget := func(id int64) map[string]any {
		var out map[string]any
		getJSON(luis, fmt.Sprintf("/api/tiles/%d/widget", id), &out)
		return out
	}
	for round := 0; round < 2; round++ { // the second round reuses the shared connections
		for _, id := range []int64{pve, protect} {
			app.forget(id)
			if w := widget(id); w["error"] != nil && w["error"] != "" {
				t.Fatalf("round %d, tile %d: %v (requests: %v)", round, id, w["error"], seen)
			}
		}
	}
	// Saved again from the editor (no key sent): the keys stay.
	for _, id := range []int64{pve, protect} {
		tile, _ := st.Tile(id)
		code, _ := luis.call("PUT", fmt.Sprintf("/api/tiles/%d", id), map[string]any{"groupId": g["id"], "name": tile.Name, "url": svc.URL, "integration": tile.Integration, "showWidget": true, "insecure": true})
		if s, _ := st.Tile(id); code != 200 || len(s.Secrets) == 0 {
			t.Fatalf("tile %d lost its key on saving: %d %v", id, code, s.Secrets)
		}
		app.forget(id)
		if w := widget(id); w["error"] != nil && w["error"] != "" {
			t.Fatalf("after saving, tile %d: %v", id, w["error"])
		}
	}
}
