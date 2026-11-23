package integrations

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func paiAuth(r *http.Request) bool { return r.Header.Get("x-api-key") == "pai_abcdefgh_secret" }

func paiDashboardRoutes() map[string]any {
	return map[string]any{
		"GET /api/dashboard": obj{
			"version": "4.5.1",
			"archive": obj{"mode": "builtin", "label": "Archive", "connected": true, "documents": 1234, "tags": 40, "correspondents": 55, "documentTypes": 12, "storageBytes": 2 * 1024 * 1024 * 1024},
			"processing": obj{"running": true, "paused": false, "queued": 3, "processedToday": 7,
				"current":          []obj{{"documentId": 77, "title": "Stromrechnung", "stage": "ocr"}},
				"lastError":        "Timeout beim Modell",
				"rateLimitedUntil": nil,
				"counts":           obj{"processed": 1100, "failed": 2, "skipped": 5}},
			"usage": obj{"totalTokens": 987654, "calls": 1200},
			"ai":    obj{"provider": "ollama", "model": "qwen3:8b"},
			"rag":   obj{"enabled": true},
		},
		"GET /api/rag/status": obj{"enabled": true, "state": "idle", "documents": 1200, "chunks": 9000},
		"GET /api/documents": obj{"count": nil, "nextCursor": nil, "results": []obj{
			{"id": 9, "title": "Kfz-Versicherung", "added": "2026-11-20T10:00:00Z", "status": "processed"},
			{"id": 8, "title": "Mietvertrag", "added": "2026-11-19T10:00:00Z", "status": nil},
		}},
		"GET /api/processing/problems": []obj{{"documentId": 5, "title": "Scan 0815", "status": "failed", "reason": "kein Text erkannt", "attempts": 3}},
		"POST /api/processing/scan":    obj{"queued": 4},
		"POST /api/processing/retry":   obj{"queued": 1},
		"POST /api/rag/search": obj{"mode": "hybrid", "tookMs": 40, "sources": []obj{
			{"documentId": 9, "title": "Kfz-Versicherung", "snippet": "doppelt"},
			{"documentId": 12, "title": "Haftpflicht", "snippet": "Die <mark>Versicherung</mark> zahlt &amp; deckt…"},
		}},
	}
}

