package integrations

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// Audiobookshelf: REST API with an API key as Bearer token
// (github.com/advplyr/audiobookshelf, server/routers/ApiRouter.js).

type audiobookshelf struct{}

func init() { register(audiobookshelf{}) }

func (audiobookshelf) Def() Definition {
	return Definition{
		ID: "audiobookshelf", Name: "Audiobookshelf", Icon: "audiobookshelf", Category: "Medien",
		Description: "Hörbücher und Podcasts: Bibliotheksgröße, Gesamtdauer, angefangene Titel, Suche nach Titeln, Autoren und Serien",
		Fields: []Field{{Key: "apiKey", Label: "API-Key", Type: "password", Required: true, Secret: true,
			Help: "Einstellungen → API-Schlüssel → Hinzufügen (ab Version 2.26). Ältere Versionen: Einstellungen → Benutzer → dein Benutzer → API-Token."}},
		HasWidget: true, HasSearch: true, Images: []string{"audiobookshelf"}, Port: 80,
	}
}

func (audiobookshelf) get(ctx context.Context, in *Instance, path string, q url.Values, out any) error {
	return in.do(ctx, request{path: path, query: q, headers: map[string]string{"Authorization": "Bearer " + in.V("apiKey")}}, out)
}

type absLibrary struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
	Stats     *struct {
		TotalItems    int     `json:"totalItems"`
		TotalSize     float64 `json:"totalSize"`
		TotalDuration float64 `json:"totalDuration"`
	} `json:"stats"`
}

type absMetadata struct {
	Title      string `json:"title"`
	AuthorName string `json:"authorName"`
	Author     string `json:"author"` // podcasts
	SeriesName string `json:"seriesName"`
}

func (a audiobookshelf) libraries(ctx context.Context, in *Instance, stats bool) ([]absLibrary, error) {
	var res struct {
		Libraries []absLibrary `json:"libraries"`
	}
	q := url.Values{}
	if stats {
		q.Set("include", "stats")
	}
	err := a.get(ctx, in, "/api/libraries", q, &res)
	return res.Libraries, err
}

func (a audiobookshelf) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	libs, err := a.libraries(ctx, in, true)
	if err != nil {
		return nil, err
	}
	books, podcasts := 0, 0
	var size, dur float64
	for _, l := range libs {
		if l.Stats == nil {
			continue
		}
		if l.MediaType == "podcast" {
			podcasts += l.Stats.TotalItems
		} else {
			books += l.Stats.TotalItems
		}
		size += l.Stats.TotalSize
		dur += l.Stats.TotalDuration
	}
	w := &Widget{Stats: []Stat{{Label: in.T("Hörbücher"), Value: in.Num(books)}}}
	if podcasts > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Podcasts"), Value: in.Num(podcasts)})
	}
	w.Stats = append(w.Stats,
		Stat{Label: in.T("Stunden"), Value: in.Num(int(dur / 3600))},
		Stat{Label: in.T("Größe"), Value: in.Bytes(size)},
	)
	// Authors per book library (the detailed statistics of each library).
	authors := 0
	for _, l := range libs {
		if l.MediaType == "podcast" {
			continue
		}
		var st struct {
			TotalAuthors int `json:"totalAuthors"`
		}
		if a.get(ctx, in, "/api/libraries/"+l.ID+"/stats", nil, &st) == nil {
			authors += st.TotalAuthors
		}
	}
	if authors > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Autoren"), Value: in.Num(authors)})
	}
	// Your own listening time.
	var ls struct {
		Today     float64 `json:"today"`     // seconds
		TotalTime float64 `json:"totalTime"` // seconds
	}
	if a.get(ctx, in, "/api/me/listening-stats", nil, &ls) == nil && ls.TotalTime > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Heute gehört"), Value: in.T("%d Min.", int(ls.Today/60))},
			Stat{Label: in.T("Insgesamt gehört"), Value: in.T("%d Std.", int(ls.TotalTime/3600))})
	}
	cover := func(id string) string { return in.Proxy("/api/items/" + id + "/cover?width=120&format=jpeg") }

	// Who is listening right now (needs an admin key; otherwise left out).
	var online struct {
		UsersOnline []struct {
			Username string `json:"username"`
			Session  *struct {
				LibraryItemID string  `json:"libraryItemId"`
				DisplayTitle  string  `json:"displayTitle"`
				DisplayAuthor string  `json:"displayAuthor"`
				CurrentTime   float64 `json:"currentTime"`
				Duration      float64 `json:"duration"`
			} `json:"session"`
		} `json:"usersOnline"`
	}
	if a.get(ctx, in, "/api/users/online", nil, &online) == nil {
		listening := 0
		for _, u := range online.UsersOnline {
			s := u.Session
			if s == nil {
				continue
			}
			listening++
			it := Item{Title: s.DisplayTitle, Subtitle: strings.Trim(s.DisplayAuthor+" · "+u.Username, " ·"), URL: in.Link("/item/" + s.LibraryItemID), Image: cover(s.LibraryItemID)}
			if s.Duration > 0 {
				it.Progress = pct(s.CurrentTime / s.Duration * 100)
			}
			w.Items = append(w.Items, it)
		}
		w.Stats = append([]Stat{{Label: in.T("Hören gerade"), Value: in.Num(listening)}}, w.Stats...)
		if listening > 0 {
			w.ItemsTitle = in.T("Läuft gerade")
			return w, nil
		}
	}

	var prog struct {
		LibraryItems []struct {
			ID    string `json:"id"`
			Media struct {
				Metadata absMetadata `json:"metadata"`
			} `json:"media"`
		} `json:"libraryItems"`
	}
	if err := a.get(ctx, in, "/api/me/items-in-progress", url.Values{"limit": {"5"}}, &prog); err == nil && len(prog.LibraryItems) > 0 {
		w.ItemsTitle = in.T("Weiterhören")
		for _, it := range prog.LibraryItems {
			m := it.Media.Metadata
			w.Items = append(w.Items, Item{Title: m.Title, Subtitle: firstNonEmpty(m.AuthorName, m.Author), URL: in.Link("/item/" + it.ID), Image: cover(it.ID)})
		}
		return w, nil
	}
	// Nothing started: what was added last, from the first book library.
	for _, l := range libs {
		if l.MediaType == "podcast" {
			continue
		}
		var res struct {
			Results []struct {
				ID    string `json:"id"`
				Media struct {
					Metadata absMetadata `json:"metadata"`
				} `json:"media"`
			} `json:"results"`
		}
		q := url.Values{"sort": {"addedAt"}, "desc": {"1"}, "limit": {"5"}}
		if a.get(ctx, in, "/api/libraries/"+l.ID+"/items", q, &res) == nil && len(res.Results) > 0 {
			w.ItemsTitle = in.T("Zuletzt hinzugefügt")
			for _, it := range res.Results {
				m := it.Media.Metadata
				w.Items = append(w.Items, Item{Title: m.Title, Subtitle: firstNonEmpty(m.AuthorName, m.Author), URL: in.Link("/item/" + it.ID), Image: cover(it.ID)})
			}
		}
		break
	}
	return w, nil
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

