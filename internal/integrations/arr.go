package integrations

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

var apiKeyField = Field{Key: "apiKey", Label: "API-Key", Type: "password", Required: true, Secret: true,
	Help: "Zu finden unter Settings → General → Security"}

// arr covers Sonarr and Radarr, which share the same v3 API.
type arr struct {
	def   Definition
	movie bool
}

func init() {
	register(&arr{def: Definition{
		ID: "sonarr", Name: "Sonarr", Icon: "sonarr", Category: "Medien",
		Description: "Serien: Download-Warteschlange, kommende und fehlende Folgen, Größe der Bibliothek, freier Speicher, Warnungen",
		Fields:      []Field{apiKeyField}, HasWidget: true, Images: []string{"sonarr"}, Port: 8989,
	}})
	register(&arr{movie: true, def: Definition{
		ID: "radarr", Name: "Radarr", Icon: "radarr", Category: "Medien",
		Description: "Filme: Download-Warteschlange, kommende Veröffentlichungen, fehlende Filme, Größe der Bibliothek, freier Speicher, Warnungen",
		Fields:      []Field{apiKeyField}, HasWidget: true, Images: []string{"radarr"}, Port: 7878,
	}})
}

func (a *arr) Def() Definition { return a.def }

func (a *arr) get(ctx context.Context, in *Instance, path string, q url.Values, out any) error {
	return in.do(ctx, request{path: "/api/v3" + path, query: q, headers: map[string]string{"X-Api-Key": in.V("apiKey")}}, out)
}

func (a *arr) Test(ctx context.Context, in *Instance) error {
	var status struct {
		AppName string `json:"appName"`
	}
	if err := a.get(ctx, in, "/system/status", nil, &status); err != nil {
		return err
	}
	if status.AppName != "" && !strings.EqualFold(status.AppName, a.def.Name) {
		return in.Errorf("unter dieser Adresse läuft %s, nicht %s", status.AppName, a.def.Name)
	}
	return nil
}

type arrQueue struct {
	TotalRecords int `json:"totalRecords"`
	Records      []struct {
		Title    string  `json:"title"`
		Size     float64 `json:"size"`
		Sizeleft float64 `json:"sizeleft"`
		Status   string  `json:"status"`
		Timeleft string  `json:"timeleft"`
		State    string  `json:"trackedDownloadState"`
		Series   *struct {
			Title string `json:"title"`
		} `json:"series"`
		Episode *struct {
			SeasonNumber  int `json:"seasonNumber"`
			EpisodeNumber int `json:"episodeNumber"`
		} `json:"episode"`
		Movie *struct {
			Title string `json:"title"`
			Year  int    `json:"year"`
		} `json:"movie"`
	} `json:"records"`
}

type arrHealth []struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

func (a *arr) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var queue arrQueue
	q := url.Values{"pageSize": {"20"}}
	if a.movie {
		q.Set("includeMovie", "true")
	} else {
		q.Set("includeSeries", "true")
		q.Set("includeEpisode", "true")
	}
	if err := a.get(ctx, in, "/queue", q, &queue); err != nil {
		return nil, err
	}
	var health arrHealth
	a.get(ctx, in, "/health", nil, &health)

	w := &Widget{ItemsTitle: in.T("Warteschlange")}
	for _, r := range queue.Records {
		title := r.Title
		if r.Movie != nil {
			title = fmt.Sprintf("%s (%d)", r.Movie.Title, r.Movie.Year)
		} else if r.Series != nil {
			title = r.Series.Title
			if r.Episode != nil {
				title += fmt.Sprintf(" · S%02dE%02d", r.Episode.SeasonNumber, r.Episode.EpisodeNumber)
			}
		}
		it := Item{Title: title, URL: in.Link("/activity/queue")}
		if r.Size > 0 {
			it.Progress = pct((r.Size - r.Sizeleft) / r.Size * 100)
		}
		sub := []string{in.Status(r.Status)}
		if r.Timeleft != "" {
			sub = append(sub, in.T("noch %s", r.Timeleft))
		}
		it.Subtitle = strings.Join(sub, " · ")
		if r.State == "importPending" || r.State == "failedPending" || r.Status == "warning" {
			it.Badge = in.T("Prüfen")
		}
		w.Items = append(w.Items, it)
		if len(w.Items) == 5 {
			break
		}
	}

	upcoming, err := a.upcoming(ctx, in)
	if err == nil && len(upcoming) > 0 {
		label := in.T("Neue Folgen (7 Tage)")
		if a.movie {
			label = in.T("Releases (30 Tage)")
		}
		w.Stats = append(w.Stats, Stat{Label: label, Value: fmt.Sprint(len(upcoming))})
	}
	w.Stats = append([]Stat{{Label: in.T("In Warteschlange"), Value: fmt.Sprint(queue.TotalRecords)}}, w.Stats...)

	var missing struct {
		TotalRecords int `json:"totalRecords"`
	}
	if a.get(ctx, in, "/wanted/missing", url.Values{"pageSize": {"1"}, "monitored": {"true"}}, &missing) == nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("Fehlend"), Value: in.Num(missing.TotalRecords), Tone: toneIf(missing.TotalRecords > 0, "warn")})
	}
	if lib, ok := a.library(ctx, in); ok {
		kind := in.T("Serien")
		if a.movie {
			kind = in.T("Filme")
		}
		w.Stats = append(w.Stats, Stat{Label: kind, Value: in.Num(lib.count)}, Stat{Label: in.T("Auf der Platte"), Value: in.Bytes(lib.size)})
	}
	var roots []struct {
		Path      string  `json:"path"`
		FreeSpace float64 `json:"freeSpace"`
	}
	if a.get(ctx, in, "/rootfolder", nil, &roots) == nil && len(roots) > 0 {
		free := roots[0].FreeSpace
		for _, r := range roots[1:] {
			free = min(free, r.FreeSpace) // the fullest folder matters
		}
		w.Stats = append(w.Stats, Stat{Label: in.T("Freier Speicher"), Value: in.Bytes(free), Tone: toneIf(free < 50e9, "warn")})
	}

	if len(w.Items) == 0 && len(upcoming) > 0 {
		w.ItemsTitle = in.T("Demnächst")
		w.Items = upcoming
		if len(w.Items) > 5 {
			w.Items = w.Items[:5]
		}
	}
	errs := 0
	for _, h := range health {
		if h.Type == "error" {
			errs++
		}
		if len(w.Alerts) < 3 {
			w.Alerts = append(w.Alerts, h.Message)
		}
	}
	if len(health) > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Warnungen"), Value: fmt.Sprint(len(health)), Tone: map[bool]string{true: "bad", false: "warn"}[errs > 0]})
	}
	return w, nil
}

