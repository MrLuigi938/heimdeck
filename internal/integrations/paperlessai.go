package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---- Paperless-AI ----
//
// Paperless-AI (github.com/clusterzx/paperless-ai) analyses documents with an
// AI model: title, tags, correspondent, type. Since 4.0 it is one Node.js
// service with a REST API (x-api-key, the global key or a personal key
// pai_…); since 4.5 it can keep the documents itself ("built-in archive")
// instead of working on a Paperless-ngx, and then also offers a
// Paperless-ngx compatible API under /ngx/api/ for apps. Version 3.x only
// knows /health and /api/processing-status.

type paperlessAI struct{}

func init() { register(paperlessAI{}) }

func (paperlessAI) Def() Definition {
	return Definition{
		ID: "paperless-ai", Name: "Paperless-AI", Icon: "paperless-ai", Category: "Dokumente",
		Description: "KI-Verarbeitung (Warteschlange, verarbeitet, fehlgeschlagen, Tokens), Archiv mit Vorschaubildern, KI-Index, Scan starten und pausieren, Dokumentsuche – auch nach Bedeutung",
		Fields: []Field{
			{Key: "apiKey", Label: "API-Key", Type: "password", Required: true, Secret: true,
				Help: "In Paperless-AI unter Settings → API keys einen Schlüssel anlegen (beginnt mit pai_; Vorlage „Read only“ genügt, für die Suche nach Bedeutung zusätzlich „chat“, für Scan, Pause und Wiederholen ein Admin-Konto) – oder den Global API key aus Settings → Integrations. Nicht den Token von Paperless-ngx. Bei Version 3 der API_KEY aus den Einstellungen."},
			{Key: "list", Label: "Liste", Type: "select", Options: []string{"zuletzt hinzugefügt", "Probleme", "keine"}, Display: true,
				Help: "Was die Kachel unter den Zahlen zeigt; laufende Analysen stehen immer oben"},
			{Key: "aiSearch", Label: "Suche nach Bedeutung", Type: "checkbox",
				Help: "Die Heimdeck-Suche fragt zusätzlich den KI-Index (RAG) von Paperless-AI und findet Dokumente auch ohne das genaue Wort – braucht das Recht „Fragen & Chat“ und ist etwas langsamer"},
		},
		HasWidget: true, HasSearch: true, Images: []string{"paperless-ai"}, Port: 3000,
	}
}

func (paperlessAI) headers(in *Instance) map[string]string {
	return map[string]string{"x-api-key": in.V("apiKey")}
}

// call talks to Paperless-AI's own API and turns its refusals into clear texts.
func (p paperlessAI) call(ctx context.Context, in *Instance, method, path string, q url.Values, body, out any) error {
	err := in.do(ctx, request{method: method, path: path, query: q, headers: p.headers(in), body: body}, out)
	var he *HTTPError
	if errors.As(err, &he) {
		switch {
		case he.Status == 401:
			if to := redirectsElsewhere(ctx, in, path); to != "" {
				return in.Errorf("Die Adresse leitet auf %s weiter – dorthin schickt Heimdeck den API-Key aus Sicherheitsgründen nicht mit. Bitte diese Adresse direkt eintragen (oder Paperless-AI selbst, z. B. http://192.168.1.20:3000)", to)
			}
			return paiUnauthorized(in, he.Body)
		case he.Status == 403 && strings.Contains(he.Body, "admin_required"):
			return in.Errorf("Dafür braucht der API-Key das Recht „Administration“")
		case he.Status == 403 && strings.Contains(he.Body, "scope_missing"):
			return in.Errorf("Dem API-Key fehlt ein Recht: %s", paiMessage(he.Body))
		case he.Status == 409:
			return in.Errorf("Paperless-AI: %s", paiMessage(he.Body))
		}
	}
	return err
}

var (
	paiKeyRe     = regexp.MustCompile(`^pai_[a-z0-9]{8}_[A-Za-z0-9_-]{20,}$`)
	paiGlobalRe  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	ngxTokenRe   = regexp.MustCompile(`^[0-9a-f]{40}$`)
	aiProviderRe = regexp.MustCompile(`^(sk-|sk_|AIza|gsk_)`)
)

