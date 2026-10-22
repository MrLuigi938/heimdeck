// Package integrations contains one module per supported service.
//
// Every integration returns its data as a generic Widget (stats, a list of
// items, alerts and buttons). The frontend renders any Widget the same way, so
// adding a new service only needs a new file in this package.
package integrations

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/i18n"
)

// Field is one input in the "add tile" form.
type Field struct {
	Key      string `json:"key"`
	Label    string `json:"label"`
	Type     string `json:"type"` // text | password | textarea | select | checkbox ("true" or "")
	Help     string `json:"help,omitempty"`
	Required bool   `json:"required"`
	Secret   bool   `json:"secret"`
	// Options are the choices of a select field; the first is the default.
	Options []string `json:"options,omitempty"`
	// Pick offers a searchable list (from a Chooser) to fill a comma-separated field.
	Pick bool `json:"pick,omitempty"`
}

// Choice is one entry of a field's pick list.
type Choice struct {
	ID    string `json:"id"`
	Name  string `json:"name,omitempty"`
	Value string `json:"value,omitempty"`
	Group string `json:"group,omitempty"`
}

// Chooser is implemented by integrations with Pick fields. An empty query
// returns a useful selection to start with.
type Chooser interface {
	Choices(ctx context.Context, in *Instance, field, query string) ([]Choice, error)
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
	// NoURL marks integrations that work without an address (e.g. local Docker).
	NoURL bool `json:"noUrl,omitempty"`
	// Setup lists the steps to prepare the service, shown in the tile editor.
	Setup []string `json:"setup,omitempty"`
	// Refresh is how often the tile reloads, in seconds (default 15), for
	// values that change quickly such as energy flows.
	Refresh int `json:"refresh,omitempty"`
}

// Instance is a configured tile, as seen by an integration.
type Instance struct {
	BaseURL   string            // used by the backend for API calls
	PublicURL string            // opened in the browser, used for links
	Values    map[string]string // config and secrets merged
	Client    *http.Client
	Lang      string // "de" or "en", for texts shown to the user
	TileID    int64
}

func (i *Instance) V(key string) string { return strings.TrimSpace(i.Values[key]) }

// T translates a user-facing text into the instance's language.
func (i *Instance) T(s string, args ...any) string { return i18n.T(i.Lang, s, args...) }

func (i *Instance) Num(n int) string         { return i18n.Num(i.Lang, n) }
func (i *Instance) Bytes(b float64) string   { return i18n.Bytes(i.Lang, b) }
func (i *Instance) Date(iso string) string   { return i18n.Date(i.Lang, iso) }
func (i *Instance) Since(t time.Time) string { return i18n.Since(i.Lang, t) }
func (i *Instance) Errorf(s string, args ...any) error {
	return errors.New(i.T(s, args...))
}

// Status translates the download states used by the *arr apps and SABnzbd.
func (i *Instance) Status(s string) string {
	de := map[string]string{
		"downloading": "Lädt", "queued": "Wartet", "paused": "Pausiert", "completed": "Fertig",
		"delay": "Verzögert", "warning": "Warnung", "failed": "Fehlgeschlagen", "idle": "Bereit",
		"fetching": "Lädt", "grabbing": "Lädt", "extracting": "Entpackt", "verifying": "Prüft", "repairing": "Repariert",
	}[strings.ToLower(s)]
	if de == "" {
		return s
	}
	return i.T(de)
}

// Proxy returns a dashboard URL that loads an image from the service through
// Heimdeck, for covers and thumbnails that need the API key.
func (i *Instance) Proxy(path string) string {
	return fmt.Sprintf("/api/tiles/%d/image?p=%s", i.TileID, url.QueryEscape(path))
}

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
	// Thumbs shows the items as a grid of their pictures (snapshots, photos).
	Thumbs bool `json:"thumbs,omitempty"`
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

// ImageSource is implemented by integrations that hand out Proxy URLs. It
// returns the request path (with auth added) and headers for an image path, or
// an error if the path is not one the integration serves images from.
type ImageSource interface {
	ImageRequest(ctx context.Context, in *Instance, path string) (string, map[string]string, error)
}

