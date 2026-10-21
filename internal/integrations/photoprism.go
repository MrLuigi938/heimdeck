package integrations

import (
	"context"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

// PhotoPrism: REST API under /api/v1 with an app password as bearer token
// (github.com/photoprism/photoprism, internal/api). Thumbnails need a preview
// token from /api/v1/config; Heimdeck adds it on the server, so it never
// reaches the browser.

type photoprism struct{}

func init() { register(photoprism{}) }

func (photoprism) Def() Definition {
	return Definition{
		ID: "photoprism", Name: "PhotoPrism", Icon: "photoprism", Category: "Medien",
		Description: "Fotos, Videos, Alben, Personen und neueste Fotos, Suche nach Fotos (Ort, Person, Motiv, Datum)",
		Setup: []string{
			"In PhotoPrism unter Einstellungen → Konto → Apps und Geräte ein App-Passwort erstellen (Bereich: alle oder „photos albums config“) und hier eintragen.",
			"Als lokale Adresse die Adresse von PhotoPrism eintragen, z. B. http://<IP>:2342.",
		},
		Fields: []Field{
			{Key: "token", Label: "App-Passwort", Type: "password", Required: true, Secret: true,
				Help: "Einstellungen → Konto → Apps und Geräte → Verbinden"},
		},
		HasWidget: true, HasSearch: true, Images: []string{"photoprism"}, Port: 2342,
	}
}

func (photoprism) get(ctx context.Context, in *Instance, path string, q url.Values, out any) error {
	return in.do(ctx, request{path: "/api/v1" + path, query: q, headers: map[string]string{"Authorization": "Bearer " + in.V("token")}}, out)
}

type ppConfig struct {
	PreviewToken string `json:"previewToken"`
	Count        struct {
		Photos    int `json:"photos"`
		Videos    int `json:"videos"`
		Albums    int `json:"albums"`
		People    int `json:"people"`
		Review    int `json:"review"`
		Favorites int `json:"favorites"`
	} `json:"count"`
}

// ppTokens remembers the preview token per tile for the image proxy.
var ppTokens sync.Map

func (p photoprism) config(ctx context.Context, in *Instance) (*ppConfig, error) {
	var c ppConfig
	if err := p.get(ctx, in, "/config", nil, &c); err != nil {
		return nil, err
	}
	if c.PreviewToken != "" {
		ppTokens.Store(in.BaseURL+"\x00"+in.V("token"), c.PreviewToken)
	}
	return &c, nil
}

func (p photoprism) Test(ctx context.Context, in *Instance) error {
	c, err := p.config(ctx, in)
	if err == nil && c.PreviewToken == "" && c.Count.Photos == 0 && c.Count.Albums == 0 {
		// Without a valid session PhotoPrism answers with its public config.
		var photos []ppPhoto
		err = p.get(ctx, in, "/photos", url.Values{"count": {"1"}}, &photos)
	}
	return err
}

type ppPhoto struct {
	UID        string    `json:"UID"`
	Type       string    `json:"Type"`
	Title      string    `json:"Title"`
	TakenAt    time.Time `json:"TakenAt"`
	PlaceLabel string    `json:"PlaceLabel"`
	Hash       string    `json:"Hash"`
}

var ppHashRe = regexp.MustCompile(`^[0-9a-f]{40}$`)

func (photoprism) item(in *Instance, ph ppPhoto) Item {
	var sub []string
	if !ph.TakenAt.IsZero() && ph.TakenAt.Year() > 1900 {
		sub = append(sub, in.Date(ph.TakenAt.Format(time.RFC3339)))
	}
	if ph.PlaceLabel != "" && ph.PlaceLabel != "Unknown" {
		sub = append(sub, ph.PlaceLabel)
	}
	it := Item{Title: firstNonEmpty(ph.Title, in.T("Foto")), Subtitle: strings.Join(sub, " · "),
		URL: in.Link("/library/browse?view=cards&q=" + url.QueryEscape("uid:"+ph.UID))}
	if ph.Type == "video" || ph.Type == "live" {
		it.Badge = in.T("Video")
	}
	if ppHashRe.MatchString(ph.Hash) {
		it.Image = in.Proxy("/api/v1/t/" + ph.Hash + "/tile_224")
	}
	return it
}

func (p photoprism) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	c, err := p.config(ctx, in)
	if err != nil {
		return nil, err
	}
	w := &Widget{Stats: []Stat{
		{Label: in.T("Fotos"), Value: in.Num(c.Count.Photos)},
		{Label: in.T("Videos"), Value: in.Num(c.Count.Videos)},
		{Label: in.T("Alben"), Value: in.Num(c.Count.Albums)},
		{Label: in.T("Personen"), Value: in.Num(c.Count.People)},
	}}
	if c.Count.Review > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Zu prüfen"), Value: in.Num(c.Count.Review), Tone: "warn"})
	}
	var photos []ppPhoto
	if p.get(ctx, in, "/photos", url.Values{"count": {"3"}, "order": {"added"}, "merged": {"true"}}, &photos) == nil && len(photos) > 0 {
		w.ItemsTitle = in.T("Zuletzt hinzugefügt")
		w.Thumbs = true
		for _, ph := range photos {
			w.Items = append(w.Items, p.item(in, ph))
		}
	}
	return w, nil
}

// Search uses PhotoPrism's own search: words, places, people, labels, dates.
func (p photoprism) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	var photos []ppPhoto
	if err := p.get(ctx, in, "/photos", url.Values{"count": {"8"}, "q": {strings.TrimSpace(query)}, "merged": {"true"}, "order": {"relevance"}}, &photos); err != nil {
		return nil, err
	}
	out := []Item{}
	for _, ph := range photos {
		out = append(out, p.item(in, ph))
	}
	return out, nil
}

var ppThumbRe = regexp.MustCompile(`^/api/v1/t/([0-9a-f]{40})/tile_224$`)

// ImageRequest adds the preview token to a thumbnail path.
func (p photoprism) ImageRequest(ctx context.Context, in *Instance, path string) (string, map[string]string, error) {
	m := ppThumbRe.FindStringSubmatch(path)
	if m == nil {
		return "", nil, in.Errorf("nicht gefunden")
	}
	tok, ok := ppTokens.Load(in.BaseURL + "\x00" + in.V("token"))
	if !ok {
		c, err := p.config(ctx, in)
		if err != nil {
			return "", nil, err
		}
		tok = c.PreviewToken
	}
	if !ppHashRe.MatchString(m[1]) || tok == "" || strings.ContainsAny(tok.(string), "/?#") {
		return "", nil, in.Errorf("nicht gefunden")
	}
	return "/api/v1/t/" + m[1] + "/" + url.PathEscape(tok.(string)) + "/tile_224",
		map[string]string{"Authorization": "Bearer " + in.V("token")}, nil
}