// redirectsElsewhere asks the address once without following redirects: if
// it sends the request on to another server, that server never got the key
// (keepKeysHome drops it there). It returns where to, or "".
func redirectsElsewhere(ctx context.Context, in *Instance, path string) string {
	if in.Client == nil {
		return ""
	}
	base, err := url.Parse(in.BaseURL)
	if err != nil {
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(in.BaseURL, "/")+path, nil)
	if err != nil {
		return ""
	}
	c := *in.Client // a copy: the shared client stays as it is
	c.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := c.Do(req)
	if err != nil {
		return ""
	}
	resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		return ""
	}
	loc, err := resp.Location()
	if err != nil || strings.EqualFold(loc.Host, base.Host) {
		return ""
	}
	return loc.Scheme + "://" + loc.Host
}

// paiUnauthorized tells why the key was refused: a key of another kind (the
// Paperless-ngx token, the AI provider's key), a key Paperless-AI does not
// know, or a proxy in front that asks for its own login.
func paiUnauthorized(in *Instance, body string) error {
	key := strings.TrimSpace(in.V("apiKey"))
	// Paperless-AI answers with JSON; a login page of a proxy (Authentik, Authelia, basic auth) with HTML.
	low := strings.ToLower(body)
	proxy := strings.Contains(low, "<html") || strings.Contains(low, "<!doctype")
	switch {
	case proxy:
		return in.Errorf("Die Anmeldung wird vor Paperless-AI abgelehnt (401) – steht ein Proxy mit eigener Anmeldung davor? Dann als lokale Adresse Paperless-AI direkt eintragen, z. B. http://192.168.1.20:3000")
	case ngxTokenRe.MatchString(key):
		return in.Errorf("Das ist der Token von Paperless-ngx – Paperless-AI hat eigene Schlüssel: unter Settings → API keys einen anlegen (beginnt mit pai_) oder den Global API key aus Settings → Integrations nehmen")
	case aiProviderRe.MatchString(key):
		return in.Errorf("Das ist der Schlüssel des KI-Anbieters – Heimdeck braucht einen Schlüssel von Paperless-AI: unter Settings → API keys einen anlegen (beginnt mit pai_)")
	case paiKeyRe.MatchString(key):
		return in.Errorf("Paperless-AI kennt diesen API-Key nicht (401) – wurde er gelöscht, oder ist das Konto gesperrt? Unter Settings → API keys einen neuen anlegen")
	case paiGlobalRe.MatchString(key):
		return in.Errorf("Paperless-AI kennt diesen Schlüssel nicht (401) – der Global API key wurde vielleicht neu erzeugt: unter Settings → Integrations nachsehen oder unter Settings → API keys einen eigenen anlegen")
	}
	return in.Errorf("Paperless-AI lehnt den API-Key ab (401) – unter Settings → API keys einen Schlüssel anlegen (beginnt mit pai_) oder den Global API key aus Settings → Integrations nehmen")
}

// paiMessage is the "error" text of a Paperless-AI error answer.
func paiMessage(body string) string {
	var e struct {
		Error  string `json:"error"`
		Detail string `json:"detail"`
	}
	if json.Unmarshal([]byte(body), &e) == nil && firstNonEmpty(e.Error, e.Detail) != "" {
		return firstNonEmpty(e.Error, e.Detail)
	}
	return body
}

func isStatus(err error, codes ...int) bool {
	var he *HTTPError
	if !errors.As(err, &he) {
		return false
	}
	for _, c := range codes {
		if he.Status == c {
			return true
		}
	}
	return false
}

type paiProcessing struct {
	Running   bool `json:"running"`
	Paused    bool `json:"paused"`
	Automatic bool `json:"automatic"`
	Scanning  bool `json:"scanning"`
	Current   []struct {
		DocumentID int    `json:"documentId"`
		Title      string `json:"title"`
		Stage      string `json:"stage"`
		StartedAt  string `json:"startedAt"`
	} `json:"current"`
	Queued           int     `json:"queued"`
	LastScanAt       string  `json:"lastScanAt"`
	NextScanAt       string  `json:"nextScanAt"`
	LastError        *string `json:"lastError"`
	RateLimitedUntil *string `json:"rateLimitedUntil"`
	LastProcessed    *struct {
		DocumentID  int    `json:"documentId"`
		Title       string `json:"title"`
		ProcessedAt string `json:"processedAt"`
	} `json:"lastProcessed"`
	ProcessedToday int `json:"processedToday"`
	Counts         struct {
		Processed int `json:"processed"`
		Failed    int `json:"failed"`
		Skipped   int `json:"skipped"`
	} `json:"counts"`
}

