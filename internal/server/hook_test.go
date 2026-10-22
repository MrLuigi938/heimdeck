package server

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heimdeck/heimdeck/internal/store"
)

func TestHookEndpoint(t *testing.T) {
	s := testServer(t)
	g, _ := s.store.CreateGroup("A", 0)
	const key = "Abc123_-Abc123_-Abc123_-"
	tile := store.Tile{GroupID: g.ID, Name: "Hooks", Integration: "webhook", Config: map[string]string{"hookKey": key}}
	if err := s.store.SaveTile(&tile); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	send := func(method, path, ct, body string) int {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if c := send("POST", "/hook/"+key, "application/json", `{"title":"Backup","message":"fertig","status":"ok"}`); c != 200 {
		t.Fatalf("json: %d", c)
	}
	if c := send("POST", "/hook/"+key, "application/json", `{"msg":"Radarr down","monitor":{"name":"Radarr"},"heartbeat":{"status":0}}`); c != 200 {
		t.Fatalf("uptime kuma: %d", c)
	}
	if c := send("GET", "/hook/"+key+"?title=Klingel&message=Jemand+ist+da", "", ""); c != 200 {
		t.Fatalf("get: %d", c)
	}
	if c := send("POST", "/hook/"+key, "text/plain", "nur Text"); c != 200 {
		t.Fatalf("text: %d", c)
	}
	if c := send("POST", "/hook/falsch-falsch-falsch-falsch", "application/json", `{}`); c != 404 {
		t.Fatalf("wrong key: %d", c)
	}

	tile, _ = s.store.Tile(tile.ID)
	w, err := s.safeFetch(t.Context(), tile, "de")
	if err != nil {
		t.Fatal(err)
	}
	titles := []string{}
	for _, it := range w.Items {
		titles = append(titles, it.Title+"|"+it.Subtitle)
	}
	all := strings.Join(titles, "\n")
	for _, want := range []string{"nur Text|", "Klingel|Jemand ist da · ", "Radarr|Radarr down · ", "Backup|fertig · "} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in\n%s", want, all)
		}
	}
	if len(w.Alerts) != 1 || w.Alerts[0] != "Radarr" {
		t.Errorf("alerts %v", w.Alerts)
	}
}
