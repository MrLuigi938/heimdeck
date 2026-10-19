package integrations

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
)

// Immich: REST API with an API key (github.com/immich-app/immich,
// open-api/immich-openapi-specs.json). Thumbnails need the key, so they are
// loaded through Heimdeck's image proxy.

type immich struct{}

func init() { register(immich{}) }

func (immich) Def() Definition {
	return Definition{
		ID: "immich", Name: "Immich", Icon: "immich", Category: "Medien",
		Description: "Fotos, Videos und Speicherplatz, Suche nach Personen und Fotos (auf Wunsch per Beschreibung, z. B. „Strand bei Sonnenuntergang“)",
		Fields: []Field{
			{Key: "apiKey", Label: "API-Key", Type: "password", Required: true, Secret: true,
				Help: "Kontoeinstellungen → API-Schlüssel → Neuer API-Schlüssel. Rechte: asset.read, asset.view, asset.statistics, person.read (für die Gesamtstatistik als Admin: server.statistics)."},
			{Key: "smartSearch", Label: "Fotos nach Inhalt suchen (KI-Suche)", Type: "checkbox",
				Help: "Findet Fotos per Beschreibung, z. B. „Hund am Strand“. Braucht den Machine-Learning-Container von Immich und zeigt zu fast jedem Suchbegriff ähnliche Fotos. Ohne: Suche nach Personen und Dateinamen."},
		},
		HasWidget: true, HasSearch: true, Images: []string{"immich-server", "immich-app/immich"}, Port: 2283,
	}
}

func (immich) call(ctx context.Context, in *Instance, method, path string, q url.Values, body, out any) error {
	return in.do(ctx, request{method: method, path: "/api" + path, query: q, body: body,
		headers: map[string]string{"x-api-key": in.V("apiKey")}}, out)
}

func (i immich) Test(ctx context.Context, in *Instance) error {
	var v struct {
		Major int `json:"major"`
	}
	return i.call(ctx, in, "GET", "/server/version", nil, nil, &v)
}

func (i immich) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var st struct {
		Photos      int     `json:"photos"`
		Videos      int     `json:"videos"`
		Usage       float64 `json:"usage"`
		UsageByUser []struct {
			UserName string  `json:"userName"`
			Photos   int     `json:"photos"`
			Videos   int     `json:"videos"`
			Usage    float64 `json:"usage"`
		} `json:"usageByUser"`
	}
	err := i.call(ctx, in, "GET", "/server/statistics", nil, nil, &st)
	var he *HTTPError
	if errors.As(err, &he) && (he.Status == 401 || he.Status == 403) {
		// Not an admin key: show the own library only.
		var own struct {
			Images int `json:"images"`
			Videos int `json:"videos"`
		}
		if err := i.call(ctx, in, "GET", "/assets/statistics", nil, nil, &own); err != nil {
			return nil, err
		}
		return &Widget{Stats: []Stat{
			{Label: in.T("Fotos"), Value: in.Num(own.Images)},
			{Label: in.T("Videos"), Value: in.Num(own.Videos)},
		}}, nil
	}
	if err != nil {
		return nil, err
	}
	w := &Widget{Stats: []Stat{
		{Label: in.T("Fotos"), Value: in.Num(st.Photos)},
		{Label: in.T("Videos"), Value: in.Num(st.Videos)},
		{Label: in.T("Speicher"), Value: in.Bytes(st.Usage)},
	}}
	if len(st.UsageByUser) > 1 {
		w.ItemsTitle = in.T("Benutzer")
		for _, u := range st.UsageByUser {
			w.Items = append(w.Items, Item{Title: u.UserName,
				Subtitle: in.T("%s Fotos, %s Videos", in.Num(u.Photos), in.Num(u.Videos)), Badge: in.Bytes(u.Usage)})
		}
	}
	return w, nil
}

type immichAssets struct {
	Assets struct {
		Items []struct {
			ID               string `json:"id"`
			OriginalFileName string `json:"originalFileName"`
			FileCreatedAt    string `json:"fileCreatedAt"`
			LocalDateTime    string `json:"localDateTime"`
			Type             string `json:"type"`
			ExifInfo         *struct {
				City    string `json:"city"`
				Country string `json:"country"`
			} `json:"exifInfo"`
		} `json:"items"`
	} `json:"assets"`
}

// Search combines people with the photo search. Smart search (CLIP) needs the
// machine-learning container; without it the search falls back to file names.
func (i immich) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	out := []Item{}
	var people []struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		IsHidden bool   `json:"isHidden"`
	}
	if err := i.call(ctx, in, "GET", "/search/person", url.Values{"name": {query}}, nil, &people); err == nil {
		for _, p := range people {
			if p.IsHidden || p.Name == "" {
				continue
			}
			out = append(out, Item{Title: p.Name, Subtitle: in.T("Person"), URL: in.Link("/people/" + p.ID),
				Image: in.Proxy("/api/people/" + p.ID + "/thumbnail")})
			if len(out) == 3 {
				break
			}
		}
	}

	var res immichAssets
	var err error
	if in.V("smartSearch") == "true" {
		err = i.call(ctx, in, "POST", "/search/smart", nil, map[string]any{"query": query, "size": 6}, &res)
	}
	if err != nil || len(res.Assets.Items) == 0 {
		var meta immichAssets
		if err2 := i.call(ctx, in, "POST", "/search/metadata", nil, map[string]any{"originalFileName": query, "size": 8, "withExif": true}, &meta); err2 == nil {
			res, err = meta, nil
		}
	}
	if err != nil && len(out) == 0 {
		return nil, err
	}
	for _, a := range res.Assets.Items {
		sub := []string{}
		date := a.LocalDateTime
		if date == "" {
			date = a.FileCreatedAt
		}
		if date != "" {
			sub = append(sub, in.Date(date))
		}
		if a.ExifInfo != nil && a.ExifInfo.City != "" {
			sub = append(sub, a.ExifInfo.City)
		}
		if a.Type == "VIDEO" {
			sub = append(sub, in.T("Video"))
		}
		out = append(out, Item{Title: a.OriginalFileName, Subtitle: strings.Join(sub, " · "), URL: in.Link("/photos/" + a.ID),
			Image: in.Proxy("/api/assets/" + a.ID + "/thumbnail?size=thumbnail")})
	}
	return out, nil
}

var immichImageRe = regexp.MustCompile(`^/api/(assets|people)/[0-9a-f-]{36}/thumbnail(\?size=thumbnail)?$`)

func (immich) ImageRequest(ctx context.Context, in *Instance, path string) (string, map[string]string, error) {
	if !immichImageRe.MatchString(path) {
		return "", nil, in.Errorf("nicht gefunden")
	}
	return path, map[string]string{"x-api-key": in.V("apiKey")}, nil
}
