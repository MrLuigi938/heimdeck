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

// Readarr (retired in 2025) and its maintained revival Bookshelf share the
// *arr API v1 (github.com/Readarr/Readarr, src/Readarr.Api.V1).

type readarr struct{}

func init() { register(readarr{}) }

func (readarr) Def() Definition {
	return Definition{
		ID: "readarr", Name: "Readarr / Bookshelf", Icon: "readarr", Category: "Medien",
		Description: "Bücher: Download-Warteschlange, Neuerscheinungen, fehlende Bücher, Autorensuche",
		Fields:      []Field{apiKeyField}, HasWidget: true, HasSearch: true, Images: []string{"readarr", "bookshelf"}, Port: 8787,
	}
}

func (readarr) call(ctx context.Context, in *Instance, method, path string, q url.Values, body, out any) error {
	return in.do(ctx, request{method: method, path: "/api/v1" + path, query: q, body: body,
		headers: map[string]string{"X-Api-Key": in.V("apiKey")}}, out)
}

func (r readarr) Test(ctx context.Context, in *Instance) error {
	var st map[string]any
	return r.call(ctx, in, "GET", "/system/status", nil, nil, &st)
}

func (r readarr) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var queue struct {
		TotalRecords int `json:"totalRecords"`
		Records      []struct {
			Title    string  `json:"title"`
			Size     float64 `json:"size"`
			Sizeleft float64 `json:"sizeleft"`
			Status   string  `json:"status"`
			Timeleft string  `json:"timeleft"`
			Author   *struct {
				AuthorName string `json:"authorName"`
			} `json:"author"`
			Book *struct {
				Title string `json:"title"`
			} `json:"book"`
		} `json:"records"`
	}
	if err := r.call(ctx, in, "GET", "/queue", url.Values{"pageSize": {"20"}, "includeAuthor": {"true"}, "includeBook": {"true"}}, nil, &queue); err != nil {
		return nil, err
	}
	var missing struct {
		TotalRecords int `json:"totalRecords"`
	}
	r.call(ctx, in, "GET", "/wanted/missing", url.Values{"pageSize": {"1"}, "monitored": {"true"}}, nil, &missing)
	var health arrHealth
	r.call(ctx, in, "GET", "/health", nil, nil, &health)

	now := time.Now().UTC()
	var cal []struct {
		Title       string `json:"title"`
		ReleaseDate string `json:"releaseDate"`
		Author      struct {
			AuthorName string `json:"authorName"`
		} `json:"author"`
		Statistics struct {
			BookFileCount int `json:"bookFileCount"`
		} `json:"statistics"`
	}
	r.call(ctx, in, "GET", "/calendar", url.Values{"start": {now.Format(time.RFC3339)}, "end": {now.AddDate(0, 0, 30).Format(time.RFC3339)},
		"includeAuthor": {"true"}, "unmonitored": {"false"}}, nil, &cal)
	sort.Slice(cal, func(i, j int) bool { return cal[i].ReleaseDate < cal[j].ReleaseDate })

	w := &Widget{ItemsTitle: in.T("Warteschlange"), Stats: []Stat{
		{Label: in.T("In Warteschlange"), Value: in.Num(queue.TotalRecords)},
		{Label: in.T("Neue Bücher (30 Tage)"), Value: in.Num(len(cal))},
		{Label: in.T("Fehlende Bücher"), Value: in.Num(missing.TotalRecords), Tone: toneIf(missing.TotalRecords > 0, "warn")},
	}}
	for _, q := range queue.Records {
		title := q.Title
		if q.Author != nil && q.Book != nil {
			title = q.Author.AuthorName + " – " + q.Book.Title
		}
		it := Item{Title: title, Subtitle: in.Status(q.Status), URL: in.Link("/activity/queue")}
		if q.Timeleft != "" {
			it.Subtitle += " · " + in.T("noch %s", q.Timeleft)
		}
		if q.Size > 0 {
			it.Progress = pct((q.Size - q.Sizeleft) / q.Size * 100)
		}
		w.Items = append(w.Items, it)
		if len(w.Items) == 5 {
			break
		}
	}
	if len(w.Items) == 0 {
		w.ItemsTitle = in.T("Demnächst")
		for _, b := range cal {
			if b.Statistics.BookFileCount > 0 {
				continue
			}
			w.Items = append(w.Items, Item{Title: b.Author.AuthorName + " – " + b.Title, Subtitle: in.Date(b.ReleaseDate), URL: in.Link("/calendar")})
			if len(w.Items) == 5 {
				break
			}
		}
	}
	if lib, ok := r.library(ctx, in); ok {
		w.Stats = append(w.Stats, Stat{Label: in.T("Autoren"), Value: in.Num(lib.authors)}, Stat{Label: in.T("Bücher vorhanden"), Value: in.Num(lib.books)},
			Stat{Label: in.T("Auf der Platte"), Value: in.Bytes(lib.size)})
	}
	var roots []struct {
		FreeSpace float64 `json:"freeSpace"`
	}
	if r.call(ctx, in, "GET", "/rootfolder", nil, nil, &roots) == nil && len(roots) > 0 {
		free := roots[0].FreeSpace
		for _, x := range roots[1:] {
			free = min(free, x.FreeSpace)
		}
		w.Stats = append(w.Stats, Stat{Label: in.T("Freier Speicher"), Value: in.Bytes(free), Tone: toneIf(free < 50e9, "warn")})
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

type readarrLibrary struct {
	at             time.Time
	authors, books int
	size           float64
}

// readarrLibraries caches the library size per address for ten minutes.
var readarrLibraries sync.Map

func (r readarr) library(ctx context.Context, in *Instance) (readarrLibrary, bool) {
	if v, ok := readarrLibraries.Load(in.BaseURL); ok && time.Since(v.(readarrLibrary).at) < 10*time.Minute {
		return v.(readarrLibrary), true
	}
	var authors []struct {
		Statistics struct {
			BookFileCount int     `json:"bookFileCount"`
			SizeOnDisk    float64 `json:"sizeOnDisk"`
		} `json:"statistics"`
	}
	if r.call(ctx, in, "GET", "/author", nil, nil, &authors) != nil {
		return readarrLibrary{}, false
	}
	lib := readarrLibrary{at: time.Now(), authors: len(authors)}
	for _, a := range authors {
		lib.books += a.Statistics.BookFileCount
		lib.size += a.Statistics.SizeOnDisk
	}
	readarrLibraries.Store(in.BaseURL, lib)
	return lib, true
}

func (r readarr) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	var authors []struct {
		ID         int    `json:"id"`
		AuthorName string `json:"authorName"`
		TitleSlug  string `json:"titleSlug"`
		Monitored  bool   `json:"monitored"`
		Statistics struct {
			BookCount     int `json:"bookCount"`
			BookFileCount int `json:"bookFileCount"`
		} `json:"statistics"`
	}
	if err := r.call(ctx, in, "GET", "/author", nil, nil, &authors); err != nil {
		return nil, err
	}
	out := []Item{}
	for _, a := range authors {
		if !strings.Contains(strings.ToLower(a.AuthorName), strings.ToLower(query)) {
			continue
		}
		s := a.Statistics
		it := Item{Title: a.AuthorName, Subtitle: in.T("%d von %d Büchern", s.BookFileCount, s.BookCount), URL: in.Link("/author/" + a.TitleSlug)}
		if a.Monitored && s.BookFileCount < s.BookCount {
			it.Actions = []Action{{ID: "search", Label: in.T("Fehlende suchen"), Params: map[string]string{"author": strconv.Itoa(a.ID)}}}
		}
		out = append(out, it)
		if len(out) == 8 {
			break
		}
	}
	return out, nil
}

func (r readarr) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	id, err := strconv.Atoi(p["author"])
	if action != "search" || err != nil {
		return in.Errorf("unbekannte Aktion %q", action)
	}
	return r.call(ctx, in, "POST", "/command", nil, map[string]any{"name": "AuthorSearch", "authorId": id}, nil)
}
