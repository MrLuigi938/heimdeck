package integrations

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Lidarr: the *arr API, version 1 (github.com/Lidarr/Lidarr, src/Lidarr.Api.V1).

type lidarr struct{}

func init() { register(lidarr{}) }

func (lidarr) Def() Definition {
	return Definition{
		ID: "lidarr", Name: "Lidarr", Icon: "lidarr", Category: "Medien",
		Description: "Musik: Download-Warteschlange, neue Alben, fehlende Alben, Künstlersuche",
		Fields:      []Field{apiKeyField}, HasWidget: true, HasSearch: true, Images: []string{"lidarr"}, Port: 8686,
	}
}

func (lidarr) call(ctx context.Context, in *Instance, method, path string, q url.Values, body, out any) error {
	return in.do(ctx, request{method: method, path: "/api/v1" + path, query: q, body: body,
		headers: map[string]string{"X-Api-Key": in.V("apiKey")}}, out)
}

func (l lidarr) Test(ctx context.Context, in *Instance) error {
	var st struct {
		AppName string `json:"appName"`
	}
	if err := l.call(ctx, in, "GET", "/system/status", nil, nil, &st); err != nil {
		return err
	}
	if st.AppName != "" && !strings.EqualFold(st.AppName, "Lidarr") {
		return in.Errorf("unter dieser Adresse läuft %s, nicht %s", st.AppName, "Lidarr")
	}
	return nil
}

type lidarrArtist struct {
	ID              int    `json:"id"`
	ArtistName      string `json:"artistName"`
	ForeignArtistID string `json:"foreignArtistId"`
	Monitored       bool   `json:"monitored"`
	Statistics      struct {
		AlbumCount      int     `json:"albumCount"`
		TrackFileCount  int     `json:"trackFileCount"`
		TotalTrackCount int     `json:"totalTrackCount"`
		SizeOnDisk      float64 `json:"sizeOnDisk"`
	} `json:"statistics"`
}

