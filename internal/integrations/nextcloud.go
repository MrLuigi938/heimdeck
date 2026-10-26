package integrations

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"

	"github.com/heimdeck/heimdeck/internal/logging"
)

// Nextcloud: OCS API with an app password (Basic auth). The server overview
// comes from the serverinfo app (admins, or any account plus NC-Token), the
// search from Unified Search, which every user may use.

type nextcloud struct{}

func init() { register(nextcloud{}) }

func (nextcloud) Def() Definition {
	return Definition{
		ID: "nextcloud", Name: "Nextcloud", Icon: "nextcloud", Category: "Dokumente",
		Description: "Dateien, Benutzer, aktive Nutzer, freier Speicher, Suche in Dateien, Kalender, Kontakten und mehr",
		Fields: []Field{
			{Key: "username", Label: "Benutzername", Type: "text", Required: true},
			{Key: "password", Label: "App-Passwort", Type: "password", Required: true, Secret: true,
				Help: "Persönliche Einstellungen → Sicherheit → Neues App-Passwort erstellen"},
			{Key: "token", Label: "Serverinfo-Token", Type: "password", Secret: true,
				Help: "Optional, für die Serverübersicht ohne Admin-Konto: occ config:app:set serverinfo token --value=…"},
		},
		HasWidget: true, HasSearch: true, Images: []string{"nextcloud"}, Port: 80,
	}
}

func (nextcloud) headers(in *Instance) map[string]string {
	return map[string]string{
		"OCS-APIRequest": "true",
		"Authorization":  "Basic " + base64.StdEncoding.EncodeToString([]byte(in.V("username")+":"+in.Values["password"])),
	}
}

func (n nextcloud) get(ctx context.Context, in *Instance, path string, q url.Values, extra map[string]string, out any) error {
	if q == nil {
		q = url.Values{}
	}
	q.Set("format", "json")
	h := n.headers(in)
	for k, v := range extra {
		h[k] = v
	}
	return in.do(ctx, request{path: path, query: q, headers: h}, out)
}

func (n nextcloud) Test(ctx context.Context, in *Instance) error {
	var res struct {
		OCS struct {
			Data struct {
				ID string `json:"id"`
			} `json:"data"`
		} `json:"ocs"`
	}
	return n.get(ctx, in, "/ocs/v2.php/cloud/user", nil, nil, &res)
}

func (n nextcloud) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var info struct {
		OCS struct {
			Data struct {
				Nextcloud struct {
					System struct {
						Version   string    `json:"version"`
						Freespace *float64  `json:"freespace"`
						CPULoad   []float64 `json:"cpuload"`
						MemTotal  float64   `json:"mem_total"` // KiB
						MemFree   float64   `json:"mem_free"`
						Apps      struct {
							Updates int `json:"num_updates_available"`
						} `json:"apps"`
					} `json:"system"`
					Storage struct {
						Users int `json:"num_users"`
						Files int `json:"num_files"`
					} `json:"storage"`
					Shares struct {
						Shares int `json:"num_shares"`
						Links  int `json:"num_shares_link"`
					} `json:"shares"`
				} `json:"nextcloud"`
				Server struct {
					Database struct {
						Size float64 `json:"size"`
					} `json:"database"`
				} `json:"server"`
				ActiveUsers struct {
					Last5m  int `json:"last5minutes"`
					Last24h int `json:"last24hours"`
				} `json:"activeUsers"`
			} `json:"data"`
		} `json:"ocs"`
	}
	var extra map[string]string
	if t := in.V("token"); t != "" {
		extra = map[string]string{"NC-Token": t}
	}
	err := n.get(ctx, in, "/ocs/v2.php/apps/serverinfo/api/v1/info", nil, extra, &info)
	var he *HTTPError
	if errors.As(err, &he) && (he.Status == 401 || he.Status == 403 || he.Status == 404) {
		return n.quota(ctx, in)
	}
	if err != nil {
		return nil, err
	}
	d := info.OCS.Data
	w := &Widget{Stats: []Stat{
		{Label: in.T("Dateien"), Value: in.Num(d.Nextcloud.Storage.Files)},
		{Label: in.T("Benutzer"), Value: in.Num(d.Nextcloud.Storage.Users)},
		{Label: in.T("Aktiv (24 h)"), Value: in.Num(d.ActiveUsers.Last24h)},
		{Label: in.T("Freigaben"), Value: in.Num(d.Nextcloud.Shares.Shares)},
	}}
	if f := d.Nextcloud.System.Freespace; f != nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("Frei"), Value: in.Bytes(*f), Tone: toneIf(*f < 10*1024*1024*1024, "warn")})
	}
	sys := d.Nextcloud.System
	w.Stats = append(w.Stats,
		Stat{Label: in.T("Aktiv (5 Min.)"), Value: in.Num(d.ActiveUsers.Last5m)},
		Stat{Label: in.T("Öffentliche Links"), Value: in.Num(d.Nextcloud.Shares.Links)})
	if len(sys.CPULoad) > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Last"), Value: fmt.Sprintf("%.2f", sys.CPULoad[0])})
	}
	if sys.MemTotal > 0 {
		used := (sys.MemTotal - sys.MemFree) / sys.MemTotal * 100
		w.Stats = append(w.Stats, Stat{Label: in.T("RAM"), Value: fmt.Sprintf("%.0f %%", used), Tone: toneIf(used >= 90, "warn")})
	}
	if s := d.Server.Database.Size; s > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Datenbank"), Value: in.Bytes(s)})
	}
	if sys.Version != "" {
		w.Stats = append(w.Stats, Stat{Label: in.T("Version"), Value: sys.Version})
	}
	if sys.Apps.Updates > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("App-Updates"), Value: in.Num(sys.Apps.Updates), Tone: "warn"})
	}
	return w, nil
}

