package integrations

import (
	"context"
	"encoding/base64"
	"net/url"
	"regexp"
)

// Komga: REST API with X-API-Key (since 1.20) or HTTP Basic auth
// (github.com/gotson/komga, komga/docs/openapi.json).

type komga struct{}

func init() { register(komga{}) }

func (komga) Def() Definition {
	return Definition{
		ID: "komga", Name: "Komga", Icon: "komga", Category: "Medien",
		Description: "Comics und Mangas: Serien und Bücher, angefangene Bücher, Suche mit Covern",
		Fields: []Field{
			{Key: "apiKey", Label: "API-Key", Type: "password", Secret: true,
				Help: "Konto → API-Schlüssel (ab Komga 1.20). Alternativ Benutzer (E-Mail) und Passwort eintragen."},
			{Key: "username", Label: "Benutzername", Type: "text"},
			{Key: "password", Label: "Passwort", Type: "password", Secret: true},
		},
		HasWidget: true, HasSearch: true, Images: []string{"komga"}, Port: 25600,
	}
}

func (komga) auth(in *Instance) map[string]string {
	if in.V("apiKey") != "" {
		return map[string]string{"X-API-Key": in.V("apiKey")}
	}
	return map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(in.V("username")+":"+in.Values["password"]))}
}

func (k komga) call(ctx context.Context, in *Instance, method, path string, q url.Values, body, out any) error {
	if in.V("apiKey") == "" && in.V("username") == "" {
		return in.Errorf("Bitte einen API-Token oder Benutzername und Passwort eintragen")
	}
	return in.do(ctx, request{method: method, path: "/api/v1" + path, query: q, body: body, headers: k.auth(in)}, out)
}

type komgaPage[T any] struct {
	TotalElements int `json:"totalElements"`
	Content       []T `json:"content"`
}

type komgaBook struct {
	ID          string `json:"id"`
	SeriesID    string `json:"seriesId"`
	SeriesTitle string `json:"seriesTitle"`
	Metadata    struct {
		Title string `json:"title"`
	} `json:"metadata"`
	Media struct {
		PagesCount int `json:"pagesCount"`
	} `json:"media"`
	ReadProgress *struct {
		Page int `json:"page"`
	} `json:"readProgress"`
}

type komgaSeries struct {
	ID         string `json:"id"`
	BooksCount int    `json:"booksCount"`
	Metadata   struct {
		Title string `json:"title"`
	} `json:"metadata"`
}

func (k komga) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var series komgaPage[komgaSeries]
	if err := k.call(ctx, in, "POST", "/series/list", url.Values{"size": {"1"}}, map[string]any{}, &series); err != nil {
		return nil, err
	}
	var books komgaPage[komgaBook]
	k.call(ctx, in, "POST", "/books/list", url.Values{"size": {"1"}}, map[string]any{}, &books)
	var reading komgaPage[komgaBook]
	k.call(ctx, in, "POST", "/books/list", url.Values{"size": {"5"}, "sort": {"readProgress.readDate,desc"}},
		map[string]any{"condition": map[string]any{"readStatus": map[string]string{"operator": "is", "value": "IN_PROGRESS"}}}, &reading)

	status := func(s string) int {
		var p komgaPage[komgaBook]
		k.call(ctx, in, "POST", "/books/list", url.Values{"size": {"1"}},
			map[string]any{"condition": map[string]any{"readStatus": map[string]string{"operator": "is", "value": s}}}, &p)
		return p.TotalElements
	}
	var libs []struct{}
	k.call(ctx, in, "GET", "/libraries", nil, nil, &libs)

	w := &Widget{Stats: []Stat{
		{Label: in.T("Serien"), Value: in.Num(series.TotalElements)},
		{Label: in.T("Bücher"), Value: in.Num(books.TotalElements)},
		{Label: in.T("Angefangen"), Value: in.Num(reading.TotalElements)},
		{Label: in.T("Gelesen"), Value: in.Num(status("READ"))},
		{Label: in.T("Ungelesen"), Value: in.Num(status("UNREAD"))},
	}}
	if len(libs) > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Bibliotheken"), Value: in.Num(len(libs))})
	}
	book := func(b komgaBook) Item {
		it := Item{Title: b.SeriesTitle, Subtitle: b.Metadata.Title, URL: in.Link("/book/" + b.ID), Image: in.Proxy("/api/v1/books/" + b.ID + "/thumbnail")}
		if b.ReadProgress != nil && b.Media.PagesCount > 0 {
			it.Progress = pct(float64(b.ReadProgress.Page) / float64(b.Media.PagesCount) * 100)
		}
		return it
	}
	if len(reading.Content) > 0 {
		w.ItemsTitle = in.T("Weiterlesen")
		for _, b := range reading.Content {
			w.Items = append(w.Items, book(b))
		}
		return w, nil
	}
	// Nothing started: the newest books.
	var recent komgaPage[komgaBook]
	if k.call(ctx, in, "POST", "/books/list", url.Values{"size": {"5"}, "sort": {"createdDate,desc"}}, map[string]any{}, &recent) == nil && len(recent.Content) > 0 {
		w.ItemsTitle = in.T("Zuletzt hinzugefügt")
		for _, b := range recent.Content {
			w.Items = append(w.Items, book(b))
		}
	}
	return w, nil
}

func (k komga) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	var series komgaPage[komgaSeries]
	if err := k.call(ctx, in, "POST", "/series/list", url.Values{"size": {"5"}}, map[string]any{"fullTextSearch": query}, &series); err != nil {
		return nil, err
	}
	out := []Item{}
	for _, s := range series.Content {
		out = append(out, Item{Title: s.Metadata.Title, Subtitle: in.T("Serie") + " · " + in.T("%d Bücher", s.BooksCount),
			URL: in.Link("/series/" + s.ID), Image: in.Proxy("/api/v1/series/" + s.ID + "/thumbnail")})
	}
	var books komgaPage[komgaBook]
	if err := k.call(ctx, in, "POST", "/books/list", url.Values{"size": {"4"}}, map[string]any{"fullTextSearch": query}, &books); err == nil {
		for _, b := range books.Content {
			out = append(out, Item{Title: b.Metadata.Title, Subtitle: b.SeriesTitle,
				URL: in.Link("/book/" + b.ID), Image: in.Proxy("/api/v1/books/" + b.ID + "/thumbnail")})
		}
	}
	return out, nil
}

var komgaImageRe = regexp.MustCompile(`^/api/v1/(series|books)/[0-9A-Za-z]{1,40}/thumbnail$`)

func (k komga) ImageRequest(ctx context.Context, in *Instance, path string) (string, map[string]string, error) {
	if !komgaImageRe.MatchString(path) {
		return "", nil, in.Errorf("nicht gefunden")
	}
	return path, k.auth(in), nil
}
