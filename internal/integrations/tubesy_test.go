package integrations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func fakeTubesy(t *testing.T, readOnly bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "tsy_ok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if readOnly && r.Method != http.MethodGet {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/v1/stats":
			json.NewEncoder(w).Encode(map[string]any{
				"user":        map[string]any{"role": "admin"},
				"downloads":   map[string]any{"active": 0},
				"speed_bytes": 2 * 1024 * 1024,
				"instance": map[string]any{
					"downloads":       map[string]any{"queued": 3, "active": 1, "completed": 9, "failed": 2, "completed_24h": 4},
					"disk_free_bytes": 50 * 1024 * 1024 * 1024,
				},
			})
		case "/api/v1/queue":
			json.NewEncoder(w).Encode([]map[string]any{{
				"id": 7, "username": "max", "title": "Ein Video", "status": "downloading",
				"progress": 42.5, "speed": 2 * 1024 * 1024, "eta": 90,
			}})
		case "/api/v1/download":
			w.WriteHeader(http.StatusUnprocessableEntity)
			w.Write([]byte(`{"detail":"Unsupported URL: https://example.com"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func TestTubesyFetch(t *testing.T) {
	srv := fakeTubesy(t, false)
	defer srv.Close()
	in := &Instance{BaseURL: srv.URL, PublicURL: "https://tubesy.example.com", Values: map[string]string{"apiKey": "tsy_ok"}, Client: srv.Client(), Lang: "de"}

	w, err := tubesy{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, s := range w.Stats {
		got[s.Label] = s.Value
	}
	// Admin keys show instance-wide numbers.
	want := map[string]string{"Tempo": "2,0 MB/s", "Aktiv": "1", "Wartet": "3", "Heute fertig": "4", "Fehler": "2", "Frei": "50,0 GB"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("stat %s = %q, want %q (all: %v)", k, got[k], v, got)
		}
	}
	if len(w.Items) != 1 || !strings.Contains(w.Items[0].Subtitle, "@max") || *w.Items[0].Progress != 42.5 {
		t.Errorf("unexpected items: %+v", w.Items)
	}
	if a := w.Items[0].Actions; len(a) != 1 || a[0].ID != "cancel" || a[0].Params["id"] != "7" {
		t.Errorf("unexpected actions: %+v", a)
	}
}

func TestTubesyErrors(t *testing.T) {
	srv := fakeTubesy(t, true)
	defer srv.Close()
	ctx := context.Background()
	in := &Instance{BaseURL: srv.URL, Values: map[string]string{"apiKey": "tsy_ok"}, Client: srv.Client(), Lang: "de"}
	if err := (tubesy{}).Action(ctx, in, "download", map[string]string{"url": "https://example.com"}); err == nil || !strings.Contains(err.Error(), "nur lesen") {
		t.Errorf("read-only key: %v", err)
	}
	bad := &Instance{BaseURL: srv.URL, Values: map[string]string{"apiKey": "nope"}, Client: srv.Client(), Lang: "de"}
	if err := (tubesy{}).Test(ctx, bad); err == nil || !strings.Contains(err.Error(), "lehnt den API-Schlüssel ab") {
		t.Errorf("bad key: %v", err)
	}
	if err := (tubesy{}).Action(ctx, in, "download", map[string]string{"url": "javascript:alert(1)"}); err == nil {
		t.Error("non-http URL must be rejected")
	}
}

func TestTubesyDetail(t *testing.T) {
	if got := tubesyDetail(`{"detail":"Unsupported URL: https://x.y"}`); got != "Unsupported URL: https://x.y" {
		t.Errorf("got %q", got)
	}
	if got := tubesyDetail(`{"detail":"abgeschnitten …`); got != "abgeschnitten …" {
		t.Errorf("truncated body: got %q", got)
	}
}