// quota is the fallback for normal accounts: the own storage use.
func (n nextcloud) quota(ctx context.Context, in *Instance) (*Widget, error) {
	var res struct {
		OCS struct {
			Data struct {
				Quota struct {
					Used     float64 `json:"used"`
					Free     float64 `json:"free"`
					Total    float64 `json:"total"`
					Relative float64 `json:"relative"`
					Quota    float64 `json:"quota"`
				} `json:"quota"`
			} `json:"data"`
		} `json:"ocs"`
	}
	if err := n.get(ctx, in, "/ocs/v2.php/cloud/user", nil, nil, &res); err != nil {
		return nil, err
	}
	q := res.OCS.Data.Quota
	w := &Widget{Stats: []Stat{{Label: in.T("Belegt"), Value: in.Bytes(q.Used)}}}
	if q.Quota > 0 {
		w.Stats = append(w.Stats,
			Stat{Label: in.T("Kontingent"), Value: in.Bytes(q.Quota)},
			Stat{Label: in.T("Ausgelastet"), Value: fmt.Sprintf("%.0f %%", q.Relative), Tone: toneIf(q.Relative > 90, "warn")})
	} else if q.Free > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Frei"), Value: in.Bytes(q.Free)})
	}
	return w, nil
}

// ncProviders are searched in this order; others (e.g. settings) are skipped.
var ncProviders = []string{"files", "calendar", "contacts", "talk-conversations", "talk-message", "notes", "deck", "mail", "bookmarks", "photos-albums"}

func (n nextcloud) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	var prov struct {
		OCS struct {
			Data []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"data"`
		} `json:"ocs"`
	}
	if err := n.get(ctx, in, "/ocs/v2.php/search/providers", nil, nil, &prov); err != nil {
		return nil, err
	}
	names := map[string]string{}
	for _, p := range prov.OCS.Data {
		names[p.ID] = p.Name
	}
	results := make([][]Item, len(ncProviders))
	var wg sync.WaitGroup
	for i, id := range ncProviders {
		if _, ok := names[id]; !ok {
			continue
		}
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			defer logging.Recover("Nextcloud-Suche")
			results[i] = n.searchProvider(ctx, in, id, names[id], query)
		}(i, id)
	}
	wg.Wait()
	out := []Item{}
	for _, r := range results {
		out = append(out, r...)
	}
	if len(out) > 10 {
		out = out[:10]
	}
	return out, nil
}

func (n nextcloud) searchProvider(ctx context.Context, in *Instance, id, name, query string) []Item {
	var res struct {
		OCS struct {
			Data struct {
				Entries []struct {
					Title        string `json:"title"`
					Subline      string `json:"subline"`
					ResourceURL  string `json:"resourceUrl"`
					ThumbnailURL string `json:"thumbnailUrl"`
				} `json:"entries"`
			} `json:"data"`
		} `json:"ocs"`
	}
	if err := n.get(ctx, in, "/ocs/v2.php/search/providers/"+url.PathEscape(id)+"/search", url.Values{"term": {query}, "limit": {"4"}}, nil, &res); err != nil {
		return nil
	}
	var out []Item
	for _, e := range res.OCS.Data.Entries {
		it := Item{Title: e.Title, Subtitle: strings.TrimPrefix(name+" · "+e.Subline, " · "), URL: n.link(in, e.ResourceURL)}
		if e.Subline == "" {
			it.Subtitle = name
		}
		if p := n.localPath(in, e.ThumbnailURL); p != "" && ncThumb(p) {
			it.Image = in.Proxy(p)
		}
		out = append(out, it)
	}
	return out
}

// link turns a resource URL into a browser link below the tile's address.
func (n nextcloud) link(in *Instance, u string) string {
	if p := n.localPath(in, u); p != "" {
		return in.Link(p)
	}
	return u
}

// localPath strips scheme, host and base path, so "https://cloud.x/index.php/f/12"
// becomes "/index.php/f/12".
func (nextcloud) localPath(in *Instance, u string) string {
	if u == "" {
		return ""
	}
	pu, err := url.Parse(u)
	if err != nil {
		return ""
	}
	p := pu.EscapedPath()
	if pu.RawQuery != "" {
		p += "?" + pu.RawQuery
	}
	if pu.Fragment != "" {
		p += "#" + pu.EscapedFragment()
	}
	return stripBasePath(in.PublicURL, stripBasePath(in.BaseURL, p))
}

func ncThumb(p string) bool {
	return hasPrefix(p, "/index.php/core/preview", "/core/preview", "/index.php/avatar/", "/avatar/", "/index.php/apps/photos/api/v1/preview/")
}

func (n nextcloud) ImageRequest(ctx context.Context, in *Instance, path string) (string, map[string]string, error) {
	if !ncThumb(path) {
		return "", nil, in.Errorf("nicht gefunden")
	}
	return path, n.headers(in), nil
}
