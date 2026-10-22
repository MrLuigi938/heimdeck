package integrations

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
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
		{Label: in.T("Dokumente"), Value: in.Num(st.Total)},
		{Label: in.T("Posteingang"), Value: in.Num(st.Inbox), Tone: toneIf(st.Inbox > 0, "warn")},
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
		out = append(out, Item{Title: d.Title, Subtitle: in.T("Dokument") + " · " + created, URL: in.Link(fmt.Sprintf("/documents/%d/details", d.ID))})
	}
	return out, nil
}

// ---- RomM ----

type romm struct{}

func init() { register(romm{}) }

func (romm) Def() Definition {
	return Definition{
		ID: "romm", Name: "RomM", Icon: "romm", Category: "Spiele",
		Description: "Plattformen, Spiele, Spielstände, Save-States, Screenshots, zuletzt hinzugefügte Spiele, Suche nach Spielen",
		Fields: []Field{
			{Key: "apiKey", Label: "API-Token", Type: "password", Secret: true,
				Help: "Empfohlen: in RomM unter Administration → Client API Tokens erstellen (Rechte roms.read und platforms.read). Dann bleiben Benutzername und Passwort leer."},
			{Key: "username", Label: "Benutzername", Type: "text"},
			{Key: "password", Label: "Passwort", Type: "password", Secret: true},
		},
		HasWidget: true, HasSearch: true, Images: []string{"romm"}, Port: 8080,
	}
}

// get authenticates with a Client API Token (RomM 4.x) or, as fallback, username and password.
func (romm) get(ctx context.Context, in *Instance, path string, q url.Values, out any) error {
	var auth string
	switch {
	case in.V("apiKey") != "":
		auth = "Bearer " + in.V("apiKey")
	case in.V("username") != "":
		auth = "Basic " + base64.StdEncoding.EncodeToString([]byte(in.V("username")+":"+in.Values["password"]))
	default:
		return in.Errorf("Bitte einen API-Token oder Benutzername und Passwort eintragen")
	}
	err := in.do(ctx, request{path: path, query: q, headers: map[string]string{"Authorization": auth}}, out)
	var he *HTTPError
	if errors.As(err, &he) && (he.Status == 401 || he.Status == 403) {
		return in.Errorf("RomM lehnt die Anmeldung ab (%d) – am besten einen API-Token mit den Rechten roms.read und platforms.read verwenden", he.Status)
	}
	return err
}

func (r romm) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var st map[string]any
	if err := r.get(ctx, in, "/api/stats", nil, &st); err != nil {
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
	for _, s := range []struct{ label, key string }{{"Plattformen", "platforms"}, {"Spiele", "roms"}, {"Spielstände", "saves"},
		{"Save-States", "states"}, {"Screenshots", "screenshots"}} {
		if v, ok := val(s.key); ok {
			w.Stats = append(w.Stats, Stat{Label: in.T(s.label), Value: in.Num(int(v))})
		}
	}
	if v, ok := val("total_filesize_bytes", "filesize", "total_filesize"); ok {
		w.Stats = append(w.Stats, Stat{Label: in.T("Größe"), Value: in.Bytes(v)})
	}
	// The newest games, with their covers.
	var raw json.RawMessage
	if r.get(ctx, in, "/api/roms", url.Values{"order_by": {"id"}, "order_dir": {"desc"}, "limit": {"5"}}, &raw) == nil {
		for _, rom := range rommList(raw) {
			w.Items = append(w.Items, rommItem(in, rom))
			if len(w.Items) == 5 {
				break
			}
		}
		if len(w.Items) > 0 {
			w.ItemsTitle = in.T("Neu hinzugefügt")
		}
	}
	return w, nil
}

// rommList reads a game list, plain or wrapped in {"items": [...]} depending on the version.
func rommList(raw json.RawMessage) []rommRom {
	var roms []rommRom
	if json.Unmarshal(raw, &roms) == nil {
		return roms
	}
	var page struct {
		Items []rommRom `json:"items"`
	}
	json.Unmarshal(raw, &page)
	return page.Items
}