type arrLibrary struct {
	at    time.Time
	count int
	size  float64
}

// arrLibraries caches the library size per address: the full series or
// movie list can be large, so it is loaded at most every ten minutes.
var arrLibraries sync.Map

func (a *arr) library(ctx context.Context, in *Instance) (arrLibrary, bool) {
	key := a.def.ID + "\x00" + in.BaseURL
	if v, ok := arrLibraries.Load(key); ok && time.Since(v.(arrLibrary).at) < 10*time.Minute {
		return v.(arrLibrary), true
	}
	lib := arrLibrary{at: time.Now()}
	if a.movie {
		var movies []struct {
			SizeOnDisk float64 `json:"sizeOnDisk"`
		}
		if a.get(ctx, in, "/movie", nil, &movies) != nil {
			return lib, false
		}
		lib.count = len(movies)
		for _, m := range movies {
			lib.size += m.SizeOnDisk
		}
	} else {
		var series []struct {
			Statistics struct {
				SizeOnDisk float64 `json:"sizeOnDisk"`
			} `json:"statistics"`
		}
		if a.get(ctx, in, "/series", nil, &series) != nil {
			return lib, false
		}
		lib.count = len(series)
		for _, s := range series {
			lib.size += s.Statistics.SizeOnDisk
		}
	}
	arrLibraries.Store(key, lib)
	return lib, true
}

func (a *arr) upcoming(ctx context.Context, in *Instance) ([]Item, error) {
	now := time.Now().UTC()
	days := 7
	if a.movie {
		days = 30
	}
	q := url.Values{
		"start":       {now.Format(time.RFC3339)},
		"end":         {now.AddDate(0, 0, days).Format(time.RFC3339)},
		"unmonitored": {"false"},
	}
	if !a.movie {
		q.Set("includeSeries", "true")
	}
	var items []Item
	if a.movie {
		var movies []struct {
			Title           string `json:"title"`
			HasFile         bool   `json:"hasFile"`
			InCinemas       string `json:"inCinemas"`
			DigitalRelease  string `json:"digitalRelease"`
			PhysicalRelease string `json:"physicalRelease"`
		}
		if err := a.get(ctx, in, "/calendar", q, &movies); err != nil {
			return nil, err
		}
		for _, m := range movies {
			if m.HasFile {
				continue
			}
			kind, date := "Digital", m.DigitalRelease
			if date == "" || (m.PhysicalRelease != "" && m.PhysicalRelease < date) {
				kind, date = "Disc", m.PhysicalRelease
			}
			if date == "" {
				kind, date = in.T("Kino"), m.InCinemas
			}
			items = append(items, Item{Title: m.Title, Subtitle: kind + " · " + in.Date(date), URL: in.Link("/calendar")})
		}
	} else {
		var eps []struct {
			Title         string `json:"title"`
			SeasonNumber  int    `json:"seasonNumber"`
			EpisodeNumber int    `json:"episodeNumber"`
			AirDateUtc    string `json:"airDateUtc"`
			HasFile       bool   `json:"hasFile"`
			Series        struct {
				Title string `json:"title"`
			} `json:"series"`
		}
		if err := a.get(ctx, in, "/calendar", q, &eps); err != nil {
			return nil, err
		}
		sort.Slice(eps, func(i, j int) bool { return eps[i].AirDateUtc < eps[j].AirDateUtc })
		for _, e := range eps {
			if e.HasFile {
				continue
			}
			items = append(items, Item{
				Title:    fmt.Sprintf("%s · S%02dE%02d", e.Series.Title, e.SeasonNumber, e.EpisodeNumber),
				Subtitle: e.Title + " · " + in.Date(e.AirDateUtc),
				URL:      in.Link("/calendar"),
			})
		}
	}
	return items, nil
}
