package integrations

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// ---- Plex ----
// Plex Media Server answers JSON with Accept: application/json; the token goes
// into X-Plex-Token. Covers are loaded through Heimdeck's image proxy so the
// token stays on the server.

type plex struct{}

func init() { register(plex{}) }

func (plex) Def() Definition {
	return Definition{
		ID: "plex", Name: "Plex", Icon: "plex", Category: "Medien",
		Description: "Wer gerade was schaut, Größe der Bibliothek, Suche mit Covern",
		Fields: []Field{{Key: "token", Label: "Plex-Token", Type: "password", Required: true, Secret: true,
			Help: "In Plex Web bei einem Film ⋯ → Medieninfo → XML anzeigen und den Wert von X-Plex-Token aus der Adresse kopieren"}},
		HasWidget: true, HasSearch: true, Images: []string{"plex"}, Port: 32400,
	}
}

func (plex) get(ctx context.Context, in *Instance, path string, q url.Values, out any) error {
	return in.do(ctx, request{path: path, query: q, headers: map[string]string{"X-Plex-Token": in.V("token")}}, out)
}

func (p plex) Test(ctx context.Context, in *Instance) error {
	var v map[string]any
	return p.get(ctx, in, "/library/sections", nil, &v)
}

type plexItem struct {
	RatingKey        string  `json:"ratingKey"`
	Type             string  `json:"type"`
	Title            string  `json:"title"`
	GrandparentTitle string  `json:"grandparentTitle"`
	ParentTitle      string  `json:"parentTitle"`
	ParentIndex      int     `json:"parentIndex"`
	Index            int     `json:"index"`
	Year             int     `json:"year"`
	Thumb            string  `json:"thumb"`
	ViewOffset       float64 `json:"viewOffset"`
	Duration         float64 `json:"duration"`
	User             *struct {
		Title string `json:"title"`
	} `json:"User"`
	Player *struct {
		Title string `json:"title"`
		State string `json:"state"`
	} `json:"Player"`
}

var plexMachines sync.Map // BaseURL → machineIdentifier

func (p plex) link(ctx context.Context, in *Instance, ratingKey string) string {
	id, ok := plexMachines.Load(in.BaseURL)
	if !ok {
		var res struct {
			MediaContainer struct {
				MachineIdentifier string `json:"machineIdentifier"`
			} `json:"MediaContainer"`
		}
		if p.get(ctx, in, "/identity", nil, &res) == nil && res.MediaContainer.MachineIdentifier != "" {
			id = res.MediaContainer.MachineIdentifier
			plexMachines.Store(in.BaseURL, id)
		}
	}
	if id == nil || ratingKey == "" {
		return in.Link("/web/index.html")
	}
	return in.Link("/web/index.html#!/server/" + id.(string) + "/details?key=" + url.QueryEscape("/library/metadata/"+ratingKey))
}

