package server

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"

	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/store"
)

// Heimdall (github.com/linuxserver/Heimdall) exports its items under
// Settings → Export as a JSON array (ItemRestController::index). Each item
// names its app by the appid from the Heimdall-Apps repository and keeps the
// app's settings (API key, user, password, override URL) as a JSON string in
// "description".

type heimdallItem struct {
	Title          string   `json:"title"`
	URL            string   `json:"url"`
	Description    *string  `json:"description"`
	AppID          *string  `json:"appid"`
	AppDescription *string  `json:"appdescription"`
	Pinned         any      `json:"pinned"`
	Order          any      `json:"pinned_order"`
	Tags           []string `json:"tags"`
}

type heimdallConfig struct {
	APIKey      string `json:"apikey"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	OverrideURL string `json:"override_url"`
}

// heimdallApps maps Heimdall appids (from each app.json in
// github.com/linuxserver/Heimdall-Apps) to Heimdeck integrations.
var heimdallApps = map[string]string{
	"140902edbcc424c09736af28ab2de604c3bde936": "adguard",
	"c7ec3e2f2d9be6a4d313d2451184b08ade93ce47": "audiobookshelf",
	"085f0b437f9bf9c98bb68b745c8dcf323a7e0499": "bazarr",
	"0bafb882c9d2c034ca5333367f8ccc90f4ae85e4": "dockge",
	"d31b835a2762114118c3bd608c5131ebcd6ee073": "evcc",
	"5c81a4f5ee23ebffa0724a22a040c54e6cb9d7c5": "home-assistant",
	"101e12b916274de5d628fad717f09c5ca302b5c2": "immich",
	"e3e34703718888700cefb3c28a0e52cb6a903ff0": "iobroker",
	"af7b37e2841d9150f6abd5a936b32a1f681d6bda": "jdownloader",
	"3e0a7f109bd760b9474c78cb652e8c3e82669226": "jellyfin",
	"0628c8d1a4868d6e6b994ae43a091f4e142ce642": "jellyseerr",
	"989fb18945a331b430fc284edfe6451aa760f8e6": "jellyseerr", // Overseerr
	"deebb60d4aafd710c8f6a4cfd917b570748f2c0c": "kavita",
	"6cd97a5fefab651371f821cb5167733112fbc536": "komga",
	"c015fff30a3dc43f5987079c0142437e3e757fd0": "lidarr",
	"d1c308982299c5face7a8b9bcf3f585ae08c13fd": "metube",
	"f2baa52d02ca888455ce47823f47bf372d5eecb3": "nextcloud",
	"6249c08eaa417b9918c69ed2d32ac88b386bc1b2": "paperless",
	"23e9c842d2423902706ae92996554f6fbd92c502": "paperless", // PaperlessNgx
	"07b796b3a10eaa8b6f485ed2379187a39400b459": "portainer",
	"de6ff49c94538e6e13a77f75991141a4fe9af8bf": "prowlarr",
	"391f2b7f3fe853e1ea09723eeafc354fa291ab48": "proxmox",
	"3ebd4dd8afe6308e392ccf09e3516eee99a1e8df": "qbittorrent",
	"8a5cd39a1a5dff323d63bbe45fffe0c9c03bd2e7": "radarr",
	"2a3931f4ca016a139bcf5a97c85ee0a67825fe43": "romm",
	"7aa898759f1d210aaa5edc8a184bfe5e1668b5bd": "sabnzbd",
	"6e2a525ceb737f9ed8badcef5eb20d1502e39656": "sonarr",
	"c106a4464d1b08d0e35edb92ddfabae6f9dc765f": "speedtest-tracker",
	"585815f2ee7291f96ea30333fd94d20b5722693f": "stash",
	"8cdba423d7bb49fad21662247750cd715f08cdc5": "unifi",
	"ddf4f264320af1347ef54d424c60fae3b4fcc448": "vaultwarden",
	"2721c724e1932aee2eb38c86d887ddc25bf01b54": "zoraxy",
}

// isHeimdallExport reports whether body looks like a Heimdall export: a JSON
// array (Heimdeck backups are objects).
func isHeimdallExport(body []byte) bool {
	s := strings.TrimSpace(string(body))
	return strings.HasPrefix(s, "[")
}

var iconSlug = regexp.MustCompile(`[^a-z0-9]+`)

// heimdallTiles converts a Heimdall export into groups of tiles. Items are
// grouped by their first tag; untagged items land in defaultGroup.
func heimdallTiles(body []byte, defaultGroup string) (map[string][]tileInput, []string, error) {
	var items []heimdallItem
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, nil, err
	}
	sort.SliceStable(items, func(i, j int) bool { return num(items[i].Order) < num(items[j].Order) })
	groups := map[string][]tileInput{}
	var order []string
	for _, it := range items {
		if strings.TrimSpace(it.Title) == "" || strings.TrimSpace(it.URL) == "" {
			continue
		}
		group := defaultGroup
		if len(it.Tags) > 0 && strings.TrimSpace(it.Tags[0]) != "" {
			group = strings.TrimSpace(it.Tags[0])
		}
		if _, ok := groups[group]; !ok {
			order = append(order, group)
		}
		in := tileInput{Tile: store.Tile{Name: it.Title, URL: it.URL, Integration: "link", Config: map[string]string{}}, Secrets: map[string]string{}}
		in.Icon = iconSlug.ReplaceAllString(strings.ToLower(it.Title), "-")
		if it.AppID != nil {
			if id, ok := heimdallApps[*it.AppID]; ok {
				if integ, ok := integrations.Get(id); ok {
					d := integ.Def()
					in.Integration, in.Icon = d.ID, d.Icon
					in.ShowWidget = d.HasWidget
					var cfg heimdallConfig
					if it.Description != nil {
						json.Unmarshal([]byte(*it.Description), &cfg)
					}
					applyHeimdallConfig(&in, d, cfg)
				}
			}
		}
		groups[group] = append(groups[group], in)
	}
	return groups, order, nil
}

// applyHeimdallConfig carries the credentials over to the matching fields.
func applyHeimdallConfig(in *tileInput, d integrations.Definition, cfg heimdallConfig) {
	values := map[string]string{"username": cfg.Username, "password": cfg.Password}
	for _, f := range d.Fields {
		v := values[f.Key]
		if f.Key == "apiKey" || f.Key == "token" {
			v = cfg.APIKey
		}
		if v == "" {
			continue
		}
		if f.Secret {
			in.Secrets[f.Key] = v
		} else {
			in.Config[f.Key] = v
		}
	}
	// Heimdall's "override URL" is the address used for API calls.
	if u := strings.TrimSpace(cfg.OverrideURL); u != "" && u != in.URL {
		in.APIURL = u
	}
}

func num(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case string:
		var f float64
		json.Unmarshal([]byte(n), &f)
		return f
	}
	return 0
}
