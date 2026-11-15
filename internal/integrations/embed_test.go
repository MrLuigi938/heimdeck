package integrations

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// The embedded page: its options, and whether the page allows being embedded.
func TestEmbed(t *testing.T) {
	headers := map[string]map[string]string{
		"/open":    {},
		"/deny":    {"X-Frame-Options": "DENY"},
		"/same":    {"X-Frame-Options": "SAMEORIGIN"},
		"/none":    {"Content-Security-Policy": "default-src 'self'; frame-ancestors 'none'"},
		"/star":    {"Content-Security-Policy": "frame-ancestors *", "X-Frame-Options": "DENY"}, // CSP wins
		"/listed":  {"Content-Security-Policy": "frame-ancestors 'self' https://dash.example"},
		"/missing": nil,
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, ok := headers[r.URL.Path]
		if !ok || h == nil {
			w.WriteHeader(404)
			return
		}
		for k, v := range h {
			w.Header().Set(k, v)
		}
		w.Write([]byte("<html></html>"))
	}))
	defer srv.Close()
	in := func(path string, values map[string]string) *Instance {
		return &Instance{BaseURL: srv.URL + path, PublicURL: srv.URL + path, Values: values, Client: srv.Client(), Lang: "de"}
	}

	for path, want := range map[string]string{"/open": "", "/deny": "X-Frame-Options", "/same": "X-Frame-Options", "/none": "frame-ancestors", "/star": ""} {
		w, err := embed{}.Fetch(context.Background(), in(path, nil))
		if err != nil || w.Embed == nil {
			t.Fatalf("%s: %v", path, err)
		}
		if (want == "") != (w.Embed.Blocked == "") || !strings.Contains(w.Embed.Blocked, want) {
			t.Errorf("%s: blocked %q, want %q", path, w.Embed.Blocked, want)
		}
		if err := (embed{}).Test(context.Background(), in(path, nil)); (err == nil) != (want == "") {
			t.Errorf("%s: test %v", path, err)
		}
	}
	w, _ := embed{}.Fetch(context.Background(), in("/listed", nil))
	if w.Embed.Blocked != "" || !strings.Contains(w.Embed.Hint, "dash.example") {
		t.Errorf("listed: %+v", w.Embed)
	}
	// A page that is down still gives a tile (the browser shows the rest); the test says it.
	if w, err := (embed{}).Fetch(context.Background(), in("/missing", nil)); err != nil || w.Embed.Blocked != "" {
		t.Errorf("missing: %v %+v", err, w)
	}
	if err := (embed{}).Test(context.Background(), in("/missing", nil)); err == nil {
		t.Error("a missing page passed the test")
	}

	v := embedView(in("/open", map[string]string{"height": "groß (480 px)", "zoom": "75 %", "reload": "alle 5 Minuten", "interactive": "nein, nur ansehen"}))
	if v.Height != 480 || v.Zoom != 75 || v.Reload != 300 || v.Interactive {
		t.Errorf("options: %+v", v)
	}
	v = embedView(in("/open", map[string]string{}))
	if v.Height != 320 || v.Zoom != 100 || v.Reload != 0 || !v.Interactive {
		t.Errorf("defaults: %+v", v)
	}
}

// A camera can be shown blurred until uncovered.
func TestCameraBlur(t *testing.T) {
	in := &Instance{Values: map[string]string{"snapshot": "http://cam/x.jpg", "blur": "true", "blurAgain": "nach 5 Minuten"}, Lang: "de", TileID: 987654}
	w, err := cameraTile{}.Fetch(context.Background(), in)
	if err != nil || !w.Camera.Blur || w.Camera.BlurFor != 300 {
		t.Fatalf("%v %+v", err, w)
	}
	in.Values["blurAgain"] = "nur von Hand"
	w, _ = cameraTile{}.Fetch(context.Background(), in)
	if !w.Camera.Blur || w.Camera.BlurFor != 0 {
		t.Fatalf("by hand: %+v", w.Camera)
	}
	delete(in.Values, "blur")
	w, _ = cameraTile{}.Fetch(context.Background(), in)
	if w.Camera.Blur {
		t.Fatal("blurred without the setting")
	}
}
