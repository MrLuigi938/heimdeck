package integrations

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sync"
	"time"
)

// jellyfin also covers Emby, which shares the API (below /emby, with its own
// token header and web links).
type jellyfin struct{ emby bool }

func init() {
	register(jellyfin{})
	register(jellyfin{emby: true})
}

func (j jellyfin) Def() Definition {
	if j.emby {
		return Definition{
			ID: "emby", Name: "Emby", Icon: "emby", Category: "Medien",
			Description: "Wer gerade was schaut (mit Transkodierung), neu Hinzugefügtes, Größe der Bibliothek, Updates, Suche",
			Fields: []Field{{Key: "apiKey", Label: "API-Key", Type: "password", Required: true, Secret: true,
				Help: "Einstellungen → Erweitert → API-Schlüssel → Neuer API-Schlüssel"}},
			HasWidget: true, HasSearch: true, Images: []string{"emby"}, Port: 8096,
		}
	}
	return Definition{
		ID: "jellyfin", Name: "Jellyfin", Icon: "jellyfin", Category: "Medien",
		Description: "Wer gerade was schaut (mit Transkodierung), neu Hinzugefügtes, Größe der Bibliothek, Updates, Suche",
		Fields: []Field{{Key: "apiKey", Label: "API-Key", Type: "password", Required: true, Secret: true,
			Help: "Dashboard → API-Schlüssel → „+“"}},
		HasWidget: true, HasSearch: true, Images: []string{"jellyfin"}, Port: 8096,
	}
}

func (j jellyfin) get(ctx context.Context, in *Instance, path string, q url.Values, out any) error {
	if j.emby {
		return in.do(ctx, request{path: "/emby" + path, query: q, headers: map[string]string{"X-Emby-Token": in.V("apiKey")}}, out)
	}
	return in.do(ctx, request{path: path, query: q, headers: map[string]string{
		"Authorization": fmt.Sprintf(`MediaBrowser Client="Heimdeck", Token="%s"`, in.V("apiKey")),
	}}, out)
}

func (j jellyfin) Test(ctx context.Context, in *Instance) error {
	return j.get(ctx, in, "/System/Info", nil, nil)
}

func (j jellyfin) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var sessions []struct {
		UserName       string `json:"UserName"`
		DeviceName     string `json:"DeviceName"`
		NowPlayingItem *struct {
			Name              string `json:"Name"`
			SeriesName        string `json:"SeriesName"`
			ParentIndexNumber int    `json:"ParentIndexNumber"`
			IndexNumber       int    `json:"IndexNumber"`
			Type              string `json:"Type"`
			RunTimeTicks      int64  `json:"RunTimeTicks"`
		} `json:"NowPlayingItem"`
		PlayState struct {
			PositionTicks int64  `json:"PositionTicks"`
			IsPaused      bool   `json:"IsPaused"`
			PlayMethod    string `json:"PlayMethod"` // DirectPlay | DirectStream | Transcode
		} `json:"PlayState"`
	}
	if err := j.get(ctx, in, "/Sessions", url.Values{"activeWithinSeconds": {"960"}}, &sessions); err != nil {
		return nil, err
	}
	var counts struct {
		MovieCount   int `json:"MovieCount"`
		SeriesCount  int `json:"SeriesCount"`
		EpisodeCount int `json:"EpisodeCount"`
		AlbumCount   int `json:"AlbumCount"`
		SongCount    int `json:"SongCount"`
		BookCount    int `json:"BookCount"`
	}
	j.get(ctx, in, "/Items/Counts", nil, &counts)
	var info struct {
		Version            string `json:"Version"`
		HasPendingRestart  bool   `json:"HasPendingRestart"`
		HasUpdateAvailable bool   `json:"HasUpdateAvailable"`
	}
	j.get(ctx, in, "/System/Info", nil, &info)

	w := &Widget{ItemsTitle: in.T("Läuft gerade")}
	transcodes := 0
	for _, s := range sessions {
		np := s.NowPlayingItem
		if np == nil {
			continue
		}
		title := np.Name
		if np.SeriesName != "" {
			title = fmt.Sprintf("%s · S%02dE%02d", np.SeriesName, np.ParentIndexNumber, np.IndexNumber)
		}
		it := Item{Title: title, Subtitle: s.UserName + " · " + s.DeviceName, URL: j.link(ctx, in, "")}
		if np.RunTimeTicks > 0 {
			it.Progress = pct(float64(s.PlayState.PositionTicks) / float64(np.RunTimeTicks) * 100)
		}
		if s.PlayState.PlayMethod == "Transcode" {
			transcodes++
			it.Badge = in.T("Transkodiert")
		}
		if s.PlayState.IsPaused {
			it.Badge = in.T("Pausiert")
		}
		w.Items = append(w.Items, it)
	}
	w.Stats = []Stat{
		{Label: in.T("Streams"), Value: fmt.Sprint(len(w.Items))},
		{Label: in.T("Transkodiert"), Value: fmt.Sprint(transcodes), Tone: toneIf(transcodes > 0, "warn")},
		{Label: in.T("Geräte aktiv"), Value: fmt.Sprint(len(sessions))},
		{Label: in.T("Filme"), Value: in.Num(counts.MovieCount)},
		{Label: in.T("Serien"), Value: in.Num(counts.SeriesCount)},
		{Label: in.T("Folgen"), Value: in.Num(counts.EpisodeCount)},
	}
	// Music and books only when the server has any.
	for _, c := range []struct {
		label string
		n     int
	}{{"Alben", counts.AlbumCount}, {"Titel", counts.SongCount}, {"Bücher", counts.BookCount}} {
		if c.n > 0 {
			w.Stats = append(w.Stats, Stat{Label: in.T(c.label), Value: in.Num(c.n)})
		}
	}
	if info.HasUpdateAvailable {
		w.Alerts = append(w.Alerts, in.T("Ein Update ist verfügbar"))
	}
	if info.HasPendingRestart {
		w.Alerts = append(w.Alerts, in.T("Ein Neustart ist nötig"))
	}
	// Nobody watching: what was added last instead of an empty list.
	if len(w.Items) == 0 {
		w.ItemsTitle = in.T("Neu hinzugefügt")
		w.Items = j.latest(ctx, in)
	}
	return w, nil
}

