// Package integrations contains one module per supported service.
//
// Every integration returns its data as a generic Widget (stats, a list of
// items, alerts and buttons). The frontend renders any Widget the same way, so
// adding a new service only needs a new file in this package.
package integrations

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// Field is one input in the "add tile" form.
type Field struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"` // text | password
	Help     string `json:"help,omitempty"`
	Required bool   `json:"required"`
	Secret   bool   `json:"secret"`
}

// Definition describes a service: how to recognise it, which icon it uses and
// which fields it needs.
type Definition struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Icon        string  `json:"icon"`
	Category    string  `json:"category"`
	Description string  `json:"description"`
	Fields      []Field `json:"fields"`
	HasWidget   bool    `json:"hasWidget"`
	HasSearch   bool    `json:"hasSearch"`
	// Images are substrings of Docker image names used for auto-discovery.
	Images []string `json:"-"`
	// Port is the usual container port, used when a container publishes several.
	Port int `json:"port,omitempty"`
}

// Instance is a configured tile, as seen by an integration.
type Instance struct {
	BaseURL   string            // used by the backend for API calls
	PublicURL string            // opened in the browser, used for links
	Values    map[string]string // config and secrets merged
	Client    *http.Client
}

func (i *Instance) V(key string) string { return strings.TrimSpace(i.Values[key]) }

// Link builds a browser link below the public URL.
func (i *Instance) Link(path string) string { return strings.TrimRight(i.PublicURL, "/") + path }

type Stat struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Tone  string `json:"tone,omitempty"` // ok | warn | bad
}

type Action struct {
	ID      string            `json:"id"`
	Label   string            `json:"label"`
	Params  map[string]string `json:"params,omitempty"`
	Tone    string            `json:"tone,omitempty"` // primary | danger
	Confirm string            `json:"confirm,omitempty"`
}

type Item struct {
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Badge    string   `json:"badge,omitempty"`
	Progress *float64 `json:"progress,omitempty"` // 0..100
	URL      string   `json:"url,omitempty"`
	Image    string   `json:"image,omitempty"`
	Actions  []Action `json:"actions,omitempty"`
}

type Widget struct {
	Stats      []Stat   `json:"stats,omitempty"`
	ItemsTitle string   `json:"itemsTitle,omitempty"`
	Items      []Item   `json:"items,omitempty"`
	Alerts     []string `json:"alerts,omitempty"`
	Actions    []Action `json:"actions,omitempty"`
}

type Integration interface {
	Def() Definition
	// Fetch loads the widget data. Link-only integrations return nil.
	Fetch(ctx context.Context, in *Instance) (*Widget, error)
}

// Tester is implemented by integrations with a cheap connection check.
// Integrations without it are tested by calling Fetch.
type Tester interface {
	Test(ctx context.Context, in *Instance) error
}

type Actioner interface {
	Action(ctx context.Context, in *Instance, action string, params map[string]string) error
}

type Searcher interface {
	Search(ctx context.Context, in *Instance, query string) ([]Item, error)
}

var registry = map[string]Integration{}

func register(i Integration) { registry[i.Def().ID] = i }

func Get(id string) (Integration, bool) {
	i, ok := registry[id]
	return i, ok
}

func All() []Definition {
	out := make([]Definition, 0, len(registry))
	for _, i := range registry {
		out = append(out, i.Def())
	}
	sort.Slice(out, func(a, b int) bool { return strings.ToLower(out[a].Name) < strings.ToLower(out[b].Name) })
	return out
}

// Match finds the integration for a Docker image name.
func Match(image string) (Integration, bool) {
	image = strings.ToLower(image)
	// Strip registry and tag: ghcr.io/hotio/radarr:release -> hotio/radarr
	if i := strings.LastIndex(image, ":"); i > strings.LastIndex(image, "/") {
		image = image[:i]
	}
	best, bestLen := Integration(nil), 0
	for _, in := range registry {
		for _, pat := range in.Def().Images {
			if strings.Contains(image, pat) && len(pat) > bestLen {
				best, bestLen = in, len(pat)
			}
		}
	}
	return best, best != nil
}

// ---- HTTP helpers ----

func NewClient(insecure bool) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{Transport: tr, Timeout: 10 * time.Second}
}

// HTTPError is returned for non-2xx responses, with a friendly hint.
type HTTPError struct {
	Status int
	Body   string
}