type paiDashboard struct {
	Version string `json:"version"`
	Archive *struct {
		Mode           string  `json:"mode"` // paperless | builtin
		Label          string  `json:"label"`
		Connected      bool    `json:"connected"`
		Error          string  `json:"error"`
		Documents      int     `json:"documents"`
		Tags           int     `json:"tags"`
		Correspondents int     `json:"correspondents"`
		DocumentTypes  int     `json:"documentTypes"`
		StorageBytes   float64 `json:"storageBytes"`
	} `json:"archive"`
	Processing paiProcessing `json:"processing"`
	Usage      *struct {
		TotalTokens int `json:"totalTokens"`
		Calls       int `json:"calls"`
	} `json:"usage"`
	AI *struct {
		Provider string `json:"provider"`
		Model    string `json:"model"`
	} `json:"ai"`
	RAG *struct {
		Enabled bool `json:"enabled"`
	} `json:"rag"`
}

func (p paperlessAI) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var d paiDashboard
	if err := p.call(ctx, in, "GET", "/api/dashboard", url.Values{"tzOffset": {"0"}}, nil, &d); err != nil {
		if isStatus(err, 404) {
			return p.fetchLegacy(ctx, in)
		}
		return nil, err
	}
	pr := d.Processing
	w := &Widget{Fit: true}
	if a := d.Archive; a != nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("Dokumente"), Value: in.Num(a.Documents)})
	}
	w.Stats = append(w.Stats,
		Stat{Label: in.T("Verarbeitet"), Value: in.Num(pr.Counts.Processed), Tone: "ok"},
		Stat{Label: in.T("Heute"), Value: in.Num(pr.ProcessedToday)},
		Stat{Label: in.T("Warteschlange"), Value: in.Num(pr.Queued + len(pr.Current)), Tone: toneIf(pr.Queued > 0, "warn")},
	)
	if pr.Counts.Failed > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Fehlgeschlagen"), Value: in.Num(pr.Counts.Failed), Tone: "bad"})
	}
	if pr.Counts.Skipped > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Übersprungen"), Value: in.Num(pr.Counts.Skipped)})
	}
	if a := d.Archive; a != nil {
		w.Stats = append(w.Stats,
			Stat{Label: in.T("Korrespondenten"), Value: in.Num(a.Correspondents)},
			Stat{Label: in.T("Tags"), Value: in.Num(a.Tags)},
			Stat{Label: in.T("Dokumenttypen"), Value: in.Num(a.DocumentTypes)},
		)
		if a.StorageBytes > 0 {
			w.Stats = append(w.Stats, Stat{Label: in.T("Speicher"), Value: in.Bytes(a.StorageBytes)})
		}
		archive := "Paperless-ngx"
		if a.Mode == "builtin" {
			archive = in.T("eingebaut")
		}
		w.Stats = append(w.Stats, Stat{Label: in.T("Archiv"), Value: archive, Tone: toneIf(!a.Connected, "bad")})
		if !a.Connected {
			w.Alerts = append(w.Alerts, strings.TrimSuffix(in.T("Archiv nicht erreichbar: %s", a.Error), ": "))
		}
	}
	if d.Usage != nil && d.Usage.TotalTokens > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Tokens"), Value: in.Num(d.Usage.TotalTokens)})
	}
	if d.AI != nil && d.AI.Model != "" {
		w.Stats = append(w.Stats, Stat{Label: in.T("KI-Modell"), Value: d.AI.Model})
	}
	if d.RAG != nil && d.RAG.Enabled {
		p.ragStat(ctx, in, w)
	}
	if pr.Paused {
		w.Alerts = append(w.Alerts, in.T("Die Verarbeitung ist pausiert"))
	}
	if pr.LastError != nil && *pr.LastError != "" {
		w.Alerts = append(w.Alerts, in.T("Letzter Fehler: %s", *pr.LastError))
	}
	if pr.RateLimitedUntil != nil {
		if t, err := time.Parse(time.RFC3339, *pr.RateLimitedUntil); err == nil && t.After(time.Now()) {
			w.Alerts = append(w.Alerts, in.T("Der KI-Anbieter bremst (Rate-Limit) bis %s", t.Local().Format("15:04")))
		}
	}

	// Buttons: they need a key with the "Administration" right.
	if pr.Paused {
		w.Actions = append(w.Actions, Action{ID: "resume", Label: in.T("Fortsetzen"), Tone: "primary"})
	} else {
		w.Actions = append(w.Actions, Action{ID: "scan", Label: in.T("Jetzt scannen"), Tone: "primary"},
			Action{ID: "pause", Label: in.T("Pausieren")})
	}
	if pr.Counts.Failed > 0 {
		w.Actions = append(w.Actions, Action{ID: "retry", Label: in.T("Fehlgeschlagene wiederholen")})
	}

	// Documents being analysed right now always come first.
	for _, c := range pr.Current {
		w.Items = append(w.Items, Item{Title: firstNonEmpty(c.Title, in.T("Dokument %d", c.DocumentID)),
			Subtitle: in.T("wird analysiert"), Badge: paiStage(in, c.Stage),
			URL: in.Link(fmt.Sprintf("/documents/%d", c.DocumentID)), Image: in.Proxy(fmt.Sprintf("/api/documents/%d/thumb", c.DocumentID))})
	}
	switch in.V("list") {
	case "keine":
		w.ItemsTitle = in.T("In Arbeit")
	case "Probleme":
		w.ItemsTitle = in.T("Probleme")
		p.problemItems(ctx, in, w)
	default:
		w.ItemsTitle = in.T("Zuletzt hinzugefügt")
		p.recentItems(ctx, in, w)
	}
	if len(w.Items) == 0 {
		w.ItemsTitle = ""
	}
	return w, nil
}

