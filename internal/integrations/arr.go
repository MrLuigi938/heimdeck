package integrations

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"
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
		Description: "Serien: Download-Warteschlange, kommende Folgen, Warnungen",
		Fields:      []Field{apiKeyField}, HasWidget: true, Images: []string{"sonarr"}, Port: 8989,
	}})
	register(&arr{movie: true, def: Definition{
		ID: "radarr", Name: "Radarr", Icon: "radarr", Category: "Medien",
		Description: "Filme: Download-Warteschlange, kommende Veröffentlichungen, Warnungen",
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
		return fmt.Errorf("unter dieser Adresse läuft %s, nicht %s", status.AppName, a.def.Name)
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

	w := &Widget{ItemsTitle: "Warteschlange"}
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
		sub := []string{statusDE(r.Status)}
		if r.Timeleft != "" {
			sub = append(sub, "noch "+r.Timeleft)
		}
		it.Subtitle = strings.Join(sub, " · ")
		if r.State == "importPending" || r.State == "failedPending" || r.Status == "warning" {
			it.Badge = "Prüfen"
		}
		w.Items = append(w.Items, it)
		if len(w.Items) == 5 {
			break
		}
	}

	upcoming, err := a.upcoming(ctx, in)
	if err == nil && len(upcoming) > 0 {
		label := "Neue Folgen (7 Tage)"
		if a.movie {
			label = "Releases (30 Tage)"
		}
		w.Stats = append(w.Stats, Stat{Label: label, Value: fmt.Sprint(len(upcoming))})
	}
	w.Stats = append([]Stat{{Label: "In Warteschlange", Value: fmt.Sprint(queue.TotalRecords)}}, w.Stats...)

	if len(w.Items) == 0 && len(upcoming) > 0 {
		w.ItemsTitle = "Demnächst"
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
		w.Stats = append(w.Stats, Stat{Label: "Warnungen", Value: fmt.Sprint(len(health)), Tone: map[bool]string{true: "bad", false: "warn"}[errs > 0]})
	}
	return w, nil
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
				kind, date = "Kino", m.InCinemas
			}
			items = append(items, Item{Title: m.Title, Subtitle: kind + " · " + shortDate(date), URL: in.Link("/calendar")})
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
				Subtitle: e.Title + " · " + shortDate(e.AirDateUtc),
				URL:      in.Link("/calendar"),
			})
		}
	}
	return items, nil
}

func shortDate(iso string) string {
	t, err := time.Parse(time.RFC3339, iso)
	if err != nil {
		return iso
	}
	t = t.Local()
	days := []string{"So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"}
	if t.Hour() == 0 && t.Minute() == 0 {
		return fmt.Sprintf("%s %02d.%02d.", days[t.Weekday()], t.Day(), t.Month())
	}
	return fmt.Sprintf("%s %02d.%02d. %02d:%02d", days[t.Weekday()], t.Day(), t.Month(), t.Hour(), t.Minute())
}

func statusDE(s string) string {
	switch strings.ToLower(s) {
	case "downloading":
		return "Lädt"
	case "queued":
		return "Wartet"
	case "paused":
		return "Pausiert"
	case "completed":
		return "Fertig"
	case "delay":
		return "Verzögert"
	case "warning":
		return "Warnung"
	case "failed":
		return "Fehlgeschlagen"
	}
	return s
}
