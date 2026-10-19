package integrations

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// Bazarr: REST API below /api with the key from Settings → General
// (github.com/morpheus65535/bazarr, bazarr/api).

type bazarr struct{}

func init() { register(bazarr{}) }

func (bazarr) Def() Definition {
	return Definition{
		ID: "bazarr", Name: "Bazarr", Icon: "bazarr", Category: "Medien",
		Description: "Untertitel: fehlende Untertitel bei Serien und Filmen, Suche starten, Bibliothekssuche",
		Fields: []Field{{Key: "apiKey", Label: "API-Key", Type: "password", Required: true, Secret: true,
			Help: "Settings → General → Security → API Key"}},
		HasWidget: true, HasSearch: true, Images: []string{"bazarr"}, Port: 6767,
	}
}

func (bazarr) call(ctx context.Context, in *Instance, method, path string, q url.Values, out any, body ...any) error {
	r := request{method: method, path: "/api" + path, query: q,
		headers: map[string]string{"X-API-KEY": in.V("apiKey")}}
	if len(body) > 0 {
		r.body = body[0]
	}
	return in.do(ctx, r, out)
}

func (b bazarr) Test(ctx context.Context, in *Instance) error {
	var st struct {
		Data struct {
			Version string `json:"bazarr_version"`
		} `json:"data"`
	}
	return b.call(ctx, in, "GET", "/system/status", nil, &st)
}

type bazarrMissing []struct {
	Name string `json:"name"`
}

func (m bazarrMissing) String() string {
	var names []string
	for _, s := range m {
		names = append(names, s.Name)
	}
	return strings.Join(names, ", ")
}

func (b bazarr) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var badges struct {
		Episodes  int `json:"episodes"`
		Movies    int `json:"movies"`
		Providers int `json:"providers"`
		Status    int `json:"status"`
	}
	if err := b.call(ctx, in, "GET", "/badges", nil, &badges); err != nil {
		return nil, err
	}
	w := &Widget{Stats: []Stat{
		{Label: in.T("Fehlend: Folgen"), Value: in.Num(badges.Episodes), Tone: toneIf(badges.Episodes > 0, "warn")},
		{Label: in.T("Fehlend: Filme"), Value: in.Num(badges.Movies), Tone: toneIf(badges.Movies > 0, "warn")},
	}}
	if badges.Providers > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Anbieter gedrosselt"), Value: in.Num(badges.Providers), Tone: "warn"})
	}
	if badges.Status > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Probleme"), Value: in.Num(badges.Status), Tone: "bad"})
		w.Alerts = append(w.Alerts, in.T("Bazarr meldet Probleme – siehe System → Status"))
	}

	var eps struct {
		Data []struct {
			SeriesTitle string        `json:"seriesTitle"`
			Episode     string        `json:"episode_number"`
			Title       string        `json:"episodeTitle"`
			Missing     bazarrMissing `json:"missing_subtitles"`
			SeriesID    int           `json:"sonarrSeriesId"`
		} `json:"data"`
	}
	var movies struct {
		Data []struct {
			Title   string        `json:"title"`
			Missing bazarrMissing `json:"missing_subtitles"`
			ID      int           `json:"radarrId"`
		} `json:"data"`
	}
	page := url.Values{"start": {"0"}, "length": {"3"}}
	b.call(ctx, in, "GET", "/episodes/wanted", page, &eps)
	b.call(ctx, in, "GET", "/movies/wanted", page, &movies)
	w.ItemsTitle = in.T("Fehlende Untertitel")
	for _, e := range eps.Data {
		w.Items = append(w.Items, Item{Title: e.SeriesTitle + " · " + e.Episode, Subtitle: e.Missing.String(),
			URL: in.Link("/series/" + strconv.Itoa(e.SeriesID))})
	}
	for _, m := range movies.Data {
		w.Items = append(w.Items, Item{Title: m.Title, Subtitle: m.Missing.String(), URL: in.Link("/movies/" + strconv.Itoa(m.ID))})
	}
	if badges.Episodes > 0 || badges.Movies > 0 {
		w.Actions = []Action{{ID: "search_wanted", Label: in.T("Fehlende suchen")}}
	}
	return w, nil
}

func (b bazarr) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	var res []struct {
		Title    string  `json:"title"`
		Year     string  `json:"year"`
		SeriesID int     `json:"sonarrSeriesId"`
		MovieID  int     `json:"radarrId"`
		Poster   *string `json:"poster"`
	}
	if err := b.call(ctx, in, "GET", "/system/searches", url.Values{"query": {query}}, &res); err != nil {
		return nil, err
	}
	out := []Item{}
	for _, r := range res {
		it := Item{Title: r.Title}
		if r.SeriesID != 0 {
			it.Subtitle, it.URL = in.T("Serie"), in.Link("/series/"+strconv.Itoa(r.SeriesID))
		} else {
			it.Subtitle, it.URL = in.T("Film"), in.Link("/movies/"+strconv.Itoa(r.MovieID))
		}
		if r.Year != "" {
			it.Subtitle += " · " + r.Year
		}
		if r.Poster != nil && *r.Poster != "" {
			it.Image = in.Proxy(stripBasePath(in.BaseURL, *r.Poster))
		}
		out = append(out, it)
		if len(out) == 8 {
			break
		}
	}
	return out, nil
}

// stripBasePath turns "/bazarr/images/…" into "/images/…" when the service runs
// below a base path that is already part of the configured address.
func stripBasePath(base, path string) string {
	if u, err := url.Parse(base); err == nil {
		if p := strings.TrimRight(u.Path, "/"); p != "" && strings.HasPrefix(path, p+"/") {
			return strings.TrimPrefix(path, p)
		}
	}
	return path
}

func (b bazarr) ImageRequest(ctx context.Context, in *Instance, path string) (string, map[string]string, error) {
	if !hasPrefix(path, "/images/") {
		return "", nil, in.Errorf("nicht gefunden")
	}
	return path, map[string]string{"X-API-KEY": in.V("apiKey")}, nil
}

func (b bazarr) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	if action != "search_wanted" {
		return in.Errorf("unbekannte Aktion %q", action)
	}
	// The scheduler runs these in the background, so the call returns right away.
	for _, task := range []string{"wanted_search_missing_subtitles_series", "wanted_search_missing_subtitles_movies"} {
		if err := b.call(ctx, in, "POST", "/system/tasks", url.Values{"taskid": {task}}, nil, map[string]string{"taskid": task}); err != nil {
			return err
		}
	}
	return nil
}