func (e *HTTPError) Error() string {
	switch e.Status {
	case 401, 403:
		return fmt.Sprintf("Zugriff verweigert (%d) – API-Key bzw. Zugangsdaten prüfen", e.Status)
	case 404:
		return "Adresse nicht gefunden (404) – stimmt die URL bzw. der Dienst-Typ?"
	}
	msg := fmt.Sprintf("Dienst antwortet mit Status %d", e.Status)
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

type request struct {
	method  string
	path    string
	query   url.Values
	headers map[string]string
	body    any
}

func (in *Instance) do(ctx context.Context, r request, out any) error {
	u := strings.TrimRight(in.BaseURL, "/") + r.path
	if len(r.query) > 0 {
		// Jellyseerr rejects "+" for spaces, so always use %20.
		u += "?" + strings.ReplaceAll(r.query.Encode(), "+", "%20")
	}
	var body io.Reader
	if r.body != nil {
		b, err := json.Marshal(r.body)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(b))
	}
	method := r.method
	if method == "" {
		method = http.MethodGet
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range r.headers {
		req.Header.Set(k, v)
	}
	resp, err := in.Client.Do(req)
	if err != nil {
		return friendlyNetErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return &HTTPError{Status: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("unerwartete Antwort (kein gültiges JSON) – stimmt der Dienst-Typ? (%v)", err)
	}
	return nil
}

func friendlyNetErr(err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return fmt.Errorf("Verbindung abgelehnt – läuft der Dienst und stimmt der Port?")
	case strings.Contains(s, "no such host"):
		return fmt.Errorf("Hostname nicht gefunden – stimmt die Adresse?")
	case strings.Contains(s, "certificate"):
		return fmt.Errorf("Zertifikatsfehler – bei selbstsignierten Zertifikaten „Zertifikat nicht prüfen“ aktivieren")
	case strings.Contains(s, "Client.Timeout") || strings.Contains(s, "deadline exceeded"):
		return fmt.Errorf("Zeitüberschreitung – Dienst nicht erreichbar")
	}
	return err
}

// ---- formatting helpers ----

func pct(v float64) *float64 {
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	return &v
}

func humanBytes(b float64) string {
	units := []string{"B", "KB", "MB", "GB", "TB", "PB"}
	i := 0
	for b >= 1024 && i < len(units)-1 {
		b /= 1024
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%.0f %s", b, units[i])
	}
	return strings.Replace(fmt.Sprintf("%.1f %s", b, units[i]), ".", ",", 1)
}

func num(n int) string {
	s := fmt.Sprint(n)
	if n < 1000 && n > -1000 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, c)
	}
	return string(out)
}

func toneIf(bad bool, tone string) string {
	if bad {
		return tone
	}
	return ""
}

// linkOnly is a service without API integration; it gets an icon, a status
// check and is recognised during Docker discovery.
type linkOnly struct{ def Definition }

func (l linkOnly) Def() Definition { return l.def }
func (l linkOnly) Fetch(context.Context, *Instance) (*Widget, error) {
	return nil, nil
}

func init() {
	for _, d := range []Definition{
		{ID: "link", Name: "Nur Link", Icon: "", Category: "Allgemein", Description: "Einfache Verknüpfung ohne Integration"},
		{ID: "zoraxy", Name: "Zoraxy", Icon: "zoraxy", Category: "Netzwerk", Images: []string{"zoraxy"}, Port: 8000},
		{ID: "anubis", Name: "Anubis", Icon: "anubis", Category: "Netzwerk", Images: []string{"anubis"}},
		{ID: "unifi", Name: "UniFi", Icon: "ubiquiti-unifi", Category: "Netzwerk", Images: []string{"unifi"}, Port: 8443},
		{ID: "fritzbox", Name: "FRITZ!Box", Icon: "fritzbox", Category: "Netzwerk", Description: "Router-Oberfläche (Integration folgt)"},
		{ID: "jdownloader", Name: "JDownloader", Icon: "jdownloader", Category: "Downloads", Images: []string{"jdownloader"}, Port: 5800},
		{ID: "portainer", Name: "Portainer", Icon: "portainer", Category: "System", Images: []string{"portainer"}, Port: 9000},
		{ID: "dockge", Name: "Dockge", Icon: "dockge", Category: "System", Images: []string{"dockge"}, Port: 5001},
		{ID: "bazarr", Name: "Bazarr", Icon: "bazarr", Category: "Medien", Images: []string{"bazarr"}, Port: 6767},
		{ID: "lidarr", Name: "Lidarr", Icon: "lidarr", Category: "Medien", Images: []string{"lidarr"}, Port: 8686},
		{ID: "qbittorrent", Name: "qBittorrent", Icon: "qbittorrent", Category: "Downloads", Images: []string{"qbittorrent"}, Port: 8080},
		{ID: "home-assistant", Name: "Home Assistant", Icon: "home-assistant", Category: "Smart Home", Images: []string{"home-assistant"}, Port: 8123},
		{ID: "vaultwarden", Name: "Vaultwarden", Icon: "vaultwarden", Category: "Sonstiges", Images: []string{"vaultwarden"}, Port: 80},
		{ID: "nextcloud", Name: "Nextcloud", Icon: "nextcloud", Category: "Sonstiges", Images: []string{"nextcloud"}, Port: 80},
		{ID: "immich", Name: "Immich", Icon: "immich", Category: "Medien", Images: []string{"immich-server"}, Port: 2283},
	} {
		register(linkOnly{def: d})
	}
}
