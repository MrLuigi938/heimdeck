package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/store"
)

// Asking an AI from the search window (stage 1): the question goes to a
// model with the search hits the user is looking at as context. Any
// OpenAI-compatible server works – Ollama, LM Studio, LocalAI, vLLM, or a
// cloud service like OpenAI, OpenRouter or Mistral. The admin sets it up;
// the hits sent along are only what the user can see anyway.

const aiKey = "_ai" // global setting, sealed like other secrets

type aiConfig struct {
	Enabled    bool   `json:"enabled"`
	URL        string `json:"url"`   // base address, e.g. http://ollama:11434/v1
	Model      string `json:"model"` // e.g. llama3.1
	APIKey     string `json:"apiKey,omitempty"`
	Label      string `json:"label"`      // shown on the button, e.g. "Ollama"
	AdminsOnly bool   `json:"adminsOnly"` // only admins may ask (cloud costs)
	SearxURL   string `json:"searxUrl"`   // optional SearXNG for web results (the model itself never browses)
	// WebSearch picks the web search: searxng | brave | tavily | openrouter
	// (empty: SearXNG when an address is set, as before there was a choice).
	WebSearch string `json:"webSearch"`
	WebKey    string `json:"webKey,omitempty"` // API key for Brave or Tavily, never sent back
	// Dashboard lets people hand the AI what their tiles show (📋 in the chat).
	Dashboard bool `json:"dashboard"`
	// Tools: with 📋 the AI asks for what it needs (list, read, search, propose
	// an action) instead of getting the whole dashboard; models without tool
	// support fall back to the whole dashboard.
	Tools    bool `json:"tools"`
	Insecure bool `json:"insecure"`
}

func (s *Server) aiConfig() aiConfig {
	var c aiConfig
	settings, _ := s.store.Settings()
	if plain, err := s.store.OpenSecret(settings[aiKey]); err == nil && plain != nil {
		json.Unmarshal(plain, &c)
	}
	return c
}

func (c aiConfig) ready() bool { return c.Enabled && c.URL != "" && c.Model != "" }

// aiFor says whether a user may ask, and under which name.
func (s *Server) aiFor(u store.User) map[string]any {
	c := s.aiConfig()
	if !c.ready() || (c.AdminsOnly && u.Role != store.RoleAdmin) {
		return nil
	}
	label := c.Label
	if label == "" {
		label = c.Model
	}
	return map[string]any{"label": label, "model": c.Model, "local": !aiIsCloud(c.URL), "web": c.webReady(), "webVia": webName(c.webProvider()), "dashboard": c.Dashboard, "tools": c.Dashboard && c.Tools}
}

// sameHost: a stored key is only ever sent to the server it was entered for –
// otherwise another admin could read it by pointing the address at their own.
func sameHost(a, b string) bool {
	pa, err1 := url.Parse(strings.TrimSpace(a))
	pb, err2 := url.Parse(strings.TrimSpace(b))
	return err1 == nil && err2 == nil && pa.Host != "" && strings.EqualFold(pa.Host, pb.Host)
}

// aiIsCloud guesses whether the hits leave the house (for a hint in the window).
func aiIsCloud(u string) bool {
	p, err := url.Parse(u)
	if err != nil {
		return true
	}
	h := strings.ToLower(p.Hostname())
	return strings.Contains(h, "openai.com") || strings.Contains(h, "openrouter.ai") || strings.Contains(h, "mistral.ai") ||
		strings.Contains(h, "anthropic.com") || strings.Contains(h, "groq.com") || strings.Contains(h, "googleapis.com") ||
		strings.Contains(h, "together.xyz") || strings.Contains(h, "deepseek.com")
}

// aiClient has no overall timeout: an answer may take a while to stream.
func (c aiConfig) client() *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if c.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	tr.ResponseHeaderTimeout = 90 * time.Second // a local model may need to load first
	return &http.Client{Transport: tr}
}

func (c aiConfig) endpoint(path string) string { return strings.TrimRight(c.URL, "/") + path }

func (c aiConfig) request(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.endpoint(path), rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	return c.client().Do(req)
}

// ---- settings (admin) ----

func (s *Server) handleGetAI(w http.ResponseWriter, r *http.Request) {
	c := s.aiConfig()
	keySet, webKeySet := c.APIKey != "", c.WebKey != ""
	c.APIKey, c.WebKey = "", ""
	c.WebSearch = c.webProvider()
	writeJSON(w, map[string]any{"config": c, "keySet": keySet, "webKeySet": webKeySet})
}

func (s *Server) handleSaveAI(w http.ResponseWriter, r *http.Request) {
	var c aiConfig
	if err := readJSON(r, &c); err != nil {
		writeErr(w, 400, err)
		return
	}
	old := s.aiConfig()
	if c.APIKey == "" && sameHost(c.URL, old.URL) {
		c.APIKey = old.APIKey
	}
	c.URL, c.Model, c.Label = strings.TrimSpace(c.URL), strings.TrimSpace(c.Model), strings.TrimSpace(c.Label)
	c.SearxURL, c.WebKey = strings.TrimSpace(c.SearxURL), strings.TrimSpace(c.WebKey)
	if c.WebSearch == "none" {
		c.WebSearch, c.SearxURL = "", ""
	}
	// An empty key field keeps the stored key – of the same service only.
	if c.WebKey == "" && c.webProvider() == old.webProvider() {
		c.WebKey = old.WebKey
	}
	if err := c.checkWeb(); err != nil {
		writeErr(w, 400, errors.New(webErrText(r, err)))
		return
	}
	if c.URL != "" {
		if p, err := url.Parse(c.URL); err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
			writeErr(w, 400, errors.New("KI: bitte eine vollständige Adresse mit http:// oder https:// angeben"))
			return
		}
	}
	if c.Enabled && (c.URL == "" || c.Model == "") {
		writeErr(w, 400, errors.New("KI: Adresse und Modell werden gebraucht"))
		return
	}
	plain, _ := json.Marshal(c)
	sealed, err := s.store.SealSecret(plain)
	if err == nil {
		err = s.store.SaveSettings(map[string]string{aiKey: sealed})
	}
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	slog.Info("KI-Einstellungen gespeichert", "aktiv", c.Enabled, "adresse", c.URL, "modell", c.Model, "von", s.username(r))
	s.kickBots() // the AI may have been switched on or off for the Telegram bots
	writeJSON(w, map[string]bool{"ok": true, "keySet": c.APIKey != ""})
}

