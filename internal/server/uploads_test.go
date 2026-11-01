package server

import (
	"encoding/base64"
	"io"
	"net/http"
	"testing"
)

// A tiny PNG (1×1 pixel).
const pngB64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="

func TestUploads(t *testing.T) {
	_, base := multiUserServer(t)
	luis := newTestClient(t, base)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "de"})

	code, up := luis.call("POST", "/api/uploads", map[string]string{"kind": "background", "name": "berge.png", "data": pngB64})
	if code != 200 || up["url"] == nil {
		t.Fatalf("upload: %d %v", code, up)
	}
	resp, err := luis.c.Get(base + up["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	png, _ := base64.StdEncoding.DecodeString(pngB64)
	if resp.StatusCode != 200 || resp.Header.Get("Content-Type") != "image/png" || string(body) != string(png) || resp.Header.Get("Content-Security-Policy") == "" {
		t.Fatalf("get: %d %s", resp.StatusCode, resp.Header)
	}

	// Not a picture, or a script disguised as one, is refused; SVG only as icon.
	svg := base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"></svg>`))
	for _, c := range []map[string]string{
		{"kind": "background", "data": base64.StdEncoding.EncodeToString([]byte("<html><script>alert(1)</script>"))},
		{"kind": "background", "data": svg},
		{"kind": "other", "data": pngB64},
	} {
		if code, out := luis.call("POST", "/api/uploads", c); code == 200 {
			t.Errorf("accepted %v: %v", c["kind"], out)
		}
	}
	if code, _ := luis.call("POST", "/api/uploads", map[string]string{"kind": "icon", "data": svg}); code != 200 {
		t.Errorf("svg icon refused: %d", code)
	}

	// Others can see the picture (shared dashboards), but not delete it.
	luis.call("POST", "/api/users", map[string]string{"username": "anna", "password": "anna-pw-123"})
	anna := newTestClient(t, base)
	anna.call("POST", "/api/login", map[string]string{"username": "anna", "password": "anna-pw-123"})
	if r, _ := anna.c.Get(base + up["url"].(string)); r.StatusCode != 200 {
		t.Fatalf("anna cannot see the picture: %d", r.StatusCode)
	}
	if code, _ := anna.call("DELETE", "/api/uploads/"+up["id"].(string), nil); code != 404 {
		t.Fatalf("anna deleted luis' picture: %d", code)
	}
	// Without a login nothing is served.
	if r, _ := http.Get(base + up["url"].(string)); r.StatusCode != 401 {
		t.Fatalf("served without login: %d", r.StatusCode)
	}
	if code, _ := luis.call("DELETE", "/api/uploads/"+up["id"].(string), nil); code != 200 {
		t.Fatalf("delete: %d", code)
	}
	_, list := luis.call("GET", "/api/uploads?kind=background", nil)
	if len(list["uploads"].([]any)) != 0 {
		t.Fatalf("still listed: %v", list)
	}
}