func (p plex) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var sess struct {
		MediaContainer struct {
			Metadata []plexItem `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	if err := p.get(ctx, in, "/status/sessions", nil, &sess); err != nil {
		return nil, err
	}
	var secs struct {
		MediaContainer struct {
			Directory []struct {
				Key  string `json:"key"`
				Type string `json:"type"`
			} `json:"Directory"`
		} `json:"MediaContainer"`
	}
	p.get(ctx, in, "/library/sections", nil, &secs)
	counts := map[string]int{}
	for _, d := range secs.MediaContainer.Directory {
		var c struct {
			MediaContainer struct {
				TotalSize int `json:"totalSize"`
				Size      int `json:"size"`
			} `json:"MediaContainer"`
		}
		q := url.Values{"X-Plex-Container-Start": {"0"}, "X-Plex-Container-Size": {"0"}}
		if p.get(ctx, in, "/library/sections/"+d.Key+"/all", q, &c) == nil {
			n := c.MediaContainer.TotalSize
			if n == 0 {
				n = c.MediaContainer.Size
			}
			counts[d.Type] += n
		}
	}
	w := &Widget{ItemsTitle: in.T("Läuft gerade")}
	for _, m := range sess.MediaContainer.Metadata {
		it := Item{Title: plexTitle(m), URL: p.link(ctx, in, m.RatingKey)}
		var sub []string
		if m.User != nil {
			sub = append(sub, m.User.Title)
		}
		if m.Player != nil {
			sub = append(sub, m.Player.Title)
			if m.Player.State == "paused" {
				it.Badge = in.T("Pausiert")
			}
		}
		it.Subtitle = strings.Join(sub, " · ")
		if m.Duration > 0 {
			it.Progress = pct(m.ViewOffset / m.Duration * 100)
		}
		w.Items = append(w.Items, it)
	}
	w.Stats = []Stat{{Label: in.T("Streams"), Value: in.Num(len(w.Items))}}
	for _, k := range []struct{ typ, label string }{{"movie", "Filme"}, {"show", "Serien"}, {"artist", "Künstler"}} {
		if n, ok := counts[k.typ]; ok {
			w.Stats = append(w.Stats, Stat{Label: in.T(k.label), Value: in.Num(n)})
		}
	}
	return w, nil
}

func plexTitle(m plexItem) string {
	switch m.Type {
	case "episode":
		return fmt.Sprintf("%s · S%02dE%02d", m.GrandparentTitle, m.ParentIndex, m.Index)
	case "track":
		return m.GrandparentTitle + " – " + m.Title
	}
	return m.Title
}

func (p plex) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	var res struct {
		MediaContainer struct {
			Hub []struct {
				Type     string     `json:"type"`
				Metadata []plexItem `json:"Metadata"`
			} `json:"Hub"`
		} `json:"MediaContainer"`
	}
	if err := p.get(ctx, in, "/hubs/search", url.Values{"query": {query}, "limit": {"5"}}, &res); err != nil {
		return nil, err
	}
	out := []Item{}
	for _, h := range res.MediaContainer.Hub {
		kind := map[string]string{"movie": "Film", "show": "Serie", "artist": "Künstler", "album": "Album", "episode": "Folge"}[h.Type]
		if kind == "" {
			continue
		}
		for _, m := range h.Metadata {
			sub := in.T(kind)
			if m.Year > 0 {
				sub += fmt.Sprintf(" · %d", m.Year)
			}
			if h.Type == "album" && m.ParentTitle != "" {
				sub += " · " + m.ParentTitle
			}
			it := Item{Title: plexTitle(m), Subtitle: sub, Badge: in.T("Vorhanden"), URL: p.link(ctx, in, m.RatingKey)}
			if m.Thumb != "" {
				it.Image = in.Proxy(m.Thumb)
			}
			out = append(out, it)
			if len(out) == 8 {
				return out, nil
			}
		}
	}
	return out, nil
}

var plexThumbRe = regexp.MustCompile(`^/library/metadata/\d+/(thumb|art)/\d+$`)

func (p plex) ImageRequest(ctx context.Context, in *Instance, path string) (string, map[string]string, error) {
	if !plexThumbRe.MatchString(path) {
		return "", nil, in.Errorf("nicht gefunden")
	}
	return path, map[string]string{"X-Plex-Token": in.V("token")}, nil
}

// ---- Navidrome (Subsonic API) ----

type navidrome struct{}

func init() { register(navidrome{}) }

func (navidrome) Def() Definition {
	return Definition{
		ID: "navidrome", Name: "Navidrome", Icon: "navidrome", Category: "Medien",
		Description: "Musik: was gerade läuft, Bibliotheksgröße, Suche nach Künstlern, Alben und Titeln mit Covern",
		Fields: []Field{
			{Key: "username", Label: "Benutzername", Type: "text", Required: true},
			{Key: "password", Label: "Passwort", Type: "password", Required: true, Secret: true},
		},
		HasWidget: true, HasSearch: true, Images: []string{"navidrome"}, Port: 4533,
	}
}

// auth builds the Subsonic token parameters: t = md5(password + salt).
func (navidrome) auth(in *Instance) url.Values {
	b := make([]byte, 8)
	rand.Read(b)
	salt := hex.EncodeToString(b)
	sum := md5.Sum([]byte(in.Values["password"] + salt))
	return url.Values{"u": {in.V("username")}, "t": {hex.EncodeToString(sum[:])}, "s": {salt},
		"v": {"1.16.1"}, "c": {"heimdeck"}, "f": {"json"}}
}

func (n navidrome) call(ctx context.Context, in *Instance, method string, q url.Values, out any) error {
	params := n.auth(in)
	for k, v := range q {
		params[k] = v
	}
	var res struct {
		R json.RawMessage `json:"subsonic-response"`
	}
	if err := in.do(ctx, request{path: "/rest/" + method, query: params}, &res); err != nil {
		return err
	}
	var head struct {
		Status string `json:"status"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	json.Unmarshal(res.R, &head)
	if head.Status != "ok" {
		if head.Error != nil && (head.Error.Code == 40 || head.Error.Code == 41) {
			return in.Errorf("Benutzername oder Passwort falsch")
		}
		if head.Error != nil {
			return fmt.Errorf("Navidrome: %s", head.Error.Message)
		}
		return in.Errorf("unerwartete Antwort (kein gültiges JSON) – stimmt der Dienst-Typ? (%v)", head.Status)
	}
	if out != nil {
		return json.Unmarshal(res.R, out)
	}
	return nil
}

func (n navidrome) Test(ctx context.Context, in *Instance) error {
	return n.call(ctx, in, "ping", nil, nil)
}

func (n navidrome) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var scan struct {
		ScanStatus struct {
			Scanning    bool `json:"scanning"`
			Count       int  `json:"count"`
			FolderCount int  `json:"folderCount"`
		} `json:"scanStatus"`
	}
	if err := n.call(ctx, in, "getScanStatus", nil, &scan); err != nil {
		return nil, err
	}
	var np struct {
		NowPlaying struct {
			Entry []struct {
				ID       string  `json:"id"`
				Title    string  `json:"title"`
				Artist   string  `json:"artist"`
				Username string  `json:"username"`
				Player   string  `json:"playerName"`
				AlbumID  string  `json:"albumId"`
				Duration float64 `json:"duration"`
				PosMS    float64 `json:"positionMs"`
			} `json:"entry"`
		} `json:"nowPlaying"`
	}
	n.call(ctx, in, "getNowPlaying", nil, &np)
	w := &Widget{Stats: []Stat{
		{Label: in.T("Streams"), Value: in.Num(len(np.NowPlaying.Entry))},
		{Label: in.T("Titel"), Value: in.Num(scan.ScanStatus.Count)},
	}}
	if scan.ScanStatus.Scanning {
		w.Stats = append(w.Stats, Stat{Label: in.T("Scan"), Value: in.T("Läuft"), Tone: "warn"})
	}
	w.ItemsTitle = in.T("Läuft gerade")
	for _, e := range np.NowPlaying.Entry {
		it := Item{Title: e.Artist + " – " + e.Title, Subtitle: strings.Trim(e.Username+" · "+e.Player, " ·"), URL: in.Link("/app/#/album/" + e.AlbumID + "/show")}
		if e.Duration > 0 && e.PosMS > 0 {
			it.Progress = pct(e.PosMS / 1000 / e.Duration * 100)
		}
		w.Items = append(w.Items, it)
	}
	return w, nil
}