func (l lidarr) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var queue struct {
		TotalRecords int `json:"totalRecords"`
		Records      []struct {
			Title    string  `json:"title"`
			Size     float64 `json:"size"`
			Sizeleft float64 `json:"sizeleft"`
			Status   string  `json:"status"`
			Timeleft string  `json:"timeleft"`
			State    string  `json:"trackedDownloadState"`
			Artist   *struct {
				ArtistName string `json:"artistName"`
			} `json:"artist"`
			Album *struct {
				Title string `json:"title"`
			} `json:"album"`
		} `json:"records"`
	}
	q := url.Values{"pageSize": {"20"}, "includeArtist": {"true"}, "includeAlbum": {"true"}}
	if err := l.call(ctx, in, "GET", "/queue", q, nil, &queue); err != nil {
		return nil, err
	}
	var missing struct {
		TotalRecords int `json:"totalRecords"`
	}
	l.call(ctx, in, "GET", "/wanted/missing", url.Values{"pageSize": {"1"}, "monitored": {"true"}}, nil, &missing)
	var health arrHealth
	l.call(ctx, in, "GET", "/health", nil, nil, &health)

	w := &Widget{ItemsTitle: in.T("Warteschlange")}
	for _, r := range queue.Records {
		title := r.Title
		if r.Artist != nil && r.Album != nil {
			title = r.Artist.ArtistName + " – " + r.Album.Title
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

	upcoming := l.upcoming(ctx, in)
	w.Stats = []Stat{
		{Label: in.T("In Warteschlange"), Value: in.Num(queue.TotalRecords)},
		{Label: in.T("Neue Alben (30 Tage)"), Value: in.Num(len(upcoming))},
		{Label: in.T("Fehlende Alben"), Value: in.Num(missing.TotalRecords), Tone: toneIf(missing.TotalRecords > 0, "warn")},
	}
	var cutoff struct {
		TotalRecords int `json:"totalRecords"`
	}
	if l.call(ctx, in, "GET", "/wanted/cutoff", url.Values{"pageSize": {"1"}, "monitored": {"true"}}, nil, &cutoff) == nil && cutoff.TotalRecords > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Qualität verbesserbar"), Value: in.Num(cutoff.TotalRecords)})
	}
	if lib, ok := l.library(ctx, in); ok {
		w.Stats = append(w.Stats, Stat{Label: in.T("Künstler"), Value: in.Num(lib.artists)}, Stat{Label: in.T("Alben"), Value: in.Num(lib.albums)},
			Stat{Label: in.T("Titel"), Value: in.Num(lib.tracks)}, Stat{Label: in.T("Auf der Platte"), Value: in.Bytes(lib.size)})
	}
	var roots []struct {
		FreeSpace float64 `json:"freeSpace"`
	}
	if l.call(ctx, in, "GET", "/rootfolder", nil, nil, &roots) == nil && len(roots) > 0 {
		free := roots[0].FreeSpace
		for _, r := range roots[1:] {
			free = min(free, r.FreeSpace)
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
		w.Stats = append(w.Stats, Stat{Label: in.T("Warnungen"), Value: in.Num(len(health)), Tone: map[bool]string{true: "bad", false: "warn"}[errs > 0]})
	}
	return w, nil
}

type lidarrLibrary struct {
	at                      time.Time
	artists, albums, tracks int
	size                    float64
}

// lidarrLibraries caches the library size per address for ten minutes, like
// the one of Sonarr and Radarr: the artist list can be large.
var lidarrLibraries sync.Map

func (l lidarr) library(ctx context.Context, in *Instance) (lidarrLibrary, bool) {
	if v, ok := lidarrLibraries.Load(in.BaseURL); ok && time.Since(v.(lidarrLibrary).at) < 10*time.Minute {
		return v.(lidarrLibrary), true
	}
	var artists []lidarrArtist
	if l.call(ctx, in, "GET", "/artist", nil, nil, &artists) != nil {
		return lidarrLibrary{}, false
	}
	lib := lidarrLibrary{at: time.Now(), artists: len(artists)}
	for _, a := range artists {
		lib.albums += a.Statistics.AlbumCount
		lib.tracks += a.Statistics.TrackFileCount
		lib.size += a.Statistics.SizeOnDisk
	}
	lidarrLibraries.Store(in.BaseURL, lib)
	return lib, true
}

func (l lidarr) upcoming(ctx context.Context, in *Instance) []Item {
	now := time.Now().UTC()
	q := url.Values{
		"start":         {now.Format(time.RFC3339)},
		"end":           {now.AddDate(0, 0, 30).Format(time.RFC3339)},
		"includeArtist": {"true"},
		"unmonitored":   {"false"},
	}
	var albums []struct {
		Title       string `json:"title"`
		ReleaseDate string `json:"releaseDate"`
		Artist      struct {
			ArtistName string `json:"artistName"`
		} `json:"artist"`
		Statistics struct {
			TrackFileCount int `json:"trackFileCount"`
		} `json:"statistics"`
	}
	if err := l.call(ctx, in, "GET", "/calendar", q, nil, &albums); err != nil {
		return nil
	}
	sort.Slice(albums, func(i, j int) bool { return albums[i].ReleaseDate < albums[j].ReleaseDate })
	var items []Item
	for _, a := range albums {
		if a.Statistics.TrackFileCount > 0 {
			continue
		}
		items = append(items, Item{Title: a.Artist.ArtistName + " – " + a.Title, Subtitle: in.Date(a.ReleaseDate), URL: in.Link("/calendar")})
	}
	return items
}

// Search looks through the artists in the library and offers a search for missing albums.
func (l lidarr) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	var artists []lidarrArtist
	if err := l.call(ctx, in, "GET", "/artist", nil, nil, &artists); err != nil {
		return nil, err
	}
	query = strings.ToLower(query)
	out := []Item{}
	for _, a := range artists {
		if !strings.Contains(strings.ToLower(a.ArtistName), query) {
			continue
		}
		s := a.Statistics
		it := Item{
			Title:    a.ArtistName,
			Subtitle: in.T("%d Alben", s.AlbumCount) + " · " + in.T("%d von %d Titeln", s.TrackFileCount, s.TotalTrackCount),
			URL:      in.Link("/artist/" + a.ForeignArtistID),
		}
		if s.TotalTrackCount > 0 && s.TrackFileCount < s.TotalTrackCount && a.Monitored {
			it.Actions = []Action{{ID: "search", Label: in.T("Fehlende suchen"), Params: map[string]string{"artist": strconv.Itoa(a.ID)}}}
		}
		out = append(out, it)
		if len(out) == 8 {
			break
		}
	}
	return out, nil
}

func (l lidarr) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	if action != "search" {
		return in.Errorf("unbekannte Aktion %q", action)
	}
	id, err := strconv.Atoi(p["artist"])
	if err != nil {
		return in.Errorf("ungültige ID")
	}
	return l.call(ctx, in, "POST", "/command", nil, map[string]any{"name": "ArtistSearch", "artistId": id}, nil)
}