func rommItem(in *Instance, rom rommRom) Item {
	name := rom.Name
	if name == "" {
		name = rom.FsName
	}
	sub := rom.PlatformDisplayName
	if sub == "" {
		sub = rom.PlatformName
	}
	if rom.FirstReleaseDate != nil && *rom.FirstReleaseDate > 0 {
		year := time.UnixMilli(*rom.FirstReleaseDate).Year()
		if sub != "" {
			sub += " · "
		}
		sub += fmt.Sprint(year)
	}
	it := Item{Title: name, Subtitle: sub, URL: in.Link(fmt.Sprintf("/rom/%d", rom.ID))}
	// Prefer RomM's own copy of the cover; IGDB's address is the fallback.
	if p := rom.PathCoverSmall + rom.PathCoverS; p != "" {
		p = strings.TrimLeft(p, "/")
		if !strings.HasPrefix(p, "assets/") {
			p = "assets/romm/resources/" + p
		}
		it.Image = in.Link("/" + p)
	} else if rom.URLCover != "" {
		it.Image = rom.URLCover
	}
	return it
}

type rommRom struct {
	ID                  int    `json:"id"`
	Name                string `json:"name"`
	FsName              string `json:"fs_name"`
	PlatformName        string `json:"platform_name"`
	PlatformDisplayName string `json:"platform_display_name"`
	FirstReleaseDate    *int64 `json:"first_release_date"` // milliseconds
	URLCover            string `json:"url_cover"`
	PathCoverSmall      string `json:"path_cover_small"` // RomM 4
	PathCoverS          string `json:"path_cover_s"`     // RomM 3
}

// Search finds platforms by name and games via RomM's own search.
func (r romm) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	out := []Item{}
	q := strings.ToLower(query)

	var platforms []struct {
		ID          int    `json:"id"`
		Name        string `json:"name"`
		DisplayName string `json:"display_name"`
		RomCount    int    `json:"rom_count"`
	}
	if err := r.get(ctx, in, "/api/platforms", nil, &platforms); err == nil {
		for _, p := range platforms {
			name := p.DisplayName
			if name == "" {
				name = p.Name
			}
			if strings.Contains(strings.ToLower(name), q) && len(out) < 3 {
				out = append(out, Item{Title: name, Subtitle: in.T("%d Spiele", p.RomCount),
					Badge: in.T("Plattform"), URL: in.Link(fmt.Sprintf("/platform/%d", p.ID))})
			}
		}
	}

	var raw json.RawMessage
	if err := r.get(ctx, in, "/api/roms", url.Values{"search_term": {query}, "limit": {"8"}}, &raw); err != nil {
		if len(out) > 0 {
			return out, nil
		}
		return nil, err
	}
	for i, rom := range rommList(raw) {
		if i == 8 {
			break
		}
		out = append(out, rommItem(in, rom))
	}
	return out, nil
}

// ---- Stash ----

type stash struct{}

func init() { register(stash{}) }

func (stash) Def() Definition {
	return Definition{
		ID: "stash", Name: "Stash", Icon: "stash", Category: "Medien",
		Description: "Bibliotheksstatistik: Szenen, Bilder, Galerien, Darsteller, Studios, Tags, Laufzeit, Größe und Wiedergaben",
		Fields: []Field{{Key: "apiKey", Label: "API-Key", Type: "password", Secret: true,
			Help: "Settings → Security → API Key (leer lassen, wenn kein Login aktiv ist)"}},
		HasWidget: true, Images: []string{"stash"}, Port: 9999,
	}
}

type stashStats struct {
	Scenes     int     `json:"scene_count"`
	Size       float64 `json:"scenes_size"`
	Duration   float64 `json:"scenes_duration"` // seconds
	Images     int     `json:"image_count"`
	ImagesSize float64 `json:"images_size"`
	Galleries  int     `json:"gallery_count"`
	Performers int     `json:"performer_count"`
	Studios    int     `json:"studio_count"`
	Tags       int     `json:"tag_count"`
	Plays      *int    `json:"total_play_count"` // newer versions only
	Played     *int    `json:"scenes_played"`
}

// query runs a GraphQL query against Stash.
func (stash) query(ctx context.Context, in *Instance, q string, out any) error {
	var res struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	h := map[string]string{}
	if k := in.V("apiKey"); k != "" {
		h["ApiKey"] = k
	}
	if err := in.do(ctx, request{method: "POST", path: "/graphql", headers: h, body: map[string]string{"query": q}}, &res); err != nil {
		return err
	}
	if len(res.Errors) > 0 {
		return fmt.Errorf("Stash: %s", res.Errors[0].Message)
	}
	return json.Unmarshal(res.Data, out)
}

