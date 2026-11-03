package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Web search for the AI chat. The model itself never browses: Heimdeck
// searches (SearXNG, Brave Search API, Tavily) and hands it the top results –
// or, with OpenRouter, asks OpenRouter to search along with the answer.

const (
	webSearXNG     = "searxng"
	webBrave       = "brave"
	webTavily      = "tavily"
	webOpenRouter  = "openrouter"
	webMaxResults  = 6
	webSnippetSize = 400
)

// The search APIs; variables so tests can point them elsewhere.
var (
	braveAPI  = "https://api.search.brave.com/res/v1/web/search"
	tavilyAPI = "https://api.tavily.com/search"
)

// webProvider is the configured search, also for settings saved before
// there was a choice (only a SearXNG address).
func (c aiConfig) webProvider() string {
	if c.WebSearch != "" {
		return c.WebSearch
	}
	if c.SearxURL != "" {
		return webSearXNG
	}
	return ""
}

// webReady says whether the chat can offer 🌐.
func (c aiConfig) webReady() bool {
	switch c.webProvider() {
	case webSearXNG:
		return c.SearxURL != ""
	case webBrave, webTavily:
		return c.WebKey != ""
	case webOpenRouter:
		return isOpenRouter(c.URL)
	}
	return false
}

func isOpenRouter(u string) bool {
	p, err := url.Parse(u)
	return err == nil && strings.HasSuffix(strings.ToLower(p.Hostname()), "openrouter.ai")
}

// webName is the provider as shown to the user.
func webName(p string) string {
	return map[string]string{webSearXNG: "SearXNG", webBrave: "Brave", webTavily: "Tavily", webOpenRouter: "OpenRouter"}[p]
}

type webHit struct{ Title, URL, Text string }

// webError keeps the German text and its values apart, so it can be
// translated with the service's name filled in.
type webError struct {
	format string
	args   []any
}

func (e *webError) Error() string { return fmt.Sprintf(e.format, e.args...) }

func webErrorf(format string, args ...any) error { return &webError{format, args} }

// webErrText is an error of the web search in the user's language.
func webErrText(r *http.Request, err error) string {
	var we *webError
	if errors.As(err, &we) {
		return tr(r, we.format, we.args...)
	}
	return tr(r, err.Error())
}

