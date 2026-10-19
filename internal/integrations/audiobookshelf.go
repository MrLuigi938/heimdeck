package integrations

import (
	"context"
	"fmt"
	"net/url"
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
			w.Items = append(w.Items, Item{Title: m.Title, Subtitle: firstNonEmpty(m.AuthorName, m.Author), URL: in.Link("/item/" + it.ID)})
		}
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
				Image: in.Link("/api/items/" + b.LibraryItem.ID + "/cover?width=120&format=jpeg")})
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