func TestPaperlessAI(t *testing.T) {
	f := newFake(t, paiDashboardRoutes(), paiAuth)
	in := f.instance(map[string]string{"apiKey": "pai_abcdefgh_secret"})
	w, err := paperlessAI{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	for label, want := range map[string]string{"Dokumente": "1.234", "Verarbeitet": "1.100", "Heute": "7", "Warteschlange": "4",
		"Fehlgeschlagen": "2", "Übersprungen": "5", "Tokens": "987.654", "KI-Modell": "qwen3:8b", "Archiv": "eingebaut", "KI-Index": "1.200 Dokumente", "Korrespondenten": "55"} {
		if s[label] != want {
			t.Errorf("%s = %q, want %q (%v)", label, s[label], want, s)
		}
	}
	if len(w.Items) != 3 || w.Items[0].Title != "Stromrechnung" || w.Items[0].Badge != "OCR" || w.Items[1].Badge != "verarbeitet" || w.Items[2].Badge != "offen" {
		t.Fatalf("items %+v", w.Items)
	}
	if w.Items[1].URL != "https://svc.example.com/documents/9" || w.Items[1].Image != "/api/tiles/7/image?p=%2Fapi%2Fdocuments%2F9%2Fthumb" {
		t.Errorf("link/thumb %q %q", w.Items[1].URL, w.Items[1].Image)
	}
	if !strings.Contains(strings.Join(w.Alerts, "|"), "Timeout beim Modell") {
		t.Errorf("alerts %v", w.Alerts)
	}
	ids := ""
	for _, a := range w.Actions {
		ids += a.ID + " "
	}
	if ids != "scan pause retry " {
		t.Errorf("actions %q", ids)
	}

	// The list of problems, each with a retry button.
	in.Values["list"] = "Probleme"
	w, err = paperlessAI{}.Fetch(context.Background(), in)
	if err != nil || len(w.Items) != 2 || w.Items[1].Subtitle != "kein Text erkannt" || w.Items[1].Actions[0].Params["id"] != "5" {
		t.Fatalf("problems %v %+v", err, w.Items)
	}
	if err := (paperlessAI{}).Action(context.Background(), in, "retry", map[string]string{"id": "5"}); err != nil || f.body("POST /api/processing/retry") != `{"ids":[5]}` {
		t.Errorf("retry %v %q", err, f.body("POST /api/processing/retry"))
	}
	if err := (paperlessAI{}).Action(context.Background(), in, "scan", nil); err != nil {
		t.Error(err)
	}

	// Thumbnails only from the document thumb path, with the key.
	if p, h, err := (paperlessAI{}).ImageRequest(context.Background(), in, "/api/documents/9/thumb"); err != nil || p != "/api/documents/9/thumb" || h["x-api-key"] != "pai_abcdefgh_secret" {
		t.Errorf("thumb %q %v %v", p, h, err)
	}
	if _, _, err := (paperlessAI{}).ImageRequest(context.Background(), in, "/api/documents/9/file"); err == nil {
		t.Error("file download must not be proxied")
	}
}

func TestPaperlessAISearch(t *testing.T) {
	f := newFake(t, paiDashboardRoutes(), paiAuth)
	in := f.instance(map[string]string{"apiKey": "pai_abcdefgh_secret"})
	items, err := paperlessAI{}.Search(context.Background(), in, "versicherung")
	if err != nil || len(items) != 2 || items[0].URL != "https://svc.example.com/documents/9" {
		t.Fatalf("%v %+v", err, items)
	}
	if f.body("POST /api/rag/search") != "" {
		t.Error("AI search asked although switched off")
	}
	// With the search by meaning: the AI hits that full text did not find, once.
	in.Values["aiSearch"] = "true"
	items, err = paperlessAI{}.Search(context.Background(), in, "versicherung")
	if err != nil || len(items) != 3 || items[2].Title != "Haftpflicht" || items[2].Badge != "KI" {
		t.Fatalf("%v %+v", err, items)
	}
	if !strings.Contains(items[2].Subtitle, "Die Versicherung zahlt & deckt") {
		t.Errorf("snippet %q", items[2].Subtitle)
	}
}

func TestPaperlessAIErrors(t *testing.T) {
	f := newFake(t, paiDashboardRoutes(), paiAuth)
	_, err := paperlessAI{}.Fetch(context.Background(), f.instance(map[string]string{"apiKey": "falsch"}))
	if err == nil || !strings.Contains(err.Error(), "lehnt den API-Key ab") {
		t.Fatalf("got %v", err)
	}
	g := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		io.WriteString(w, `{"error":"Only administrators can do this","code":"admin_required"}`)
	}))
	defer g.Close()
	err = paperlessAI{}.Action(context.Background(), &Instance{BaseURL: g.URL, Client: g.Client(), Lang: "de", Values: map[string]string{"apiKey": "k"}}, "pause", nil)
	if err == nil || !strings.Contains(err.Error(), "Administration") {
		t.Fatalf("got %v", err)
	}
}

// Paperless-AI 3.x: no dashboard, only the processing state; no document search.
func TestPaperlessAILegacy(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /api/processing-status": obj{"currentlyProcessing": nil, "processedToday": 4, "isProcessing": false,
			"lastProcessed": obj{"documentId": 3, "title": "Rechnung", "processed_at": "2026-11-01T08:00:00Z"}},
	}, paiAuth)
	in := f.instance(map[string]string{"apiKey": "pai_abcdefgh_secret"})
	w, err := paperlessAI{}.Fetch(context.Background(), in)
	if err != nil || stats(w)["Heute"] != "4" || stats(w)["Status"] != "bereit" || len(w.Items) != 1 || w.Items[0].Title != "Rechnung" {
		t.Fatalf("%v %+v", err, w)
	}
	if _, err := (paperlessAI{}).Search(context.Background(), in, "x"); !errors.Is(err, ErrNoSearch) {
		t.Errorf("got %v", err)
	}
	// Even older: only /health.
	h := newFake(t, map[string]any{"GET /health": obj{"status": "healthy"}}, nil)
	w, err = paperlessAI{}.Fetch(context.Background(), h.instance(map[string]string{"apiKey": "k"}))
	if err != nil || stats(w)["Status"] != "bereit" {
		t.Fatalf("%v %+v", err, w)
	}
}

