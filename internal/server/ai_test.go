package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
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