// webResults searches and returns the top hits as numbered lines.
func webResults(ctx context.Context, c aiConfig, q string) ([]string, error) {
	var hits []webHit
	var err error
	switch c.webProvider() {
	case webSearXNG:
		hits, err = searxSearch(ctx, c, q)
	case webBrave:
		hits, err = braveSearch(ctx, c, q)
	case webTavily:
		hits, err = tavilySearch(ctx, c, q)
	default:
		return nil, errors.New("Keine Websuche eingerichtet")
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for i, h := range hits {
		if i == webMaxResults {
			break
		}
		out = append(out, "["+strconv.Itoa(i+1)+"] "+clip(h.Title, 150)+" — "+clip(h.URL, 300)+"\n    "+clip(h.Text, webSnippetSize))
	}
	return out, nil
}

// webDo sends a search request and returns the JSON body; name is the
// service in the error texts.
func webDo(c aiConfig, req *http.Request, name string) ([]byte, error) {
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, webErrorf("%s ist nicht erreichbar", name)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	switch {
	case resp.StatusCode == 401 || resp.StatusCode == 403:
		return nil, webErrorf("%s lehnt den API-Schlüssel ab – stimmt er?", name)
	case resp.StatusCode == 429 || resp.StatusCode == 432 || resp.StatusCode == 433:
		return nil, webErrorf("%s: Kontingent aufgebraucht oder zu viele Anfragen – später noch einmal versuchen", name)
	case resp.StatusCode != 200:
		return nil, webErrorf("%s antwortet mit %s", name, resp.Status)
	}
	return data, nil
}

func searxSearch(ctx context.Context, c aiConfig, q string) ([]webHit, error) {
	u := strings.TrimRight(c.SearxURL, "/") + "/search?" + url.Values{"q": {q}, "format": {"json"}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.client().Do(req)
	if err != nil {
		return nil, errors.New("SearXNG ist nicht erreichbar – stimmt die Adresse?")
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == 429 || resp.StatusCode == 403:
		// Public instances block automated requests (their "limiter").
		return nil, errors.New("Diese SearXNG-Instanz lehnt automatische Anfragen ab – öffentliche Instanzen tun das fast immer. Eine eigene SearXNG nehmen (z. B. als Docker-Container)")
	case resp.StatusCode != 200:
		return nil, errors.New("SearXNG antwortet mit " + resp.Status + " – ist „json“ unter search.formats erlaubt?")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(bytes.TrimSpace(data), []byte("{")) {
		return nil, errors.New("SearXNG liefert eine Webseite statt JSON (Bot-Schutz, oder „json“ ist nicht erlaubt) – bei einer eigenen SearXNG unter search.formats „json“ erlauben")
	}
	var res struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}
	var out []webHit
	for _, x := range res.Results {
		out = append(out, webHit{x.Title, x.URL, x.Content})
	}
	return out, nil
}

// braveSearch uses the Brave Search API (api-dashboard.search.brave.com).
func braveSearch(ctx context.Context, c aiConfig, q string) ([]webHit, error) {
	u := braveAPI + "?" + url.Values{"q": {q}, "count": {strconv.Itoa(webMaxResults)}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", c.WebKey)
	data, err := webDo(c, req, "Brave")
	if err != nil {
		return nil, err
	}
	var res struct {
		Web struct {
			Results []struct {
				Title       string   `json:"title"`
				URL         string   `json:"url"`
				Description string   `json:"description"`
				Extra       []string `json:"extra_snippets"`
			} `json:"results"`
		} `json:"web"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}
	var out []webHit
	for _, x := range res.Web.Results {
		text := stripTags(x.Description)
		if len(x.Extra) > 0 {
			text += " " + stripTags(x.Extra[0])
		}
		out = append(out, webHit{stripTags(x.Title), x.URL, text})
	}
	return out, nil
}

// tavilySearch uses Tavily (tavily.com), a search API made for AI assistants.
func tavilySearch(ctx context.Context, c aiConfig, q string) ([]webHit, error) {
	body, _ := json.Marshal(map[string]any{"query": q, "max_results": webMaxResults, "search_depth": "basic"})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tavilyAPI, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.WebKey)
	data, err := webDo(c, req, "Tavily")
	if err != nil {
		return nil, err
	}
	var res struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
		} `json:"results"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return nil, err
	}
	var out []webHit
	for _, x := range res.Results {
		out = append(out, webHit{x.Title, x.URL, x.Content})
	}
	return out, nil
}

// stripTags removes the <strong> marks Brave puts around matches.
func stripTags(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '<':
			in = true
		case r == '>' && in:
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// openRouterWeb is the plugin that lets OpenRouter search along with the answer.
func openRouterWeb() map[string]any {
	return map[string]any{"plugins": []any{map[string]any{"id": "web", "max_results": 5}}}
}

// handleWebTest tries the web search from the AI settings form (admins);
// an empty key means the stored one.
func (s *Server) handleWebTest(w http.ResponseWriter, r *http.Request) {
	var c aiConfig
	if err := readJSON(r, &c); err != nil {
		writeErr(w, 400, err)
		return
	}
	old := s.aiConfig()
	if c.WebKey == "" && c.webProvider() == old.webProvider() {
		c.WebKey = old.WebKey
	}
	if err := c.checkWeb(); err != nil {
		writeErr(w, 400, errors.New(webErrText(r, err)))
		return
	}
	if c.webProvider() == webOpenRouter {
		// Searching costs money there; it happens with each question.
		writeJSON(w, map[string]any{"results": 0, "provider": webOpenRouter})
		return
	}
	ctx, cancel := timeout(r, 12*time.Second)
	defer cancel()
	results, err := webResults(ctx, c, "Heimdeck dashboard")
	if err != nil {
		writeErr(w, 502, errors.New(webErrText(r, err)))
		return
	}
	writeJSON(w, map[string]any{"results": len(results), "provider": c.webProvider()})
}

// checkWeb validates the web search settings.
func (c aiConfig) checkWeb() error {
	switch c.webProvider() {
	case "":
		return nil
	case webSearXNG:
		if p, err := url.Parse(c.SearxURL); err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
			return errors.New("SearXNG: bitte eine vollständige Adresse mit http:// oder https:// angeben")
		}
	case webBrave, webTavily:
		if c.WebKey == "" {
			return webErrorf("%s: bitte den API-Schlüssel eintragen", webName(c.webProvider()))
		}
	case webOpenRouter:
		if !isOpenRouter(c.URL) {
			return errors.New("Die Websuche über OpenRouter geht nur, wenn die KI-Adresse OpenRouter ist")
		}
	default:
		return errors.New("Unbekannte Websuche")
	}
	return nil
}
