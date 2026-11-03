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

// Brave and Tavily need an API key, which is never sent back; OpenRouter
// searches by itself when asked through its web plugin.
func TestWebSearchProviders(t *testing.T) {
	var got map[string]any
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"Okay.\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer model.Close()
	brave := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Subscription-Token") != "brave-key" {
			w.WriteHeader(401)
			return
		}
		io.WriteString(w, `{"web":{"results":[{"title":"<strong>Heimdeck</strong> on GitHub","url":"https://github.com/MrLuigi938/heimdeck","description":"A <strong>dashboard</strong> for the home lab"}]}}`)
	}))
	defer brave.Close()
	tavily := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var q map[string]any
		json.NewDecoder(r.Body).Decode(&q)
		if r.Header.Get("Authorization") != "Bearer tvly-key" || q["query"] == nil {
			w.WriteHeader(401)
			return
		}
		io.WriteString(w, `{"results":[{"title":"Heimdeck","url":"https://blum.ing/heimdeck/","content":"Self-hosted start page"},{"title":"Two","url":"https://example.com","content":"x"}]}`)
	}))
	defer tavily.Close()
	oldBrave, oldTavily := braveAPI, tavilyAPI
	braveAPI, tavilyAPI = brave.URL, tavily.URL
	defer func() { braveAPI, tavilyAPI = oldBrave, oldTavily }()

	_, base := multiUserServer(t)
	luis := newTestClient(t, base)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "de"})
	chat := func() http.Header {
		req, _ := http.NewRequest("POST", base+"/api/ai/chat", strings.NewReader(`{"web":true,"messages":[{"role":"user","text":"Was ist Heimdeck?"}]}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := luis.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		io.ReadAll(resp.Body)
		resp.Body.Close()
		return resp.Header
	}
	ai := map[string]any{"enabled": true, "url": model.URL + "/v1", "model": "m"}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range ai {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}

	// Brave: a key is needed; the stored one stays secret and is kept.
	if status, _ := luis.call("PUT", "/api/ai", with(map[string]any{"webSearch": "brave"})); status != 400 {
		t.Fatalf("brave without a key: %d", status)
	}
	if status, res := luis.call("PUT", "/api/ai", with(map[string]any{"webSearch": "brave", "webKey": "brave-key"})); status != 200 {
		t.Fatalf("save brave: %d %v", status, res)
	}
	_, cfg := luis.call("GET", "/api/ai", nil)
	if cfg["webKeySet"] != true || fmt.Sprint(cfg["config"].(map[string]any)["webKey"]) != "<nil>" {
		t.Fatalf("key sent back or not kept: %v", cfg)
	}
	luis.call("PUT", "/api/ai", with(map[string]any{"webSearch": "brave"})) // empty field keeps the key
	if hdr := chat(); hdr.Get("X-Web-Results") != "1" {
		t.Fatalf("brave results: %v", hdr)
	}
	sys := fmt.Sprint(got["messages"].([]any)[0])
	if !strings.Contains(sys, "https://github.com/MrLuigi938/heimdeck") || strings.Contains(sys, "<strong>") {
		t.Fatalf("brave hits in the prompt: %s", sys)
	}
	if status, res := luis.call("POST", "/api/ai/websearch", with(map[string]any{"webSearch": "brave", "webKey": "wrong"})); status != 502 || !strings.Contains(fmt.Sprint(res["error"]), "API-Schlüssel") {
		t.Fatalf("wrong brave key: %d %v", status, res)
	}

	// Switching to Tavily does not take the Brave key along.
	if status, _ := luis.call("PUT", "/api/ai", with(map[string]any{"webSearch": "tavily"})); status != 400 {
		t.Fatalf("tavily took the brave key: %d", status)
	}
	luis.call("PUT", "/api/ai", with(map[string]any{"webSearch": "tavily", "webKey": "tvly-key"}))
	if status, res := luis.call("POST", "/api/ai/websearch", with(map[string]any{"webSearch": "tavily"})); status != 200 || res["results"] != float64(2) {
		t.Fatalf("tavily test: %d %v", status, res)
	}
	if hdr := chat(); hdr.Get("X-Web-Results") != "2" {
		t.Fatalf("tavily results: %v", hdr)
	}

	// OpenRouter only with an OpenRouter address; then the plugin goes along.
	if status, _ := luis.call("PUT", "/api/ai", with(map[string]any{"webSearch": "openrouter"})); status != 400 {
		t.Fatalf("openrouter web search with another server: %d", status)
	}
}

func TestOpenRouterPlugin(t *testing.T) {
	c := aiConfig{URL: "https://openrouter.ai/api/v1", Model: "openai/gpt-4o-mini", WebSearch: webOpenRouter}
	if !c.webReady() || c.checkWeb() != nil {
		t.Fatal("openrouter web search not ready")
	}
	p := openRouterWeb()["plugins"].([]any)[0].(map[string]any)
	if p["id"] != "web" {
		t.Fatalf("plugin %v", p)
	}
	old := aiConfig{SearxURL: "http://searxng:8080"}
	if old.webProvider() != webSearXNG || !old.webReady() {
		t.Fatal("settings from before the choice no longer use SearXNG")
	}
}
