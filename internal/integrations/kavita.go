package integrations

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// Kavita: REST API (github.com/Kareadita/Kavita, Kavita.Server/Controllers).
// Since 0.8.9 the API key works directly as x-api-key header; older versions
// exchange it for a JWT via /api/Plugin/authenticate, which is the fallback.

type kavita struct{}

func init() { register(kavita{}) }

func (kavita) Def() Definition {
	return Definition{
		ID: "kavita", Name: "Kavita", Icon: "kavita", Category: "Medien",
		Description: "Comics, Mangas und E-Books: Serien, Bände, Weiterlesen, Suche mit Covern",
		Fields: []Field{{Key: "apiKey", Label: "API-Key", Type: "password", Required: true, Secret: true,
			Help: "Benutzereinstellungen → 3rd Party Clients bzw. API-Schlüssel"}},
		HasWidget: true, HasSearch: true, Images: []string{"kavita"}, Port: 5000,
	}
}

type kavitaToken struct {
	token string
	until time.Time
}

var kavitaTokens sync.Map // BaseURL+key -> kavitaToken

func (k kavita) call(ctx context.Context, in *Instance, method, path string, q url.Values, body, out any) error {
	cacheKey := in.BaseURL + "\x00" + in.V("apiKey")
	headers := map[string]string{"x-api-key": in.V("apiKey")}
	if t, ok := kavitaTokens.Load(cacheKey); ok && time.Now().Before(t.(kavitaToken).until) {
		headers = map[string]string{"Authorization": "Bearer " + t.(kavitaToken).token}
	}
	err := in.do(ctx, request{method: method, path: path, query: q, body: body, headers: headers}, out)
	var he *HTTPError
	if !errors.As(err, &he) || he.Status != 401 || headers["Authorization"] != "" {
		return err
	}
	var user struct {
		Token string `json:"token"`
	}
	if err := in.do(ctx, request{method: "POST", path: "/api/Plugin/authenticate",
		query: url.Values{"apiKey": {in.V("apiKey")}, "pluginName": {"Heimdeck"}}}, &user); err != nil {
		return err
	}
	kavitaTokens.Store(cacheKey, kavitaToken{user.Token, time.Now().Add(30 * time.Minute)})
	return in.do(ctx, request{method: method, path: path, query: q, body: body,
		headers: map[string]string{"Authorization": "Bearer " + user.Token}}, out)
}

type kavitaSeries struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	LibraryID   int    `json:"libraryId"`
	LibraryName string `json:"libraryName"`
	Pages       int    `json:"pages"`
	PagesRead   int    `json:"pagesRead"`
}

func (k kavita) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	w := &Widget{}
	var st struct {
		SeriesCount int     `json:"seriesCount"`
		VolumeCount int     `json:"volumeCount"`
		TotalFiles  int     `json:"totalFiles"`
		TotalSize   float64 `json:"totalSize"`
	}
	err := k.call(ctx, in, "GET", "/api/Stats/server/stats", nil, nil, &st)
	var he *HTTPError
	if errors.As(err, &he) && (he.Status == 401 || he.Status == 403) {
		// Not an admin: count the libraries instead.
		var libs []struct {
			ID int `json:"id"`
		}
		if err := k.call(ctx, in, "GET", "/api/Library/libraries", nil, nil, &libs); err != nil {
			return nil, err
		}
		w.Stats = []Stat{{Label: in.T("Bibliotheken"), Value: in.Num(len(libs))}}
	} else if err != nil {
		return nil, err
	} else {
		w.Stats = []Stat{
			{Label: in.T("Serien"), Value: in.Num(st.SeriesCount)},
			{Label: in.T("Bände"), Value: in.Num(st.VolumeCount)},
			{Label: in.T("Dateien"), Value: in.Num(st.TotalFiles)},
			{Label: in.T("Größe"), Value: in.Bytes(st.TotalSize)},
		}
	}

	var deck []kavitaSeries
	q := url.Values{"libraryId": {"0"}, "PageNumber": {"1"}, "PageSize": {"5"}}
	if err := k.call(ctx, in, "POST", "/api/Series/on-deck", q, map[string]any{}, &deck); err == nil && len(deck) > 0 {
		w.ItemsTitle = in.T("Weiterlesen")
		for _, s := range deck {
			it := Item{Title: s.Name, Subtitle: s.LibraryName, URL: in.Link(fmt.Sprintf("/library/%d/series/%d", s.LibraryID, s.ID))}
			if s.Pages > 0 {
				it.Progress = pct(float64(s.PagesRead) / float64(s.Pages) * 100)
			}
			w.Items = append(w.Items, it)
		}
	}
	return w, nil
}

func (k kavita) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	var res struct {
		Series []struct {
			SeriesID    int    `json:"seriesId"`
			Name        string `json:"name"`
			LibraryID   int    `json:"libraryId"`
			LibraryName string `json:"libraryName"`
			Format      int    `json:"format"`
		} `json:"series"`
	}
	q := url.Values{"queryString": {query}, "includeChapterAndFiles": {"false"}}
	if err := k.call(ctx, in, "GET", "/api/Search/search", q, nil, &res); err != nil {
		return nil, err
	}
	out := []Item{}
	for _, s := range res.Series {
		out = append(out, Item{Title: s.Name, Subtitle: s.LibraryName,
			URL:   in.Link(fmt.Sprintf("/library/%d/series/%d", s.LibraryID, s.SeriesID)),
			Image: in.Proxy("/api/image/series-cover?seriesId=" + strconv.Itoa(s.SeriesID))})
		if len(out) == 8 {
			break
		}
	}
	return out, nil
}

var kavitaImageRe = regexp.MustCompile(`^/api/image/series-cover\?seriesId=\d+$`)

// The cover endpoint takes the key as query parameter in every version, so it
// also works before 0.8.9.
func (kavita) ImageRequest(ctx context.Context, in *Instance, path string) (string, map[string]string, error) {
	if !kavitaImageRe.MatchString(path) {
		return "", nil, in.Errorf("nicht gefunden")
	}
	return path + "&apiKey=" + url.QueryEscape(in.V("apiKey")), nil, nil
}