// ragStat adds the state of the AI index (the "Ask your archive" chat).
func (p paperlessAI) ragStat(ctx context.Context, in *Instance, w *Widget) {
	var r struct {
		Enabled   bool    `json:"enabled"`
		State     string  `json:"state"`
		Documents int     `json:"documents"`
		Chunks    int     `json:"chunks"`
		Embedded  int     `json:"embedded"`
		LastError *string `json:"lastError"`
		Progress  any     `json:"progress"`
	}
	if p.call(ctx, in, "GET", "/api/rag/status", nil, nil, &r) != nil || !r.Enabled {
		return
	}
	switch r.State {
	case "indexing":
		w.Stats = append(w.Stats, Stat{Label: in.T("KI-Index"), Value: in.T("indexiert …"), Tone: "warn"})
	case "error":
		w.Stats = append(w.Stats, Stat{Label: in.T("KI-Index"), Value: in.T("Fehler"), Tone: "bad"})
		if r.LastError != nil && *r.LastError != "" {
			w.Alerts = append(w.Alerts, in.T("KI-Index: %s", *r.LastError))
		}
	default:
		w.Stats = append(w.Stats, Stat{Label: in.T("KI-Index"), Value: in.T("%s Dokumente", in.Num(r.Documents)), Tone: "ok"})
	}
}

func paiStage(in *Instance, stage string) string {
	switch strings.ToLower(stage) {
	case "ocr":
		return "OCR"
	case "analyse", "analyze", "analysis", "ai":
		return in.T("KI")
	case "update", "updating", "saving", "save":
		return in.T("speichert")
	case "":
		return ""
	}
	return stage
}

func paiStatus(in *Instance, s string) string {
	switch s {
	case "processed":
		return in.T("verarbeitet")
	case "failed":
		return in.T("fehlgeschlagen")
	case "skipped":
		return in.T("übersprungen")
	}
	return in.T("offen")
}

type paiDoc struct {
	ID      int     `json:"id"`
	Title   string  `json:"title"`
	Created string  `json:"created"`
	Added   string  `json:"added"`
	Status  *string `json:"status"`
	Snippet string  `json:"snippet"`
}