// FetchImage loads an image through an ImageSource. The caller closes the body.
func FetchImage(ctx context.Context, src ImageSource, in *Instance, path string) (*http.Response, error) {
	if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || strings.Contains(path, "..") {
		return nil, errors.New("invalid image path")
	}
	path, headers, err := src.ImageRequest(ctx, in, path)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(in.BaseURL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := in.Client.Do(req)
	if err != nil {
		// The URL may carry a key, so never pass the raw error on.
		if fe := friendlyNetErr(in.Lang, err); fe != err {
			return nil, fe
		}
		return nil, errors.New(i18n.T(in.Lang, "Dienst nicht erreichbar"))
	}
	if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), "image/") {
		resp.Body.Close()
		return nil, &HTTPError{Status: resp.StatusCode, Lang: in.Lang}
	}
	return resp, nil
}

// namedID is an entry of a pick list field, with the display name the user
// gave it (empty: use the service's own name).
type namedID struct{ ID, Name string }

// namedIDs reads pick list fields like "sensor.temp = Wohnzimmer, light.kueche":
// entries separated by commas, semicolons or line breaks, each optionally
// followed by "= display name". Entries without a name may also be separated
// by spaces, as lists were before names existed.
func namedIDs(list string) []namedID {
	var out []namedID
	for _, part := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ';' || r == '\n' }) {
		if id, name, ok := strings.Cut(part, "="); ok {
			if id = strings.TrimSpace(id); id != "" {
				out = append(out, namedID{ID: id, Name: strings.TrimSpace(name)})
			}
			continue
		}
		for _, id := range strings.Fields(part) {
			out = append(out, namedID{ID: id})
		}
	}
	return out
}