type absItem struct {
	LibraryItem struct {
		ID    string `json:"id"`
		Media struct {
			Metadata absMetadata `json:"metadata"`
		} `json:"media"`
	} `json:"libraryItem"`
}

func (a audiobookshelf) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	libs, err := a.libraries(ctx, in, false)
	if err != nil {
		return nil, err
	}
	out := []Item{}
	for _, l := range libs {
		var res struct {
			Book    []absItem `json:"book"`
			Podcast []absItem `json:"podcast"`
			Authors []struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				NumBooks int    `json:"numBooks"`
			} `json:"authors"`
			Series []struct {
				Series struct {
					ID   string `json:"id"`
					Name string `json:"name"`
				} `json:"series"`
				Books []any `json:"books"`
			} `json:"series"`
		}
		if err := a.get(ctx, in, "/api/libraries/"+l.ID+"/search", url.Values{"q": {query}, "limit": {"6"}}, &res); err != nil {
			return out, err
		}
		kind := in.T("Hörbuch")
		items := res.Book
		if l.MediaType == "podcast" {
			kind, items = in.T("Podcast"), res.Podcast
		}
		for _, b := range items {
			m := b.LibraryItem.Media.Metadata
			sub := []string{kind}
			if author := firstNonEmpty(m.AuthorName, m.Author); author != "" {
				sub = append(sub, author)
			}
			if m.SeriesName != "" {
				sub = append(sub, m.SeriesName)
			}
			// Covers need no login in Audiobookshelf.
			out = append(out, Item{Title: m.Title, Subtitle: strings.Join(sub, " · "), URL: in.Link("/item/" + b.LibraryItem.ID),
				Image: in.Proxy("/api/items/" + b.LibraryItem.ID + "/cover?width=120&format=jpeg")})
		}
		for _, au := range res.Authors {
			out = append(out, Item{Title: au.Name, Subtitle: in.T("Autor") + " · " + in.T("%d Bücher", au.NumBooks),
				URL: in.Link("/author/" + au.ID)})
		}
		for _, s := range res.Series {
			out = append(out, Item{Title: s.Series.Name, Subtitle: in.T("Serie") + " · " + in.T("%d Bücher", len(s.Books)),
				URL: in.Link(fmt.Sprintf("/library/%s/series/%s", l.ID, s.Series.ID))})
		}
		if len(out) >= 8 {
			return out[:8], nil
		}
	}
	return out, nil
}

var absCoverRe = regexp.MustCompile(`^/api/items/[A-Za-z0-9_-]+/cover\?width=\d+&format=jpeg$`)

// ImageRequest loads covers through Heimdeck (with the API key, and from https dashboards too).
func (audiobookshelf) ImageRequest(ctx context.Context, in *Instance, path string) (string, map[string]string, error) {
	if !absCoverRe.MatchString(path) {
		return "", nil, in.Errorf("nicht gefunden")
	}
	return path, map[string]string{"Authorization": "Bearer " + in.V("apiKey")}, nil
}
