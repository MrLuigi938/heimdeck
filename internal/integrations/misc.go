package integrations

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
)

// ---- Paperless-ngx ----

type paperless struct{}

func init() { register(paperless{}) }

func (paperless) Def() Definition {
	return Definition{
		ID: "paperless", Name: "Paperless-ngx", Icon: "paperless-ngx", Category: "Dokumente",
		Description: "Anzahl Dokumente, Posteingang, Volltextsuche",
		Fields: []Field{{Key: "apiKey", Label: "API-Token", Type: "password", Required: true, Secret: true,
			Help: "Profil (oben rechts) → API-Auth-Token"}},
		HasWidget: true, HasSearch: true, Images: []string{"paperless"}, Port: 8000,
	}
}

func (paperless) get(ctx context.Context, in *Instance, path string, q url.Values, out any) error {
	return in.do(ctx, request{path: path, query: q, headers: map[string]string{"Authorization": "Token " + in.V("apiKey")}}, out)
}

func (p paperless) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var st struct {
		Total int `json:"documents_total"`
		Inbox int `json:"documents_inbox"`
	}
	if err := p.get(ctx, in, "/api/statistics/", nil, &st); err != nil {
		return nil, err
	}
	return &Widget{Stats: []Stat{
		{Label: "Dokumente", Value: num(st.Total)},
		{Label: "Posteingang", Value: num(st.Inbox), Tone: toneIf(st.Inbox > 0, "warn")},
	}}, nil
}

func (p paperless) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	var res struct {
		Results []struct {
			ID      int    `json:"id"`
			Title   string `json:"title"`
			Created string `json:"created"`
		} `json:"results"`
	}
	// Full-text search needs Paperless' search index; if it finds nothing (or the
	// index is missing), fall back to a plain title/content filter.
	err := p.get(ctx, in, "/api/documents/", url.Values{"query": {query}, "page_size": {"8"}}, &res)
	if err != nil || len(res.Results) == 0 {
		res.Results = nil
		if err2 := p.get(ctx, in, "/api/documents/", url.Values{"title_content": {query}, "page_size": {"8"}, "ordering": {"-created"}}, &res); err2 != nil {
			if err != nil {
				return nil, err
			}
			return nil, err2
		}
	}
	out := []Item{}
	for _, d := range res.Results {
		created := d.Created
		if len(created) >= 10 {
			created = created[8:10] + "." + created[5:7] + "." + created[:4]
		}
		out = append(out, Item{Title: d.Title, Subtitle: "Dokument · " + created, URL: in.Link(fmt.Sprintf("/documents/%d/details", d.ID))})
	}
	return out, nil
}

// ---- RomM ----

type romm struct{}

func init() { register(romm{}) }

func (romm) Def() Definition {
	return Definition{
		ID: "romm", Name: "RomM", Icon: "romm", Category: "Spiele",
		Description: "Plattformen, Spiele und Speicherstände",
		Fields: []Field{
			{Key: "username", Label: "Benutzername", Type: "text", Required: true},
			{Key: "password", Label: "Passwort", Type: "password", Required: true, Secret: true},
		},
		HasWidget: true, Images: []string{"romm"}, Port: 8080,
	}
}

func (romm) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	auth := base64.StdEncoding.EncodeToString([]byte(in.V("username") + ":" + in.Values["password"]))
	var st map[string]any
	if err := in.do(ctx, request{path: "/api/stats", headers: map[string]string{"Authorization": "Basic " + auth}}, &st); err != nil {
		return nil, err
	}
	// RomM uses upper-case keys (PLATFORMS, ROMS, ...); read case-insensitively.
	val := func(keys ...string) (float64, bool) {
		for k, v := range st {
			for _, want := range keys {
				if strings.EqualFold(k, want) {
					f, ok := v.(float64)
					return f, ok
				}
			}
		}
		return 0, false
	}
	w := &Widget{}
	for _, s := range []struct{ label, key string }{{"Plattformen", "platforms"}, {"Spiele", "roms"}, {"Spielstände", "saves"}} {
		if v, ok := val(s.key); ok {
			w.Stats = append(w.Stats, Stat{Label: s.label, Value: num(int(v))})
		}
	}
	if v, ok := val("total_filesize_bytes", "filesize", "total_filesize"); ok {
		w.Stats = append(w.Stats, Stat{Label: "Größe", Value: humanBytes(v)})
	}
	return w, nil
}

// ---- Stash ----

type stash struct{}

func init() { register(stash{}) }

func (stash) Def() Definition {
	return Definition{
		ID: "stash", Name: "Stash", Icon: "stash", Category: "Medien",
		Description: "Bibliotheksstatistik",
		Fields: []Field{{Key: "apiKey", Label: "API-Key", Type: "password", Secret: true,
			Help: "Settings → Security → API Key (leer lassen, wenn kein Login aktiv ist)"}},
		HasWidget: true, Images: []string{"stash"}, Port: 9999,
	}
}

func (stash) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var res struct {
		Data struct {
			Stats struct {
				Scenes     int     `json:"scene_count"`
				Images     int     `json:"image_count"`
				Performers int     `json:"performer_count"`
				Size       float64 `json:"scenes_size"`
			} `json:"stats"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	h := map[string]string{}
	if k := in.V("apiKey"); k != "" {
		h["ApiKey"] = k
	}
	body := map[string]string{"query": "{ stats { scene_count image_count performer_count scenes_size } }"}
	if err := in.do(ctx, request{method: "POST", path: "/graphql", headers: h, body: body}, &res); err != nil {
		return nil, err
	}
	if len(res.Errors) > 0 {
		return nil, fmt.Errorf("Stash: %s", res.Errors[0].Message)
	}
	s := res.Data.Stats
	return &Widget{Stats: []Stat{
		{Label: "Szenen", Value: num(s.Scenes)},
		{Label: "Bilder", Value: num(s.Images)},
		{Label: "Größe", Value: humanBytes(s.Size)},
	}}, nil
}

// ---- MeTube ----

type metube struct{}

func init() { register(metube{}) }

func (metube) Def() Definition {
	return Definition{
		ID: "metube", Name: "MeTube", Icon: "metube", Category: "Downloads",
		Description: "Laufende und fertige Video-Downloads",
		HasWidget:   true, Images: []string{"metube"}, Port: 8081,
	}
}

type metubeEntry struct {
	Title   string   `json:"title"`
	Status  string   `json:"status"`
	Percent *float64 `json:"percent"`
}

func (metube) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var res struct {
		Done    []metubeEntry `json:"done"`
		Queue   []metubeEntry `json:"queue"`
		Pending []metubeEntry `json:"pending"`
	}
	if err := in.do(ctx, request{path: "/history"}, &res); err != nil {
		return nil, err
	}
	failed := 0
	for _, d := range res.Done {
		if d.Status == "error" {
			failed++
		}
	}
	w := &Widget{
		Stats: []Stat{
			{Label: "Aktiv", Value: fmt.Sprint(len(res.Queue) + len(res.Pending))},
			{Label: "Fertig", Value: fmt.Sprint(len(res.Done) - failed)},
			{Label: "Fehler", Value: fmt.Sprint(failed), Tone: toneIf(failed > 0, "bad")},
		},
		ItemsTitle: "Läuft",
	}
	for _, q := range append(res.Queue, res.Pending...) {
		it := Item{Title: q.Title, Subtitle: q.Status, URL: in.Link("/")}
		if q.Percent != nil {
			it.Progress = pct(*q.Percent)
		}
		w.Items = append(w.Items, it)
		if len(w.Items) == 5 {
			break
		}
	}
	return w, nil
}
