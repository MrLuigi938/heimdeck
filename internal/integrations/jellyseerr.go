package integrations

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// jellyseerr also covers Overseerr and Seerr, which share the same API.
type jellyseerr struct {
	mu     sync.Mutex
	titles map[string]string // "movie:123" -> title
}

func init() { register(&jellyseerr{titles: map[string]string{}}) }

func (*jellyseerr) Def() Definition {
	return Definition{
		ID: "jellyseerr", Name: "Jellyseerr / Overseerr", Icon: "jellyseerr", Category: "Medien",
		Description: "Offene Anfragen direkt genehmigen oder ablehnen, Filme & Serien suchen und anfragen",
		Fields: []Field{{Key: "apiKey", Label: "API-Key", Type: "password", Required: true, Secret: true,
			Help: "Einstellungen → Allgemein → API-Schlüssel"}},
		HasWidget: true, HasSearch: true, Images: []string{"jellyseerr", "overseerr", "seerr"}, Port: 5055,
	}
}

func (j *jellyseerr) call(ctx context.Context, in *Instance, method, path string, q url.Values, body, out any) error {
	return in.do(ctx, request{method: method, path: "/api/v1" + path, query: q, body: body,
		headers: map[string]string{"X-Api-Key": in.V("apiKey")}}, out)
}

func (j *jellyseerr) Test(ctx context.Context, in *Instance) error {
	return j.call(ctx, in, "GET", "/request/count", nil, nil, nil)
}

type seerrRequest struct {
	ID        int    `json:"id"`
	Type      string `json:"type"`
	CreatedAt string `json:"createdAt"`
	Is4k      bool   `json:"is4k"`
	Media     struct {
		TmdbID    int    `json:"tmdbId"`
		MediaType string `json:"mediaType"`
	} `json:"media"`
	RequestedBy struct {
		DisplayName string `json:"displayName"`
	} `json:"requestedBy"`
	Seasons []struct {
		SeasonNumber int `json:"seasonNumber"`
	} `json:"seasons"`
}

func (j *jellyseerr) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var counts struct {
		Pending    int `json:"pending"`
		Processing int `json:"processing"`
		Available  int `json:"available"`
	}
	if err := j.call(ctx, in, "GET", "/request/count", nil, nil, &counts); err != nil {
		return nil, err
	}
	var list struct {
		Results []seerrRequest `json:"results"`
	}
	q := url.Values{"take": {"10"}, "filter": {"pending"}, "sort": {"added"}}
	if err := j.call(ctx, in, "GET", "/request", q, nil, &list); err != nil {
		return nil, err
	}

	// Request lists contain only TMDB ids; look titles up in parallel.
	items := make([]Item, len(list.Results))
	var wg sync.WaitGroup
	for i, r := range list.Results {
		wg.Add(1)
		go func(i int, r seerrRequest) {
			defer wg.Done()
			title := j.title(ctx, in, r.Media.MediaType, r.Media.TmdbID)
			sub := r.RequestedBy.DisplayName
			if r.Media.MediaType == "tv" && len(r.Seasons) > 0 {
				sub += " · " + in.T("%d Staffel(n)", len(r.Seasons))
			}
			if t, err := time.Parse(time.RFC3339, r.CreatedAt); err == nil {
				sub += " · " + in.Since(t)
			}
			badge := in.T(map[string]string{"movie": "Film", "tv": "Serie"}[r.Media.MediaType])
			if r.Is4k {
				badge += " 4K"
			}
			id := strconv.Itoa(r.ID)
			items[i] = Item{
				Title: title, Subtitle: sub, Badge: badge,
				URL: in.Link(fmt.Sprintf("/%s/%d", r.Media.MediaType, r.Media.TmdbID)),
				Actions: []Action{
					{ID: "approve", Label: in.T("Genehmigen"), Tone: "primary", Params: map[string]string{"id": id}},
					{ID: "decline", Label: in.T("Ablehnen"), Tone: "danger", Params: map[string]string{"id": id}, Confirm: in.T("Anfrage für „%s“ ablehnen?", title)},
				},
			}
		}(i, r)
	}
	wg.Wait()

	return &Widget{
		Stats: []Stat{
			{Label: in.T("Offen"), Value: fmt.Sprint(counts.Pending), Tone: toneIf(counts.Pending > 0, "warn")},
			{Label: in.T("In Arbeit"), Value: fmt.Sprint(counts.Processing)},
			{Label: in.T("Verfügbar"), Value: fmt.Sprint(counts.Available)},
		},
		ItemsTitle: in.T("Offene Anfragen"),
		Items:      items,
	}, nil
}