// handleAIModels asks the server which models it offers (to pick one, and to
// check the connection). Unsaved values from the form are used.
func (s *Server) handleAIModels(w http.ResponseWriter, r *http.Request) {
	var c aiConfig
	if err := readJSON(r, &c); err != nil {
		writeErr(w, 400, err)
		return
	}
	if old := s.aiConfig(); c.APIKey == "" && sameHost(c.URL, old.URL) {
		c.APIKey = old.APIKey
	}
	if p, err := url.Parse(strings.TrimSpace(c.URL)); err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
		writeErr(w, 400, errors.New("KI: bitte eine vollständige Adresse mit http:// oder https:// angeben"))
		return
	}
	ctx, cancel := timeout(r, 15*time.Second)
	defer cancel()
	resp, err := c.request(ctx, "GET", "/models", nil)
	if err != nil {
		writeErr(w, 502, fmt.Errorf("KI nicht erreichbar: %v", err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		writeErr(w, 502, fmt.Errorf("KI antwortet mit %s – stimmen Adresse (meist mit /v1 am Ende) und Schlüssel?", resp.Status))
		return
	}
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&list)
	models := []string{}
	for _, m := range list.Data {
		models = append(models, m.ID)
	}
	writeJSON(w, map[string]any{"models": models})
}

// ---- asking ----

type aiHit struct {
	Service  string `json:"service"`
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	Badge    string `json:"badge"`
}

func clip(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// handleAsk streams the model's answer as plain text, piece by piece.
func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.User(uid(r))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	c := s.aiConfig()
	if s.aiFor(u) == nil {
		writeErr(w, 403, errors.New("Die KI ist nicht eingerichtet"))
		return
	}
	var body struct {
		Question string  `json:"question"`
		Hits     []aiHit `json:"hits"`
		Date     string  `json:"date"` // the user's local date and time, so "this week" means something
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Question) == "" {
		writeErr(w, 400, errors.New("Frage fehlt"))
		return
	}
	lang := langOf(r)
	var ctxLines []string
	for i, h := range body.Hits {
		if i == 60 {
			break
		}
		line := "- [" + clip(h.Service, 40) + "] " + clip(h.Title, 160)
		if h.Subtitle != "" {
			line += " — " + clip(h.Subtitle, 200)
		}
		if h.Badge != "" {
			line += " (" + clip(h.Badge, 40) + ")"
		}
		ctxLines = append(ctxLines, line)
	}
	system := "You are the assistant inside Heimdeck, a self-hosted home dashboard. Answer briefly and concretely, in " +
		map[string]string{"de": "German", "en": "English"}[lang] + ". " +
		"Below are the search results the user currently sees from their own services (media servers, downloads, calendars, documents, smart home …). " +
		"Use them as your main source and name the service in square brackets when you rely on one, e.g. [Sonarr]. " +
		"If they do not answer the question, say so briefly and answer from general knowledge only if that makes sense. " +
		"Never invent entries that are not in the results."
	if body.Date != "" {
		system += " The user's current local date and time: " + clip(body.Date, 60) + "."
	}
	user := "Question: " + clip(body.Question, 1000) + "\n\nSearch results:\n"
	if len(ctxLines) == 0 {
		user += "(none)"
	} else {
		user += strings.Join(ctxLines, "\n")
	}

	s.streamChat(w, r, c, u, []any{map[string]string{"role": "system", "content": system}, map[string]string{"role": "user", "content": user}}, len(ctxLines), nil)
}

// streamChat sends the messages to the model and passes its answer on as
// plain text, piece by piece.
// streamChat sends the conversation and streams the answer back; extra goes
// into the request as well (e.g. OpenRouter's web search plugin).
func (s *Server) streamChat(w http.ResponseWriter, r *http.Request, c aiConfig, u store.User, messages []any, hits int, extra map[string]any) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	body := map[string]any{"model": c.Model, "stream": true, "temperature": 0.3, "messages": messages}
	for k, v := range extra {
		body[k] = v
	}
	resp, err := c.request(ctx, "POST", "/chat/completions", body)
	if err != nil {
		slog.Warn("KI nicht erreichbar", "fehler", err)
		writeErr(w, 502, errors.New("Die KI ist gerade nicht erreichbar"))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 400))
		slog.Warn("KI lehnt ab", "status", resp.Status, "antwort", string(b))
		writeErr(w, 502, fmt.Errorf("Die KI antwortet mit %s – Details im Protokoll", resp.Status))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no") // nginx: pass it on piece by piece
	rc := http.NewResponseController(w)
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	start := time.Now()
	n := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		data, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil || len(chunk.Choices) == 0 {
			continue
		}
		if piece := chunk.Choices[0].Delta.Content; piece != "" {
			piece = modelText(piece)
			io.WriteString(w, piece)
			n += len(piece)
			rc.Flush()
		}
	}
	slog.Debug("KI-Antwort", "modell", c.Model, "treffer", hits, "zeichen", n, "dauer", time.Since(start).Round(time.Millisecond), "benutzer", u.Username)
}