func (p paperlessAI) recentItems(ctx context.Context, in *Instance, w *Widget) {
	var res struct {
		Results []paiDoc `json:"results"`
	}
	if p.call(ctx, in, "GET", "/api/documents", url.Values{"sort": {"-added"}, "pageSize": {"8"}, "count": {"false"}}, nil, &res) != nil {
		return
	}
	for _, d := range res.Results {
		status := ""
		if d.Status != nil {
			status = *d.Status
		}
		badge := paiStatus(in, status)
		w.Items = append(w.Items, p.docItem(in, d.ID, d.Title, in.Date(firstNonEmpty(d.Added, d.Created)), badge))
	}
}

func (p paperlessAI) docItem(in *Instance, id int, title, subtitle, badge string) Item {
	return Item{Title: firstNonEmpty(title, in.T("Dokument %d", id)), Subtitle: subtitle, Badge: badge,
		URL: in.Link(fmt.Sprintf("/documents/%d", id)), Image: in.Proxy(fmt.Sprintf("/api/documents/%d/thumb", id))}
}

type paiProblem struct {
	DocumentID int    `json:"documentId"`
	Title      string `json:"title"`
	Status     string `json:"status"`
	Reason     string `json:"reason"`
	Attempts   int    `json:"attempts"`
	UpdatedAt  string `json:"updatedAt"`
}

func (p paperlessAI) problemItems(ctx context.Context, in *Instance, w *Widget) {
	var raw json.RawMessage
	if p.call(ctx, in, "GET", "/api/processing/problems", url.Values{"status": {"failed"}}, nil, &raw) != nil {
		return
	}
	// A plain list, or wrapped as {items: [...]} / {results: [...]}.
	var list []paiProblem
	if json.Unmarshal(raw, &list) != nil {
		var wrap struct {
			Items   []paiProblem `json:"items"`
			Results []paiProblem `json:"results"`
		}
		json.Unmarshal(raw, &wrap)
		list = append(wrap.Items, wrap.Results...)
	}
	for _, pb := range list {
		it := p.docItem(in, pb.DocumentID, pb.Title, pb.Reason, in.T("fehlgeschlagen"))
		if pb.Status == "skipped" {
			it.Badge = in.T("übersprungen")
		}
		it.Actions = []Action{{ID: "retry", Label: in.T("Erneut"), Params: map[string]string{"id": strconv.Itoa(pb.DocumentID)}}}
		w.Items = append(w.Items, it)
	}
}

// fetchLegacy serves Paperless-AI 3.x, which has no dashboard API.
func (p paperlessAI) fetchLegacy(ctx context.Context, in *Instance) (*Widget, error) {
	var st struct {
		CurrentlyProcessing *struct {
			DocumentID int    `json:"documentId"`
			Title      string `json:"title"`
		} `json:"currentlyProcessing"`
		LastProcessed *struct {
			DocumentID  int    `json:"documentId"`
			Title       string `json:"title"`
			ProcessedAt string `json:"processed_at"`
		} `json:"lastProcessed"`
		ProcessedToday int  `json:"processedToday"`
		IsProcessing   bool `json:"isProcessing"`
	}
	w := &Widget{}
	if err := p.call(ctx, in, "GET", "/api/processing-status", nil, nil, &st); err != nil {
		// Very old versions: at least say whether it is up.
		var h struct {
			Status string `json:"status"`
		}
		if err2 := in.do(ctx, request{path: "/health"}, &h); err2 != nil {
			return nil, err
		}
		w.Stats = []Stat{{Label: in.T("Status"), Value: paiHealth(in, h.Status), Tone: toneIf(h.Status != "healthy", "warn")}}
		return w, nil
	}
	state := in.T("bereit")
	if st.IsProcessing {
		state = in.T("analysiert")
	}
	w.Stats = []Stat{
		{Label: in.T("Heute"), Value: in.Num(st.ProcessedToday)},
		{Label: in.T("Status"), Value: state, Tone: toneIf(st.IsProcessing, "ok")},
	}
	if c := st.CurrentlyProcessing; c != nil && c.DocumentID > 0 {
		w.Items = append(w.Items, Item{Title: firstNonEmpty(c.Title, in.T("Dokument %d", c.DocumentID)), Subtitle: in.T("wird analysiert")})
	}
	if l := st.LastProcessed; l != nil && l.DocumentID > 0 {
		w.Items = append(w.Items, Item{Title: firstNonEmpty(l.Title, in.T("Dokument %d", l.DocumentID)), Subtitle: in.Date(l.ProcessedAt), Badge: in.T("verarbeitet")})
	}
	if len(w.Items) > 0 {
		w.ItemsTitle = in.T("Verarbeitung")
	}
	return w, nil
}