// hasPrefix reports whether path starts with one of the prefixes.
func hasPrefix(path string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

var registry = map[string]Integration{}

func register(i Integration) { registry[i.Def().ID] = i }

// Register adds an integration implemented outside this package.
func Register(i Integration) { register(i) }

func Get(id string) (Integration, bool) {
	i, ok := registry[id]
	return i, ok
}

// All returns every definition with its texts translated into lang.
func All(lang string) []Definition {
	out := make([]Definition, 0, len(registry))
	for _, i := range registry {
		out = append(out, Localize(i.Def(), lang))
	}
	sort.Slice(out, func(a, b int) bool { return strings.ToLower(out[a].Name) < strings.ToLower(out[b].Name) })
	return out
}

// Localize translates the user-facing texts of a definition.
func Localize(d Definition, lang string) Definition {
	d.Name = i18n.T(lang, d.Name)
	d.Category = i18n.T(lang, d.Category)
	d.Description = i18n.T(lang, d.Description)
	fields := make([]Field, len(d.Fields))
	for i, f := range d.Fields {
		f.Label = i18n.T(lang, f.Label)
		f.Help = i18n.T(lang, f.Help)
		fields[i] = f
	}
	d.Fields = fields
	setup := make([]string, len(d.Setup))
	for i, step := range d.Setup {
		setup[i] = i18n.T(lang, step)
	}
	d.Setup = setup
	return d
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

// MatchName finds the integration for a VM or container name such as
// "jellyfin", "adguard-home" or "paperless-ngx" (names and tags as used by the
// Proxmox community scripts). Link-only services count too.
func MatchName(names ...string) (Integration, bool) {
	norm := func(s string) string {
		return strings.NewReplacer("-", "", "_", "", " ", "", ".", "", "/", "").Replace(strings.ToLower(s))
	}
	best, bestLen := Integration(nil), 0
	for _, n := range names {
		n = norm(n)
		if n == "" {
			continue
		}
		for id, in := range registry {
			if id == "link" {
				continue
			}
			d := in.Def()
			cands := append([]string{d.ID, d.Name, strings.Split(d.Name, " ")[0], d.Icon}, d.Images...)
			for _, c := range cands {
				c = norm(c)
				// Very short patterns ("stash") must match the whole name.
				if len(c) >= 4 && strings.Contains(n, c) && len(c) > bestLen || c == n && len(c) > bestLen {
					best, bestLen = in, len(c)
				}
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
	Lang   string
}

func (e *HTTPError) Error() string {
	switch e.Status {
	case 401, 403:
		return i18n.T(e.Lang, "Zugriff verweigert (%d) – API-Key bzw. Zugangsdaten prüfen", e.Status)
	case 404:
		return i18n.T(e.Lang, "Adresse nicht gefunden (404) – stimmt die URL bzw. der Dienst-Typ?")
	}
	msg := i18n.T(e.Lang, "Dienst antwortet mit Status %d", e.Status)
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
		return friendlyNetErr(in.Lang, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return &HTTPError{Status: resp.StatusCode, Body: strings.TrimSpace(string(b)), Lang: in.Lang}
	}
	if out == nil {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	br := bufio.NewReader(resp.Body)
	if isHTML(resp, br) {
		return notJSON{in.htmlAnswer(req, resp)}
	}
	if err := json.NewDecoder(br).Decode(out); err != nil {
		return notJSON{in.Errorf("unerwartete Antwort (kein gültiges JSON) – stimmt der Dienst-Typ? (%v)", err)}
	}
	return nil
}

// isHTML reports whether the answer is a web page instead of the API's JSON.
func isHTML(resp *http.Response, br *bufio.Reader) bool {
	if strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		return true
	}
	head, _ := br.Peek(64)
	return strings.HasPrefix(strings.TrimSpace(string(head)), "<")
}

// htmlAnswer explains a web page where the API was expected: usually a login
// page of a proxy (Authentik, Authelia, Cloudflare Access) in front of the service.
func (in *Instance) htmlAnswer(req *http.Request, resp *http.Response) error {
	if final := resp.Request.URL; final.Host != req.URL.Host {
		return in.Errorf("Statt der Schnittstelle kommt eine Webseite – die Anfrage wurde zu %s weitergeleitet, vermutlich eine Anmeldeseite. Bitte die interne Adresse des Dienstes eintragen (Weitere Optionen → Interne Adresse).", final.Host)
	}
	return in.Errorf("Statt der Schnittstelle kommt eine Webseite – liegt eine Anmeldeseite (z. B. Authentik, Cloudflare) davor oder stimmt der Dienst-Typ nicht? Dann die interne Adresse des Dienstes eintragen (Weitere Optionen → Interne Adresse).")
}

// ErrNotJSON matches answers that are not the expected JSON, usually because
// the address belongs to a web page or a different service.
var ErrNotJSON = errors.New("not json")

type notJSON struct{ error }

func (notJSON) Is(target error) bool { return target == ErrNotJSON }

func friendlyNetErr(lang string, err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "connection refused"):
		return errors.New(i18n.T(lang, "Verbindung abgelehnt – läuft der Dienst und stimmt der Port?"))
	case strings.Contains(s, "no such host"):
		return errors.New(i18n.T(lang, "Hostname nicht gefunden – stimmt die Adresse?"))
	case strings.Contains(s, "certificate"):
		return errors.New(i18n.T(lang, "Zertifikatsfehler – bei selbstsignierten Zertifikaten „Zertifikat nicht prüfen“ aktivieren"))
	case strings.Contains(s, "Client.Timeout") || strings.Contains(s, "deadline exceeded"):
		return errors.New(i18n.T(lang, "Zeitüberschreitung – Dienst nicht erreichbar"))
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
		{ID: "jdownloader", Name: "JDownloader", Icon: "jdownloader", Category: "Downloads", Images: []string{"jdownloader"}, Port: 5800},
		{ID: "portainer", Name: "Portainer", Icon: "portainer", Category: "System", Images: []string{"portainer"}, Port: 9000},
		{ID: "dockge", Name: "Dockge", Icon: "dockge", Category: "System", Images: []string{"dockge"}, Port: 5001},
		{ID: "vaultwarden", Name: "Vaultwarden", Icon: "vaultwarden", Category: "Sonstiges", Images: []string{"vaultwarden"}, Port: 80},
	} {
		register(linkOnly{def: d})
	}
}