// Paperless-AI 4.5 with built-in archive used like Paperless-ngx: the
// Paperless-ngx tile finds the compatible API under /ngx/api/ by itself.
func TestPaperlessNgxOnPaperlessAI(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /ngx/api/statistics/": obj{"documents_total": 50, "documents_inbox": nil, "inbox_tag": nil, "tag_count": 3, "correspondent_count": 4, "document_type_count": 2, "storage_path_count": 0, "character_count": 0},
		"GET /ngx/api/tasks/":      []obj{},
		"GET /ngx/api/documents/":  obj{"count": 1, "results": []obj{{"id": 9, "title": "Kfz-Versicherung", "created": "2026-11-20", "added": "2026-11-20T10:00:00Z"}}},
	}, func(r *http.Request) bool { return r.Header.Get("Authorization") == "Token pai_abcdefgh_secret" })
	in := f.instance(map[string]string{"apiKey": "pai_abcdefgh_secret"})
	w, err := paperless{}.Fetch(context.Background(), in)
	if err != nil || stats(w)["Dokumente"] != "50" || len(w.Items) != 1 {
		t.Fatalf("%v %+v", err, w)
	}
	if w.Items[0].URL != "https://svc.example.com/documents/9" || w.Items[0].Image != "/api/tiles/7/image?p=%2Fngx%2Fapi%2Fdocuments%2F9%2Fthumb%2F" {
		t.Errorf("%q %q", w.Items[0].URL, w.Items[0].Image)
	}
	if _, _, err := (paperless{}).ImageRequest(context.Background(), in, "/ngx/api/documents/9/thumb/"); err != nil {
		t.Error(err)
	}
	items, err := paperless{}.Search(context.Background(), in, "kfz")
	if err != nil || len(items) != 1 || items[0].URL != "https://svc.example.com/documents/9" {
		t.Fatalf("%v %+v", err, items)
	}
	// The address entered as the apps use it (…/ngx).
	in.BaseURL += "/ngx"
	in.PublicURL += "/ngx"
	items, err = paperless{}.Search(context.Background(), in, "kfz")
	if err != nil || len(items) != 1 || items[0].URL != "https://svc.example.com/documents/9" {
		t.Fatalf("%v %+v", err, items)
	}
}

func TestPaperlessAIDiscovery(t *testing.T) {
	if in, ok := Match("clusterzx/paperless-ai:4.5.1"); !ok || in.Def().ID != "paperless-ai" {
		t.Error("image not recognised")
	}
	if in, ok := MatchName("paperless-ai"); !ok || in.Def().ID != "paperless-ai" {
		t.Error("name not recognised")
	}
	if in, ok := Match("ghcr.io/paperless-ngx/paperless-ngx"); !ok || in.Def().ID != "paperless" {
		t.Error("Paperless-ngx lost")
	}
}

// A refused key says why: another kind of key, an unknown one, or a proxy in front.
func TestPaperlessAIUnauthorized(t *testing.T) {
	pai := `{"error":"Authentication required"}`
	for _, c := range []struct{ key, body, want string }{
		{"0123456789abcdef0123456789abcdef01234567", pai, "Token von Paperless-ngx"},
		{"sk-proj-abc", pai, "KI-Anbieters"},
		{"pai_abcdefgh_" + strings.Repeat("x", 30), pai, "kennt diesen API-Key nicht"},
		{strings.Repeat("ab", 32), pai, "Global API key wurde vielleicht neu erzeugt"},
		{"irgendwas", pai, "Settings → API keys"},
		{"pai_abcdefgh_" + strings.Repeat("x", 30), "<html>401 Authorization Required</html>", "Proxy"},
	} {
		in := &Instance{Values: map[string]string{"apiKey": c.key}, Lang: "de"}
		if err := paiUnauthorized(in, c.body); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%q: %v, want %q", c.key, err, c.want)
		}
	}
}

// An address that sends Heimdeck on to another server: the key is not sent
// along there, and the message says where the address leads.
func TestPaperlessAIRedirect(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "" {
			t.Error("the key went to another server")
		}
		w.WriteHeader(401)
		io.WriteString(w, `{"error":"Authentication required"}`)
	}))
	defer target.Close()
	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(target.URL, "127.0.0.1", "localhost", 1)+r.URL.Path, http.StatusMovedPermanently)
	}))
	defer front.Close()
	in := &Instance{BaseURL: front.URL, Values: map[string]string{"apiKey": "pai_abcdefgh_" + strings.Repeat("x", 30)}, Client: NewClient(false), Lang: "de"}
	_, err := paperlessAI{}.Fetch(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "leitet auf http://localhost:") {
		t.Fatalf("got %v", err)
	}
}