func paiHealth(in *Instance, s string) string {
	switch s {
	case "healthy":
		return in.T("bereit")
	case "not_configured":
		return in.T("nicht eingerichtet")
	}
	return s
}

var paiThumbRe = regexp.MustCompile(`^/api/documents/\d+/thumb$`)

func (p paperlessAI) ImageRequest(ctx context.Context, in *Instance, path string) (string, map[string]string, error) {
	if !paiThumbRe.MatchString(path) {
		return "", nil, in.Errorf("nicht gefunden")
	}
	return path, p.headers(in), nil
}

func (p paperlessAI) Action(ctx context.Context, in *Instance, action string, params map[string]string) error {
	switch action {
	case "scan", "pause", "resume":
		return p.call(ctx, in, "POST", "/api/processing/"+action, nil, map[string]any{}, nil)
	case "retry":
		body := map[string]any{}
		if id, err := strconv.Atoi(params["id"]); err == nil && id > 0 {
			body["ids"] = []int{id}
		}
		return p.call(ctx, in, "POST", "/api/processing/retry", nil, body, nil)
	}
	return in.Errorf("unbekannte Aktion %q", action)
}

var paiTagRe = regexp.MustCompile(`<[^>]*>`)

// paiSnippet turns a search snippet (with <mark>) into short plain text.
func paiSnippet(s string) string {
	s = strings.Join(strings.Fields(html.UnescapeString(paiTagRe.ReplaceAllString(s, ""))), " ")
	if r := []rune(s); len(r) > 90 {
		s = string(r[:89]) + "…"
	}
	return s
}

func (p paperlessAI) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	// The passage search of the AI index runs at the same time, if wanted.
	type ragSource struct {
		DocumentID int    `json:"documentId"`
		Title      string `json:"title"`
		Snippet    string `json:"snippet"`
		Created    string `json:"created"`
	}
	var rag struct {
		Sources []ragSource `json:"sources"`
	}
	var wg sync.WaitGroup
	if in.V("aiSearch") == "true" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if p.call(ctx, in, "POST", "/api/rag/search", nil, map[string]any{"query": query, "limit": 6}, &rag) != nil {
				rag.Sources = nil
			}
		}()
	}
	var res struct {
		Results []paiDoc `json:"results"`
	}
	// q understands Paperless-AI's search syntax (tag:, from:, created: …).
	err := p.call(ctx, in, "GET", "/api/documents", url.Values{"q": {query}, "pageSize": {"8"}, "sort": {"relevance"}, "count": {"false"}}, nil, &res)
	wg.Wait()
	if err != nil {
		if isStatus(err, 404) {
			return nil, ErrNoSearch // 3.x has no document list
		}
		return nil, err
	}
	out := []Item{}
	seen := map[int]bool{}
	for _, d := range res.Results {
		seen[d.ID] = true
		sub := in.T("Dokument")
		if d.Created != "" {
			sub += " · " + in.Date(d.Created)
		}
		if s := paiSnippet(d.Snippet); s != "" {
			sub += " · " + s
		}
		out = append(out, Item{Title: firstNonEmpty(d.Title, in.T("Dokument %d", d.ID)), Subtitle: sub, URL: in.Link(fmt.Sprintf("/documents/%d", d.ID))})
	}
	for _, s := range rag.Sources {
		if s.DocumentID == 0 || seen[s.DocumentID] {
			continue
		}
		seen[s.DocumentID] = true
		sub := in.T("passt inhaltlich")
		if t := paiSnippet(s.Snippet); t != "" {
			sub += " · " + t
		}
		out = append(out, Item{Title: firstNonEmpty(s.Title, in.T("Dokument %d", s.DocumentID)), Subtitle: sub, Badge: in.T("KI"), URL: in.Link(fmt.Sprintf("/documents/%d", s.DocumentID))})
	}
	return out, nil
}