func (n navidrome) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	var res struct {
		Result struct {
			Artist []struct {
				ID, Name, CoverArt string
				AlbumCount         int `json:"albumCount"`
			} `json:"artist"`
			Album []struct {
				ID, Name, Artist, CoverArt string
				Year                       int `json:"year"`
			} `json:"album"`
			Song []struct {
				ID, Title, Artist, Album, AlbumID, CoverArt string
			} `json:"song"`
		} `json:"searchResult3"`
	}
	q := url.Values{"query": {query}, "artistCount": {"2"}, "albumCount": {"3"}, "songCount": {"3"}}
	if err := n.call(ctx, in, "search3", q, &res); err != nil {
		return nil, err
	}
	cover := func(id string) string {
		if id == "" {
			return ""
		}
		return in.Proxy("/rest/getCoverArt?id=" + url.QueryEscape(id) + "&size=120")
	}
	out := []Item{}
	for _, a := range res.Result.Artist {
		out = append(out, Item{Title: a.Name, Subtitle: in.T("Künstler") + " · " + in.T("%d Alben", a.AlbumCount),
			URL: in.Link("/app/#/artist/" + a.ID + "/show"), Image: cover(a.CoverArt)})
	}
	for _, a := range res.Result.Album {
		sub := in.T("Album") + " · " + a.Artist
		if a.Year > 0 {
			sub += fmt.Sprintf(" · %d", a.Year)
		}
		out = append(out, Item{Title: a.Name, Subtitle: sub, URL: in.Link("/app/#/album/" + a.ID + "/show"), Image: cover(a.CoverArt)})
	}
	for _, s := range res.Result.Song {
		out = append(out, Item{Title: s.Title, Subtitle: in.T("Titel") + " · " + s.Artist + " · " + s.Album,
			URL: in.Link("/app/#/album/" + s.AlbumID + "/show"), Image: cover(s.CoverArt)})
	}
	return out, nil
}

var naviCoverRe = regexp.MustCompile(`^/rest/getCoverArt\?id=[A-Za-z0-9_%.-]+&size=120$`)

// The cover endpoint wants the auth parameters in the URL.
func (n navidrome) ImageRequest(ctx context.Context, in *Instance, path string) (string, map[string]string, error) {
	if !naviCoverRe.MatchString(path) {
		return "", nil, in.Errorf("nicht gefunden")
	}
	a := n.auth(in)
	a.Del("f")
	return path + "&" + a.Encode(), nil, nil
}
