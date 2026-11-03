package server

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// The question and the visible hits go to an OpenAI-compatible server; its
// streamed answer comes back piece by piece as plain text.
func TestAskAI(t *testing.T) {
	var got map[string]any
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Write([]byte(`{"data":[{"id":"llama3.1"},{"id":"qwen2.5"}]}`))
		case "/v1/chat/completions":
			json.NewDecoder(r.Body).Decode(&got)
			w.Header().Set("Content-Type", "text/event-stream")
			for _, p := range []string{"Diese Woche ", "kommt Severance ", "[Sonarr]."} {
				b, _ := json.Marshal(map[string]any{"choices": []map[string]any{{"delta": map[string]string{"content": p}}}})
				fmt.Fprintf(w, "data: %s\n\n", b)
				w.(http.Flusher).Flush()
			}
			io.WriteString(w, "data: [DONE]\n\n")
		}
	}))
	defer model.Close()

	_, base := multiUserServer(t)
	luis := newTestClient(t, base)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "de"})
	if _, out := luis.call("POST", "/api/ai/models", map[string]any{"url": model.URL + "/v1"}); fmt.Sprint(out["models"]) != "[llama3.1 qwen2.5]" {
		t.Fatalf("models: %v", out)
	}
	if code, out := luis.call("PUT", "/api/ai", map[string]any{"enabled": true, "url": model.URL + "/v1", "model": "llama3.1", "label": "Ollama", "adminsOnly": true}); code != 200 {
		t.Fatalf("save: %d %v", code, out)
	}
	_, dash := luis.call("GET", "/api/dashboard", nil)
	if dash["ai"] == nil {
		t.Fatal("no ai for the admin")
	}

	body := `{"question":"Was kommt diese Woche?","hits":[{"service":"Sonarr","title":"Severance · S02E09","subtitle":"morgen 21:00"}],"date":"Sonntag, 1. November 2026, 18:00"}`
	req, _ := http.NewRequest("POST", base+"/api/ai/ask", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp, err := luis.c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	answer, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(answer) != "Diese Woche kommt Severance [Sonarr]." {
		t.Fatalf("answer %q (%d)", answer, resp.StatusCode)
	}
	msgs := fmt.Sprint(got["messages"])
	if got["model"] != "llama3.1" || !strings.Contains(msgs, "[Sonarr] Severance · S02E09 — morgen 21:00") || !strings.Contains(msgs, "German") {
		t.Fatalf("request %v", got)
	}

	// Admins only: a normal user cannot ask.
	luis.call("POST", "/api/users", map[string]string{"username": "anna", "password": "anna-pw-123"})
	anna := newTestClient(t, base)
	anna.call("POST", "/api/login", map[string]string{"username": "anna", "password": "anna-pw-123"})
	if code, _ := anna.call("POST", "/api/ai/ask", map[string]any{"question": "x"}); code != 403 {
		t.Fatalf("anna asked: %d", code)
	}
	_, d2 := anna.call("GET", "/api/dashboard", nil)
	if d2["ai"] != nil {
		t.Fatal("ai offered to anna")
	}
}

// The chat sends the whole conversation, pictures as image parts and – when
// asked – web results from SearXNG; Word documents are turned into text.
func TestAIChat(t *testing.T) {
	var got map[string]any
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Okay.\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer model.Close()
	searx := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("format") != "json" {
			w.WriteHeader(403)
			return
		}
		io.WriteString(w, `{"results":[{"title":"Jellyfin 10.11","url":"https://jellyfin.org/posts/10.11","content":"Release notes"}]}`)
	}))
	defer searx.Close()

	_, base := multiUserServer(t)
	luis := newTestClient(t, base)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "de"})
	luis.call("PUT", "/api/ai", map[string]any{"enabled": true, "url": model.URL + "/v1", "model": "llava", "searxUrl": searx.URL})
	_, dash := luis.call("GET", "/api/dashboard", nil)
	if dash["ai"].(map[string]any)["web"] != true {
		t.Fatalf("web search not offered: %v", dash["ai"])
	}
	ask := func(body string) (string, http.Header) {
		req, _ := http.NewRequest("POST", base+"/api/ai/chat", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		resp, err := luis.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(b), resp.Header
	}
	answer, hdr := ask(`{"web":true,"messages":[{"role":"user","text":"Hallo"},{"role":"assistant","text":"Hi!"},{"role":"user","text":"Was ist neu in Jellyfin?","images":["data:image/png;base64,AAAA"]}]}`)
	if answer != "Okay." || hdr.Get("X-Web-Results") != "1" {
		t.Fatalf("answer %q web %q", answer, hdr.Get("X-Web-Results"))
	}
	msgs := got["messages"].([]any)
	sys := fmt.Sprint(msgs[0])
	lastMsg := fmt.Sprint(msgs[len(msgs)-1])
	if len(msgs) != 4 || !strings.Contains(sys, "https://jellyfin.org/posts/10.11") || !strings.Contains(lastMsg, "image_url") {
		t.Fatalf("messages %v", msgs)
	}
	// A public instance that blocks Heimdeck: the chat says why, and the
	// model is told the search failed (not that it has no internet).
	blocked := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(429)
	}))
	defer blocked.Close()
	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "<!doctype html><title>Security check</title>")
	}))
	defer page.Close()
	for _, c := range []struct{ url, want string }{{blocked.URL, "automatische Anfragen"}, {page.URL, "Webseite statt JSON"}} {
		luis.call("PUT", "/api/ai", map[string]any{"enabled": true, "url": model.URL + "/v1", "model": "llava", "searxUrl": c.url})
		_, hdr := ask(`{"web":true,"messages":[{"role":"user","text":"Suche online nach Heimdeck"}]}`)
		reason, _ := url.PathUnescape(hdr.Get("X-Web-Error"))
		if !strings.Contains(reason, c.want) || !strings.Contains(fmt.Sprint(got["messages"]), "web search the user switched on failed") {
			t.Fatalf("%s: reason %q", c.url, reason)
		}
		status, res := luis.call("POST", "/api/ai/searx", map[string]any{"searxUrl": c.url})
		if status != 502 || !strings.Contains(fmt.Sprint(res["error"]), c.want) {
			t.Fatalf("test button: %d %v", status, res)
		}
	}
	if status, res := luis.call("POST", "/api/ai/searx", map[string]any{"searxUrl": searx.URL}); status != 200 || res["results"] != float64(1) {
		t.Fatalf("test button with a working SearXNG: %d %v", status, res)
	}
	luis.call("PUT", "/api/ai", map[string]any{"enabled": true, "url": model.URL + "/v1", "model": "llava", "searxUrl": searx.URL})

	// Without the switch nothing goes to the web.
	ask(`{"messages":[{"role":"user","text":"Was ist neu?"}]}`)
	if strings.Contains(fmt.Sprint(got["messages"]), "jellyfin.org") {
		t.Fatal("web results without asking")
	}

	// A minimal Word document.
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, _ := zw.Create("word/document.xml")
	f.Write([]byte(`<w:document xmlns:w="w"><w:body><w:p><w:r><w:t>Hallo</w:t></w:r></w:p><w:p><w:r><w:t>Welt</w:t></w:r></w:p></w:body></w:document>`))
	zw.Close()
	_, out := luis.call("POST", "/api/ai/extract", map[string]string{"name": "brief.docx", "data": base64.StdEncoding.EncodeToString(buf.Bytes())})
	if out["text"] != "Hallo\nWelt" {
		t.Fatalf("docx: %v", out)
	}
}