// latest lists the newest movies and episodes.
func (j jellyfin) latest(ctx context.Context, in *Instance) []Item {
	var res struct {
		Items []struct {
			ID                string `json:"Id"`
			Name              string `json:"Name"`
			Type              string `json:"Type"`
			SeriesName        string `json:"SeriesName"`
			ParentIndexNumber int    `json:"ParentIndexNumber"`
			IndexNumber       int    `json:"IndexNumber"`
			ProductionYear    int    `json:"ProductionYear"`
			DateCreated       string `json:"DateCreated"`
		} `json:"Items"`
	}
	q := url.Values{"SortBy": {"DateCreated"}, "SortOrder": {"Descending"}, "Recursive": {"true"},
		"IncludeItemTypes": {"Movie,Episode"}, "Limit": {"5"}, "Fields": {"DateCreated"}}
	if j.get(ctx, in, "/Items", q, &res) != nil {
		return nil
	}
	var out []Item
	for _, it := range res.Items {
		title, sub := it.Name, in.T("Film")
		if it.Type == "Episode" {
			title = fmt.Sprintf("%s · S%02dE%02d", firstNonEmpty(it.SeriesName, it.Name), it.ParentIndexNumber, it.IndexNumber)
			sub = it.Name
		} else if it.ProductionYear > 0 {
			sub = fmt.Sprintf("%s · %d", sub, it.ProductionYear)
		}
		if t, err := time.Parse(time.RFC3339Nano, it.DateCreated); err == nil {
			sub += " · " + in.Since(t)
		}
		out = append(out, Item{Title: title, Subtitle: sub, URL: j.link(ctx, in, it.ID)})
	}
	return out
}

func (j jellyfin) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	var res struct {
		Items []struct {
			ID             string `json:"Id"`
			Name           string `json:"Name"`
			Type           string `json:"Type"`
			ProductionYear int    `json:"ProductionYear"`
		} `json:"Items"`
	}
	q := url.Values{
		"searchTerm":       {query},
		"Recursive":        {"true"},
		"IncludeItemTypes": {"Movie,Series"},
		"Limit":            {"8"},
	}
	if err := j.get(ctx, in, "/Items", q, &res); err != nil {
		return nil, err
	}
	out := []Item{}
	for _, it := range res.Items {
		kind := in.T(map[string]string{"Movie": "Film", "Series": "Serie"}[it.Type])
		sub := kind
		if it.ProductionYear > 0 {
			sub = fmt.Sprintf("%s · %d", kind, it.ProductionYear)
		}
		out = append(out, Item{
			Title: it.Name, Subtitle: sub, Badge: in.T("Vorhanden"),
			URL:   j.link(ctx, in, it.ID),
			Image: in.Proxy(map[bool]string{true: "/emby", false: ""}[j.emby] + "/Items/" + it.ID + "/Images/Primary?maxHeight=120"),
		})
	}
	return out, nil
}

var embyServerIDs sync.Map // BaseURL → server id

// link opens an item (or the dashboard when id is empty) in the web client.
func (j jellyfin) link(ctx context.Context, in *Instance, id string) string {
	if !j.emby {
		if id == "" {
			return in.Link("/web/#/dashboard")
		}
		return in.Link("/web/#/details?id=" + id)
	}
	if id == "" {
		return in.Link("/web/index.html#!/dashboard")
	}
	server, ok := embyServerIDs.Load(in.BaseURL)
	if !ok {
		var info struct {
			ID string `json:"Id"`
		}
		if j.get(ctx, in, "/System/Info/Public", nil, &info) == nil && info.ID != "" {
			server = info.ID
			embyServerIDs.Store(in.BaseURL, info.ID)
		} else {
			server = ""
		}
	}
	return in.Link("/web/index.html#!/item?id=" + id + "&serverId=" + server.(string))
}

// jellyfinImageRe matches the posters the search hands out.
var jellyfinImageRe = regexp.MustCompile(`^(/emby)?/Items/[A-Za-z0-9_-]+/Images/Primary(\?maxHeight=\d+)?$`)

// ImageRequest loads posters through Heimdeck, so they also show when the
// dashboard runs on https and the server on plain http.
func (j jellyfin) ImageRequest(ctx context.Context, in *Instance, path string) (string, map[string]string, error) {
	if !jellyfinImageRe.MatchString(path) {
		return "", nil, in.Errorf("nicht gefunden")
	}
	return path, map[string]string{"X-Emby-Token": in.V("apiKey")}, nil
}