func (j *jellyseerr) title(ctx context.Context, in *Instance, mediaType string, tmdbID int) string {
	key := fmt.Sprintf("%s:%d", mediaType, tmdbID)
	j.mu.Lock()
	t, ok := j.titles[key]
	j.mu.Unlock()
	if ok {
		return t
	}
	var res struct {
		Title       string `json:"title"`
		Name        string `json:"name"`
		ReleaseDate string `json:"releaseDate"`
		FirstAir    string `json:"firstAirDate"`
	}
	if err := j.call(ctx, in, "GET", fmt.Sprintf("/%s/%d", mediaType, tmdbID), url.Values{"language": {"de"}}, nil, &res); err != nil {
		return fmt.Sprintf("TMDB #%d", tmdbID)
	}
	t = res.Title + res.Name
	if d := res.ReleaseDate + res.FirstAir; len(d) >= 4 {
		t += " (" + d[:4] + ")"
	}
	j.mu.Lock()
	j.titles[key] = t
	j.mu.Unlock()
	return t
}

func (j *jellyseerr) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	switch action {
	case "approve", "decline":
		if _, err := strconv.Atoi(p["id"]); err != nil {
			return in.Errorf("ungültige Anfrage-ID")
		}
		return j.call(ctx, in, "POST", "/request/"+p["id"]+"/"+action, nil, nil, nil)
	case "request":
		id, err := strconv.Atoi(p["tmdbId"])
		if err != nil || (p["mediaType"] != "movie" && p["mediaType"] != "tv") {
			return in.Errorf("ungültiger Titel")
		}
		body := map[string]any{"mediaType": p["mediaType"], "mediaId": id}
		if p["mediaType"] == "tv" {
			body["seasons"] = "all"
		}
		return j.call(ctx, in, "POST", "/request", nil, body, nil)
	}
	return in.Errorf("unbekannte Aktion %q", action)
}

func (j *jellyseerr) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	var res struct {
		Results []struct {
			ID          int    `json:"id"`
			MediaType   string `json:"mediaType"`
			Title       string `json:"title"`
			Name        string `json:"name"`
			ReleaseDate string `json:"releaseDate"`
			FirstAir    string `json:"firstAirDate"`
			PosterPath  string `json:"posterPath"`
			MediaInfo   *struct {
				Status int `json:"status"`
			} `json:"mediaInfo"`
		} `json:"results"`
	}
	q := url.Values{"query": {query}, "page": {"1"}, "language": {"de"}}
	if err := j.call(ctx, in, "GET", "/search", q, nil, &res); err != nil {
		return nil, err
	}
	out := []Item{}
	for _, r := range res.Results {
		if r.MediaType != "movie" && r.MediaType != "tv" {
			continue
		}
		title := r.Title + r.Name
		sub := in.T(map[string]string{"movie": "Film", "tv": "Serie"}[r.MediaType])
		if d := r.ReleaseDate + r.FirstAir; len(d) >= 4 {
			sub += " · " + d[:4]
		}
		it := Item{Title: title, Subtitle: sub, URL: in.Link(fmt.Sprintf("/%s/%d", r.MediaType, r.ID))}
		if r.PosterPath != "" {
			it.Image = "https://image.tmdb.org/t/p/w92" + r.PosterPath
		}
		status := 0
		if r.MediaInfo != nil {
			status = r.MediaInfo.Status
		}
		switch status {
		case 5:
			it.Badge = in.T("Vorhanden")
		case 4:
			it.Badge = in.T("Teilweise")
		case 2, 3:
			it.Badge = in.T("Angefragt")
		default:
			it.Actions = []Action{{ID: "request", Label: in.T("Anfragen"), Tone: "primary",
				Params: map[string]string{"tmdbId": strconv.Itoa(r.ID), "mediaType": r.MediaType}}}
		}
		out = append(out, it)
		if len(out) == 8 {
			break
		}
	}
	return out, nil
}
