package server

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// With tools the model asks for a tile, gets its live data and the keys of
// its buttons, and proposes one – which only shows up as a button in the
// chat; nothing runs on the service.
func TestAIChatTools(t *testing.T) {
	var mu sync.Mutex
	var rounds []map[string]any
	restarted := 0
	nas := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			restarted++
		}
		io.WriteString(w, `{"free": "1.8 TB"}`)
	}))
	defer nas.Close()
	sse := func(w http.ResponseWriter, chunks ...string) {
		for _, c := range chunks {
			io.WriteString(w, "data: "+c+"\n\n")
		}
		io.WriteString(w, "data: [DONE]\n\n")
	}
	refuse := false
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var got map[string]any
		json.NewDecoder(r.Body).Decode(&got)
		mu.Lock()
		rounds = append(rounds, got)
		n := len(rounds)
		mu.Unlock()
		if refuse && got["tools"] != nil {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":{"message":"this model does not support tools"}}`)
			return
		}
		msgs := got["messages"].([]any)
		last := msgs[len(msgs)-1].(map[string]any)
		switch {
		case refuse:
			sse(w, `{"choices":[{"delta":{"content":"Without tools."}}]}`)
		case last["role"] == "user":
			// The call arrives in pieces, as with OpenAI.
			sse(w, `{"choices":[{"delta":{"content":"Moment … "}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"read_tile","arguments":"{\"na"}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"me\":\"NAS\"}"}}]}}]}`,
				`{"choices":[{"delta":{"tool_calls":[{"index":1,"id":"c2","type":"function","function":{"name":"list_tiles","arguments":"{}"}}]}}]}`)
		case n == 2:
			sse(w, `{"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c3","type":"function","function":{"name":"propose_action","arguments":"{\"action\":\"a1\",\"reason\":\"you asked\"}"}}]}}]}`)
		default:
			sse(w, `{"choices":[{"delta":{"content":"Click the button to restart it."}}]}`)
		}
	}))
	defer model.Close()

	_, base := multiUserServer(t)
	luis := newTestClient(t, base)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "en"})
	_, g := luis.call("POST", "/api/groups", map[string]any{"name": "Server"})
	luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "NAS", "url": nas.URL, "integration": "customapi", "showWidget": true,
		"config": map[string]string{"stats": "Free = free", "buttons": "Restart = POST /restart"}})
	_, hidden := luis.call("POST", "/api/groups", map[string]any{"name": "Vault"})
	luis.call("PUT", fmt.Sprintf("/api/groups/%v", hidden["id"]), map[string]any{"name": "Vault", "visibility": "hidden"})
	luis.call("POST", "/api/tiles", map[string]any{"groupId": hidden["id"], "name": "Secret wiki", "url": "http://wiki.local", "integration": "link"})
	luis.call("PUT", "/api/ai", map[string]any{"enabled": true, "url": model.URL + "/v1", "model": "m", "dashboard": true, "tools": true})

	chat := func() (string, http.Header) {
		req, _ := http.NewRequest("POST", base+"/api/ai/chat", strings.NewReader(`{"dashboard":true,"messages":[{"role":"user","text":"Restart the NAS"}]}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := luis.c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(b), resp.Header
	}
	out, hdr := chat()
	if hdr.Get("X-AI-Tools") != "1" || len(rounds) != 3 {
		t.Fatalf("rounds %d, header %q, out %q", len(rounds), hdr.Get("X-AI-Tools"), out)
	}
	// The tool results went back to the model: the tile's data and its button key.
	second := fmt.Sprint(rounds[1]["messages"])
	if !strings.Contains(second, "Free 1.8 TB") || !strings.Contains(second, "[a1] Restart") || !strings.Contains(second, "NAS") {
		t.Fatalf("tool results: %s", second)
	}
	if strings.Contains(second, "Secret wiki") || strings.Contains(second, nas.URL) {
		t.Fatalf("hidden tile or address went to the model: %s", second)
	}
	// The chat gets the text and the events; the action itself did not run.
	events := regexp.MustCompile("\x1e([^\x1f]*)\x1f").FindAllStringSubmatch(out, -1)
	text := regexp.MustCompile("\x1e[^\x1f]*\x1f").ReplaceAllString(out, "")
	if text != "Moment … Click the button to restart it." || len(events) != 3 {
		t.Fatalf("text %q, events %v", text, events)
	}
	var prop struct {
		Propose proposal `json:"propose"`
	}
	json.Unmarshal([]byte(events[2][1]), &prop)
	if prop.Propose.Tile != "NAS" || prop.Propose.Action == "" || prop.Propose.Label != "Restart" {
		t.Fatalf("proposal %+v", prop.Propose)
	}
	if restarted != 0 {
		t.Fatal("the model ran the action itself")
	}
	// The button in the chat uses the normal action endpoint.
	if code, res := luis.call("POST", fmt.Sprintf("/api/tiles/%d/action", prop.Propose.TileID), map[string]any{"action": prop.Propose.Action, "params": prop.Propose.Params}); code != 200 || restarted != 1 {
		t.Fatalf("clicking the button: %d %v (restarted %d)", code, res, restarted)
	}

	// A model without tools: the whole dashboard goes along instead.
	refuse = true
	rounds = nil
	out, hdr = chat()
	sys := fmt.Sprint(rounds[len(rounds)-1]["messages"].([]any)[0])
	if out != "Without tools." || hdr.Get("X-AI-Tools") != "fallback" || !strings.Contains(sys, "Free 1.8 TB") {
		t.Fatalf("fallback: %q %q %s", out, hdr.Get("X-AI-Tools"), sys)
	}
}
