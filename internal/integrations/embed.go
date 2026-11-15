package integrations

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// embed shows a web page inside the tile (an iframe in the browser). The
// browser loads the page itself; Heimdeck only checks now and then whether the
// page allows being shown inside another one, so the tile can say why it stays
// empty instead of showing the browser's "refused to connect".
type embed struct{}

func init() { register(embed{}) }

// EmbedView is what the browser needs for an embedded page.
type EmbedView struct {
	Height      int    `json:"height"`           // px outside the free grid
	Zoom        int    `json:"zoom"`             // percent
	Reload      int    `json:"reload,omitempty"` // seconds, 0 = never
	Interactive bool   `json:"interactive"`
	Blocked     string `json:"blocked,omitempty"` // the page forbids being embedded
	Hint        string `json:"hint,omitempty"`    // it allows only some sites
}

func (embed) Def() Definition {
	return Definition{
		ID: "embed", Name: "Webseite einbetten", Icon: "hd:embed", Category: "Allgemein",
		Description: "Zeigt eine Webseite direkt in der Kachel – z. B. ein Grafana-Panel, eine Wetterkarte oder die Oberfläche eines Geräts",
		Setup: []string{
			"Als Adresse die Seite eintragen, die in der Kachel erscheinen soll – mit lokaler und öffentlicher Adresse wie bei jeder Kachel.",
			"Manche Seiten verbieten, in anderen Seiten angezeigt zu werden (X-Frame-Options oder frame-ancestors). Die Kachel sagt es dann; über den Titel lässt sich die Seite weiterhin öffnen.",
			"Läuft Heimdeck über https, zeigt der Browser nur Seiten mit https an.",
		},
		Fields: []Field{
			{Key: "height", Label: "Höhe", Type: "select", Options: []string{"mittel (320 px)", "klein (200 px)", "groß (480 px)", "sehr groß (720 px)"}, Display: true,
				Help: "Im freien Raster bestimmt die Größe der Kachel die Höhe."},
			{Key: "zoom", Label: "Zoom", Type: "select", Options: []string{"100 %", "90 %", "75 %", "60 %", "50 %", "125 %"}, Display: true,
				Help: "Kleiner gezoomt passt mehr von der Seite in die Kachel."},
			{Key: "reload", Label: "Neu laden", Type: "select", Options: []string{"nie", "jede Minute", "alle 5 Minuten", "alle 15 Minuten", "jede Stunde"}, Display: true,
				Help: "Für Seiten, die sich nicht selbst aktualisieren."},
			{Key: "interactive", Label: "Bedienen", Type: "select", Options: []string{"ja", "nein, nur ansehen"}, Display: true,
				Help: "„nur ansehen“: Klicks und Scrollen gehen nicht an die Seite – z. B. für eine Anzeige an der Wand."},
		},
		HasWidget: true, Refresh: 600,
	}
}

func embedView(in *Instance) *EmbedView {
	v := &EmbedView{Height: 320, Zoom: 100, Interactive: in.V("interactive") != "nein, nur ansehen"}
	if h, ok := map[string]int{"klein (200 px)": 200, "groß (480 px)": 480, "sehr groß (720 px)": 720}[in.V("height")]; ok {
		v.Height = h
	}
	if z, ok := map[string]int{"90 %": 90, "75 %": 75, "60 %": 60, "50 %": 50, "125 %": 125}[in.V("zoom")]; ok {
		v.Zoom = z
	}
	v.Reload = map[string]int{"jede Minute": 60, "alle 5 Minuten": 300, "alle 15 Minuten": 900, "jede Stunde": 3600}[in.V("reload")]
	return v
}

func (e embed) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	v := embedView(in)
	// Whether the page may be embedded hardly ever changes: asked once an hour.
	// A page that is down says nothing – the browser shows that itself.
	type verdict struct{ Blocked, Hint string }
	if r, err := remember(in, "embed|"+in.BaseURL, time.Hour, func() (verdict, error) {
		blocked, hint, err := embedCheck(ctx, in)
		return verdict{blocked, hint}, err
	}); err == nil {
		v.Blocked, v.Hint = r.Blocked, r.Hint
	}
	return &Widget{Embed: v}, nil
}

func (embed) Test(ctx context.Context, in *Instance) error {
	blocked, _, err := embedCheck(ctx, in)
	if err != nil {
		return err
	}
	if blocked != "" {
		return in.Errorf("%s", blocked)
	}
	return nil
}

// embedCheck reads the page's headers: Content-Security-Policy frame-ancestors
// wins over X-Frame-Options, as in the browsers.
func embedCheck(ctx context.Context, in *Instance) (blocked, hint string, err error) {
	status, h, _, err := in.rawDo(ctx, http.MethodGet, "", nil, map[string]string{"Accept": "text/html,*/*"})
	if err != nil {
		return "", "", err
	}
	if status >= 400 {
		return "", "", in.Errorf("Die Seite antwortet mit Fehler %d", status)
	}
	return frameVerdict(in, h)
}

func frameVerdict(in *Instance, h http.Header) (blocked, hint string, err error) {
	for _, csp := range h.Values("Content-Security-Policy") {
		for _, d := range strings.Split(csp, ";") {
			f := strings.Fields(strings.ToLower(strings.TrimSpace(d)))
			if len(f) == 0 || f[0] != "frame-ancestors" {
				continue
			}
			var hosts []string
			for _, src := range f[1:] {
				switch src {
				case "*", "http:", "https:":
					return "", "", nil
				case "'none'", "'self'":
				default:
					hosts = append(hosts, src)
				}
			}
			if len(hosts) == 0 {
				return in.T("Diese Seite erlaubt nicht, in einer anderen Seite angezeigt zu werden (frame-ancestors)."), "", nil
			}
			return "", in.T("Die Seite erlaubt das Einbetten nur für: %s", strings.Join(hosts, ", ")), nil
		}
	}
	switch strings.ToUpper(strings.TrimSpace(h.Get("X-Frame-Options"))) {
	case "DENY", "SAMEORIGIN":
		return in.T("Diese Seite erlaubt nicht, in einer anderen Seite angezeigt zu werden (X-Frame-Options)."), "", nil
	}
	return "", "", nil
}