func (s stash) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var res struct {
		Stats stashStats `json:"stats"`
	}
	// Fields every supported version knows; a field unknown to the server would fail the whole query.
	if err := s.query(ctx, in, "{ stats { scene_count scenes_size scenes_duration image_count images_size gallery_count performer_count studio_count tag_count } }", &res); err != nil {
		return nil, err
	}
	st := res.Stats
	w := &Widget{Stats: []Stat{
		{Label: in.T("Szenen"), Value: in.Num(st.Scenes)},
		{Label: in.T("Bilder"), Value: in.Num(st.Images)},
		{Label: in.T("Galerien"), Value: in.Num(st.Galleries)},
		{Label: in.T("Darsteller"), Value: in.Num(st.Performers)},
		{Label: in.T("Studios"), Value: in.Num(st.Studios)},
		{Label: in.T("Tags"), Value: in.Num(st.Tags)},
		{Label: in.T("Laufzeit"), Value: in.T("%d Std.", int(st.Duration/3600))},
		{Label: in.T("Größe"), Value: in.Bytes(st.Size)},
		{Label: in.T("Bilder-Größe"), Value: in.Bytes(st.ImagesSize)},
	}}
	// Play statistics exist since Stash 0.24; older versions just leave them out.
	var plays struct {
		Stats stashStats `json:"stats"`
	}
	if s.query(ctx, in, "{ stats { total_play_count scenes_played } }", &plays) == nil && plays.Stats.Plays != nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("Wiedergaben"), Value: in.Num(*plays.Stats.Plays)})
		if plays.Stats.Played != nil {
			w.Stats = append(w.Stats, Stat{Label: in.T("Gesehene Szenen"), Value: in.Num(*plays.Stats.Played)})
		}
	}
	return w, nil
}

// ---- MeTube ----

type metube struct{}

func init() { register(metube{}) }

func (metube) Def() Definition {
	return Definition{
		ID: "metube", Name: "MeTube", Icon: "metube", Category: "Downloads",
		Description: "Laufende Video-Downloads mit Tempo und Restzeit, fertige und fehlgeschlagene Downloads",
		HasWidget:   true, Images: []string{"metube"}, Port: 8081,
	}
}

type metubeEntry struct {
	Title   string   `json:"title"`
	Status  string   `json:"status"`
	Percent *float64 `json:"percent"`
	Speed   *float64 `json:"speed"` // bytes per second
	ETA     *float64 `json:"eta"`   // seconds
	Msg     string   `json:"msg"`
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
	var speed float64
	for _, q := range res.Queue {
		if q.Speed != nil {
			speed += *q.Speed
		}
	}
	w := &Widget{
		Stats: []Stat{
			{Label: in.T("Aktiv"), Value: fmt.Sprint(len(res.Queue) + len(res.Pending))},
			{Label: in.T("Fertig"), Value: fmt.Sprint(len(res.Done) - failed)},
			{Label: in.T("Fehler"), Value: fmt.Sprint(failed), Tone: toneIf(failed > 0, "bad")},
			{Label: in.T("Tempo"), Value: in.Bytes(speed) + "/s"},
		},
		ItemsTitle: in.T("Läuft"),
		Alerts:     failedMsgs(res.Done),
	}
	for _, q := range append(res.Queue, res.Pending...) {
		sub := []string{in.Status(q.Status)}
		if q.Speed != nil && *q.Speed > 0 {
			sub = append(sub, in.Bytes(*q.Speed)+"/s")
		}
		if q.ETA != nil && *q.ETA > 0 {
			sub = append(sub, in.T("noch %s", shortDuration(int(*q.ETA))))
		}
		it := Item{Title: q.Title, Subtitle: strings.Join(sub, " · "), URL: in.Link("/")}
		if q.Percent != nil {
			it.Progress = pct(*q.Percent)
		}
		w.Items = append(w.Items, it)
		if len(w.Items) == 5 {
			break
		}
	}
	// Nothing running: the latest finished downloads instead of an empty list.
	if len(w.Items) == 0 && len(res.Done) > 0 {
		w.ItemsTitle = in.T("Zuletzt fertig")
		for i := len(res.Done) - 1; i >= 0 && len(w.Items) < 5; i-- {
			d := res.Done[i]
			it := Item{Title: d.Title, URL: in.Link("/")}
			if d.Status == "error" {
				it.Badge = in.T("Fehlgeschlagen")
			}
			w.Items = append(w.Items, it)
		}
	}
	return w, nil
}

// failedMsgs lists the newest failed downloads with MeTube's reason (at most three).
func failedMsgs(done []metubeEntry) []string {
	var out []string
	for i := len(done) - 1; i >= 0 && len(out) < 3; i-- {
		if d := done[i]; d.Status == "error" {
			msg := d.Title
			if d.Msg != "" {
				msg += ": " + d.Msg
			}
			if r := []rune(msg); len(r) > 160 {
				msg = string(r[:160]) + "…"
			}
			out = append(out, msg)
		}
	}
	return out
}
