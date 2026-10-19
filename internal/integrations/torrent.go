package integrations

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
)

// Download clients with a session login: qBittorrent (WebUI API v2),
// Transmission (RPC) and Deluge (Web JSON-RPC). Sessions are cached per
// address and user and renewed when the client answers 401/403.

var sessions sync.Map // key → cookie or session id

func sessionKey(in *Instance, kind string) string {
	return kind + "\x00" + in.BaseURL + "\x00" + in.V("username")
}

// rawDo sends a request and returns status, headers and body (max 5 MB).
func (in *Instance) rawDo(ctx context.Context, method, path string, body io.Reader, headers map[string]string) (int, http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(in.BaseURL, "/")+path, body)
	if err != nil {
		return 0, nil, nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := in.Client.Do(req)
	if err != nil {
		return 0, nil, nil, friendlyNetErr(in.Lang, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	return resp.StatusCode, resp.Header, b, nil
}

func torrentEta(in *Instance, sec float64) string {
	if sec <= 0 || sec >= 8640000 {
		return ""
	}
	return in.T("noch %s", shortDuration(int(sec)))
}

// ---- qBittorrent ----

type qbittorrent struct{}

func init() { register(qbittorrent{}) }

func (qbittorrent) Def() Definition {
	return Definition{
		ID: "qbittorrent", Name: "qBittorrent", Icon: "qbittorrent", Category: "Downloads",
		Description: "Tempo, aktive Torrents mit Fortschritt, alle pausieren/fortsetzen, Torrent-Suche",
		Fields: []Field{
			{Key: "username", Label: "Benutzername", Type: "text", Help: "Leer lassen, wenn Heimdeck in der Whitelist („Authentifizierung überspringen“) steht"},
			{Key: "password", Label: "Passwort", Type: "password", Secret: true},
		},
		HasWidget: true, HasSearch: true, Images: []string{"qbittorrent"}, Port: 8080,
	}
}

func (q qbittorrent) login(ctx context.Context, in *Instance) (string, error) {
	form := url.Values{"username": {in.V("username")}, "password": {in.Values["password"]}}
	status, h, body, err := in.rawDo(ctx, "POST", "/api/v2/auth/login", strings.NewReader(form.Encode()),
		map[string]string{"Content-Type": "application/x-www-form-urlencoded"})
	if err != nil {
		return "", err
	}
	if status == 403 {
		return "", in.Errorf("qBittorrent hat Heimdeck nach zu vielen Fehlversuchen gesperrt – später erneut versuchen")
	}
	if status != 200 || strings.TrimSpace(string(body)) == "Fails." {
		return "", in.Errorf("Benutzername oder Passwort falsch")
	}
	// The cookie is called SID or, since 5.2, QBT_SID_<port>.
	for _, c := range (&http.Response{Header: h}).Cookies() {
		if strings.Contains(c.Name, "SID") {
			cookie := c.Name + "=" + c.Value
			sessions.Store(sessionKey(in, "qbt"), cookie)
			return cookie, nil
		}
	}
	return "", nil // no login needed (whitelist)
}

func (q qbittorrent) call(ctx context.Context, in *Instance, method, path string, form url.Values, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		h := map[string]string{}
		if c, ok := sessions.Load(sessionKey(in, "qbt")); ok {
			h["Cookie"] = c.(string)
		}
		var body io.Reader
		if form != nil {
			body = strings.NewReader(form.Encode())
			h["Content-Type"] = "application/x-www-form-urlencoded"
		}
		status, _, b, err := in.rawDo(ctx, method, path, body, h)
		if err != nil {
			return err
		}
		if (status == 401 || status == 403) && attempt == 0 {
			if _, err := q.login(ctx, in); err != nil {
				return err
			}
			continue
		}
		if status < 200 || status > 299 {
			return &HTTPError{Status: status, Body: strings.TrimSpace(string(b)), Lang: in.Lang}
		}
		if out != nil {
			if s, ok := out.(*string); ok {
				*s = string(b)
				return nil
			}
			return json.Unmarshal(b, out)
		}
		return nil
	}
	return &HTTPError{Status: 401, Lang: in.Lang}
}

type qbtTorrent struct {
	Hash     string  `json:"hash"`
	Name     string  `json:"name"`
	Progress float64 `json:"progress"`
	DLSpeed  float64 `json:"dlspeed"`
	ETA      float64 `json:"eta"`
	State    string  `json:"state"`
	Size     float64 `json:"size"`
}

func (q qbittorrent) Test(ctx context.Context, in *Instance) error {
	var v string
	return q.call(ctx, in, "GET", "/api/v2/app/version", nil, &v)
}

func qbtState(in *Instance, s string) string {
	switch {
	case s == "downloading" || s == "forcedDL" || s == "metaDL" || s == "forcedMetaDL":
		return in.Status("downloading")
	case strings.HasPrefix(s, "stalled"):
		return in.T("Wartet auf Peers")
	case strings.HasPrefix(s, "paused") || strings.HasPrefix(s, "stopped"):
		return in.Status("paused")
	case strings.HasPrefix(s, "queued"):
		return in.Status("queued")
	case strings.HasPrefix(s, "checking"):
		return in.Status("verifying")
	case s == "uploading" || s == "forcedUP":
		return in.T("Seedet")
	case s == "error" || s == "missingFiles":
		return in.Status("failed")
	}
	return s
}

func (q qbittorrent) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var info struct {
		DL     float64 `json:"dl_info_speed"`
		UP     float64 `json:"up_info_speed"`
		Status string  `json:"connection_status"`
	}
	if err := q.call(ctx, in, "GET", "/api/v2/transfer/info", nil, &info); err != nil {
		return nil, err
	}
	var all []qbtTorrent
	if err := q.call(ctx, in, "GET", "/api/v2/torrents/info", nil, &all); err != nil {
		return nil, err
	}
	downloading, seeding, paused, errs := 0, 0, 0, 0
	var active []qbtTorrent
	for _, t := range all {
		switch {
		case t.Progress < 1 && !strings.HasPrefix(t.State, "paused") && !strings.HasPrefix(t.State, "stopped"):
			downloading++
			active = append(active, t)
		case t.State == "uploading" || t.State == "stalledUP" || t.State == "forcedUP":
			seeding++
		}
		if strings.HasPrefix(t.State, "paused") || strings.HasPrefix(t.State, "stopped") {
			paused++
		}
		if t.State == "error" || t.State == "missingFiles" {
			errs++
		}
	}
	w := &Widget{Stats: []Stat{
		{Label: "↓", Value: in.Bytes(info.DL) + "/s"},
		{Label: "↑", Value: in.Bytes(info.UP) + "/s"},
		{Label: in.T("Lädt"), Value: in.Num(downloading)},
		{Label: in.T("Seedet"), Value: in.Num(seeding)},
	}}
	if errs > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Fehler"), Value: in.Num(errs), Tone: "bad"})
	}
	if info.Status == "disconnected" {
		w.Alerts = append(w.Alerts, in.T("qBittorrent ist nicht mit dem Netzwerk verbunden"))
	}
	sort.Slice(active, func(i, j int) bool { return active[i].DLSpeed > active[j].DLSpeed })
	w.ItemsTitle = in.T("Downloads")
	for i, t := range active {
		if i == 5 {
			break
		}
		w.Items = append(w.Items, q.item(in, t))
	}
	if paused > 0 && paused == len(all) {
		w.Actions = []Action{{ID: "start", Label: in.T("Alle fortsetzen"), Tone: "primary"}}
	} else if len(all) > 0 {
		w.Actions = []Action{{ID: "stop", Label: in.T("Alle pausieren")}}
	}
	return w, nil
}

func (q qbittorrent) item(in *Instance, t qbtTorrent) Item {
	sub := []string{qbtState(in, t.State)}
	if t.DLSpeed > 0 {
		sub = append(sub, in.Bytes(t.DLSpeed)+"/s")
	}
	if eta := torrentEta(in, t.ETA); eta != "" && t.Progress < 1 {
		sub = append(sub, eta)
	}
	it := Item{Title: t.Name, Subtitle: strings.Join(sub, " · "), Badge: in.Bytes(t.Size), URL: in.Link("/")}
	if t.Progress < 1 {
		it.Progress = pct(t.Progress * 100)
	}
	return it
}

func (q qbittorrent) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	var all []qbtTorrent
	if err := q.call(ctx, in, "GET", "/api/v2/torrents/info", nil, &all); err != nil {
		return nil, err
	}
	out := []Item{}
	for _, t := range all {
		if strings.Contains(strings.ToLower(t.Name), strings.ToLower(query)) {
			out = append(out, q.item(in, t))
			if len(out) == 8 {
				break
			}
		}
	}
	return out, nil
}

func (q qbittorrent) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	if action != "stop" && action != "start" {
		return in.Errorf("unbekannte Aktion %q", action)
	}
	form := url.Values{"hashes": {"all"}}
	err := q.call(ctx, in, "POST", "/api/v2/torrents/"+action, form, nil)
	var he *HTTPError
	if errors.As(err, &he) && he.Status == 404 {
		// qBittorrent 4.x calls them pause/resume.
		old := map[string]string{"stop": "pause", "start": "resume"}[action]
		err = q.call(ctx, in, "POST", "/api/v2/torrents/"+old, form, nil)
	}
	return err
}

// ---- Transmission ----

type transmission struct{}

func init() { register(transmission{}) }

func (transmission) Def() Definition {
	return Definition{
		ID: "transmission", Name: "Transmission", Icon: "transmission", Category: "Downloads",
		Description: "Tempo, aktive Torrents mit Fortschritt, alle pausieren/fortsetzen, Torrent-Suche",
		Fields: []Field{
			{Key: "username", Label: "Benutzername", Type: "text", Help: "Nur nötig, wenn in Transmission eine Anmeldung eingerichtet ist"},
			{Key: "password", Label: "Passwort", Type: "password", Secret: true},
		},
		HasWidget: true, HasSearch: true, Images: []string{"transmission"}, Port: 9091,
	}
}

// call uses the classic RPC format, which every version up to 4.x accepts.
func (tr transmission) call(ctx context.Context, in *Instance, method string, args any, out any) error {
	body, _ := json.Marshal(map[string]any{"method": method, "arguments": args})
	for attempt := 0; attempt < 2; attempt++ {
		h := map[string]string{"Content-Type": "application/json"}
		if in.V("username") != "" {
			h["Authorization"] = "Basic " + basicAuth(in.V("username"), in.Values["password"])
		}
		if id, ok := sessions.Load(sessionKey(in, "tr")); ok {
			h["X-Transmission-Session-Id"] = id.(string)
		}
		status, hdr, b, err := in.rawDo(ctx, "POST", "/transmission/rpc", bytes.NewReader(body), h)
		if err != nil {
			return err
		}
		if status == 409 {
			sessions.Store(sessionKey(in, "tr"), hdr.Get("X-Transmission-Session-Id"))
			continue
		}
		if status != 200 {
			return &HTTPError{Status: status, Lang: in.Lang}
		}
		var res struct {
			Result    string          `json:"result"`
			Arguments json.RawMessage `json:"arguments"`
		}
		if err := json.Unmarshal(b, &res); err != nil {
			return in.Errorf("unerwartete Antwort (kein gültiges JSON) – stimmt der Dienst-Typ? (%v)", err)
		}
		if res.Result != "success" {
			return errors.New("Transmission: " + res.Result)
		}
		if out != nil {
			return json.Unmarshal(res.Arguments, out)
		}
		return nil
	}
	return &HTTPError{Status: 409, Lang: in.Lang}
}

type trTorrent struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	PercentDone float64 `json:"percentDone"`
	Rate        float64 `json:"rateDownload"`
	ETA         float64 `json:"eta"`
	Status      int     `json:"status"`
	Size        float64 `json:"totalSize"`
	Error       int     `json:"error"`
}

var trFields = []string{"id", "name", "percentDone", "rateDownload", "eta", "status", "totalSize", "error"}

func (tr transmission) torrents(ctx context.Context, in *Instance) ([]trTorrent, error) {
	var res struct {
		Torrents []trTorrent `json:"torrents"`
	}
	err := tr.call(ctx, in, "torrent-get", map[string]any{"fields": trFields}, &res)
	return res.Torrents, err
}

func trState(in *Instance, s int) string {
	switch s {
	case 0:
		return in.Status("paused")
	case 1, 2:
		return in.Status("verifying")
	case 3, 5:
		return in.Status("queued")
	case 4:
		return in.Status("downloading")
	case 6:
		return in.T("Seedet")
	}
	return ""
}

func (tr transmission) item(in *Instance, t trTorrent) Item {
	sub := []string{trState(in, t.Status)}
	if t.Rate > 0 {
		sub = append(sub, in.Bytes(t.Rate)+"/s")
	}
	if eta := torrentEta(in, t.ETA); eta != "" && t.PercentDone < 1 {
		sub = append(sub, eta)
	}
	it := Item{Title: t.Name, Subtitle: strings.Join(sub, " · "), Badge: in.Bytes(t.Size), URL: in.Link("/transmission/web/")}
	if t.PercentDone < 1 {
		it.Progress = pct(t.PercentDone * 100)
	}
	return it
}

func (tr transmission) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var st struct {
		Down   float64 `json:"downloadSpeed"`
		Up     float64 `json:"uploadSpeed"`
		Active int     `json:"activeTorrentCount"`
		Paused int     `json:"pausedTorrentCount"`
		Count  int     `json:"torrentCount"`
	}
	if err := tr.call(ctx, in, "session-stats", map[string]any{}, &st); err != nil {
		return nil, err
	}
	all, err := tr.torrents(ctx, in)
	if err != nil {
		return nil, err
	}
	downloading, seeding, errs := 0, 0, 0
	var active []trTorrent
	for _, t := range all {
		switch t.Status {
		case 3, 4:
			downloading++
			active = append(active, t)
		case 5, 6:
			seeding++
		}
		if t.Error != 0 {
			errs++
		}
	}
	w := &Widget{Stats: []Stat{
		{Label: "↓", Value: in.Bytes(st.Down) + "/s"},
		{Label: "↑", Value: in.Bytes(st.Up) + "/s"},
		{Label: in.T("Lädt"), Value: in.Num(downloading)},
		{Label: in.T("Seedet"), Value: in.Num(seeding)},
	}}
	if errs > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Fehler"), Value: in.Num(errs), Tone: "bad"})
	}
	sort.Slice(active, func(i, j int) bool { return active[i].Rate > active[j].Rate })
	w.ItemsTitle = in.T("Downloads")
	for i, t := range active {
		if i == 5 {
			break
		}
		w.Items = append(w.Items, tr.item(in, t))
	}
	if st.Count > 0 && st.Paused == st.Count {
		w.Actions = []Action{{ID: "start", Label: in.T("Alle fortsetzen"), Tone: "primary"}}
	} else if st.Count > 0 {
		w.Actions = []Action{{ID: "stop", Label: in.T("Alle pausieren")}}
	}
	return w, nil
}

func (tr transmission) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	all, err := tr.torrents(ctx, in)
	if err != nil {
		return nil, err
	}
	out := []Item{}
	for _, t := range all {
		if strings.Contains(strings.ToLower(t.Name), strings.ToLower(query)) {
			out = append(out, tr.item(in, t))
			if len(out) == 8 {
				break
			}
		}
	}
	return out, nil
}

func (tr transmission) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	if action != "stop" && action != "start" {
		return in.Errorf("unbekannte Aktion %q", action)
	}
	// Without "ids" the command applies to all torrents.
	return tr.call(ctx, in, "torrent-"+action, map[string]any{}, nil)
}

// ---- Deluge ----

type deluge struct{}

func init() { register(deluge{}) }

func (deluge) Def() Definition {
	return Definition{
		ID: "deluge", Name: "Deluge", Icon: "deluge", Category: "Downloads",
		Description: "Tempo, aktive Torrents mit Fortschritt, alle pausieren/fortsetzen, Torrent-Suche",
		Fields: []Field{{Key: "password", Label: "Passwort", Type: "password", Required: true, Secret: true,
			Help: "Das Passwort der Weboberfläche (Standard: deluge)"}},
		HasWidget: true, HasSearch: true, Images: []string{"deluge"}, Port: 8112,
	}
}

func (d deluge) rpc(ctx context.Context, in *Instance, method string, params []any, out any) error {
	body, _ := json.Marshal(map[string]any{"method": method, "params": params, "id": 1})
	for attempt := 0; attempt < 2; attempt++ {
		h := map[string]string{"Content-Type": "application/json"}
		if c, ok := sessions.Load(sessionKey(in, "deluge")); ok {
			h["Cookie"] = c.(string)
		}
		status, hdr, b, err := in.rawDo(ctx, "POST", "/json", bytes.NewReader(body), h)
		if err != nil {
			return err
		}
		if status != 200 {
			return &HTTPError{Status: status, Lang: in.Lang}
		}
		var res struct {
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
				Code    int    `json:"code"`
			} `json:"error"`
		}
		if err := json.Unmarshal(b, &res); err != nil {
			return in.Errorf("unerwartete Antwort (kein gültiges JSON) – stimmt der Dienst-Typ? (%v)", err)
		}
		if method == "auth.login" {
			for _, c := range (&http.Response{Header: hdr}).Cookies() {
				if c.Name == "_session_id" {
					sessions.Store(sessionKey(in, "deluge"), c.Name+"="+c.Value)
				}
			}
		}
		if res.Error != nil && res.Error.Code == 1 && attempt == 0 && method != "auth.login" {
			if err := d.login(ctx, in); err != nil {
				return err
			}
			continue
		}
		if res.Error != nil {
			return errors.New("Deluge: " + res.Error.Message)
		}
		if out != nil {
			return json.Unmarshal(res.Result, out)
		}
		return nil
	}
	return &HTTPError{Status: 401, Lang: in.Lang}
}

func (d deluge) login(ctx context.Context, in *Instance) error {
	var ok bool
	if err := d.rpc(ctx, in, "auth.login", []any{in.Values["password"]}, &ok); err != nil {
		return err
	}
	if !ok {
		return in.Errorf("Falsches Passwort")
	}
	// The web UI has to be connected to a daemon; connect to the first one.
	var connected bool
	d.rpc(ctx, in, "web.connected", []any{}, &connected)
	if !connected {
		var hosts [][]any
		if err := d.rpc(ctx, in, "web.get_hosts", []any{}, &hosts); err == nil && len(hosts) > 0 {
			d.rpc(ctx, in, "web.connect", []any{hosts[0][0]}, nil)
		}
	}
	return nil
}

type delugeTorrent struct {
	Name     string  `json:"name"`
	Progress float64 `json:"progress"` // 0..100
	Rate     float64 `json:"download_payload_rate"`
	ETA      float64 `json:"eta"`
	State    string  `json:"state"`
	Size     float64 `json:"total_size"`
}

func (d deluge) ui(ctx context.Context, in *Instance) (map[string]delugeTorrent, float64, float64, error) {
	var res struct {
		Connected bool                     `json:"connected"`
		Torrents  map[string]delugeTorrent `json:"torrents"`
		Stats     struct {
			Down float64 `json:"download_rate"`
			Up   float64 `json:"upload_rate"`
		} `json:"stats"`
	}
	keys := []string{"name", "progress", "download_payload_rate", "eta", "state", "total_size"}
	if err := d.rpc(ctx, in, "web.update_ui", []any{keys, map[string]any{}}, &res); err != nil {
		return nil, 0, 0, err
	}
	if !res.Connected {
		return nil, 0, 0, in.Errorf("Deluge ist mit keinem Daemon verbunden – in der Weboberfläche im Verbindungsmanager verbinden")
	}
	return res.Torrents, res.Stats.Down, res.Stats.Up, nil
}

func (d deluge) item(in *Instance, t delugeTorrent) Item {
	state := map[string]string{"Downloading": in.Status("downloading"), "Seeding": in.T("Seedet"), "Paused": in.Status("paused"),
		"Queued": in.Status("queued"), "Checking": in.Status("verifying"), "Error": in.Status("failed")}[t.State]
	if state == "" {
		state = t.State
	}
	sub := []string{state}
	if t.Rate > 0 {
		sub = append(sub, in.Bytes(t.Rate)+"/s")
	}
	if eta := torrentEta(in, t.ETA); eta != "" && t.Progress < 100 {
		sub = append(sub, eta)
	}
	it := Item{Title: t.Name, Subtitle: strings.Join(sub, " · "), Badge: in.Bytes(t.Size), URL: in.Link("/")}
	if t.Progress < 100 {
		it.Progress = pct(t.Progress)
	}
	return it
}

func (d deluge) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	all, down, up, err := d.ui(ctx, in)
	if err != nil {
		return nil, err
	}
	downloading, seeding, paused, errs := 0, 0, 0, 0
	var active []delugeTorrent
	for _, t := range all {
		switch t.State {
		case "Downloading", "Queued":
			downloading++
			active = append(active, t)
		case "Seeding":
			seeding++
		case "Paused":
			paused++
		case "Error":
			errs++
		}
	}
	w := &Widget{Stats: []Stat{
		{Label: "↓", Value: in.Bytes(down) + "/s"},
		{Label: "↑", Value: in.Bytes(up) + "/s"},
		{Label: in.T("Lädt"), Value: in.Num(downloading)},
		{Label: in.T("Seedet"), Value: in.Num(seeding)},
	}}
	if errs > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Fehler"), Value: in.Num(errs), Tone: "bad"})
	}
	sort.Slice(active, func(i, j int) bool { return active[i].Rate > active[j].Rate })
	w.ItemsTitle = in.T("Downloads")
	for i, t := range active {
		if i == 5 {
			break
		}
		w.Items = append(w.Items, d.item(in, t))
	}
	if len(all) > 0 && paused == len(all) {
		w.Actions = []Action{{ID: "start", Label: in.T("Alle fortsetzen"), Tone: "primary"}}
	} else if len(all) > 0 {
		w.Actions = []Action{{ID: "stop", Label: in.T("Alle pausieren")}}
	}
	return w, nil
}

func (d deluge) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	all, _, _, err := d.ui(ctx, in)
	if err != nil {
		return nil, err
	}
	out := []Item{}
	for _, t := range all {
		if strings.Contains(strings.ToLower(t.Name), strings.ToLower(query)) {
			out = append(out, d.item(in, t))
			if len(out) == 8 {
				break
			}
		}
	}
	return out, nil
}

func (d deluge) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	switch action {
	case "stop":
		return d.rpc(ctx, in, "core.pause_session", []any{}, nil)
	case "start":
		// Resume the session and every paused torrent.
		if err := d.rpc(ctx, in, "core.resume_session", []any{}, nil); err != nil {
			return err
		}
		return d.rpc(ctx, in, "core.resume_torrents", []any{nil}, nil)
	}
	return in.Errorf("unbekannte Aktion %q", action)
}

// ---- NZBGet ----

type nzbget struct{}

func init() { register(nzbget{}) }

func (nzbget) Def() Definition {
	return Definition{
		ID: "nzbget", Name: "NZBGet", Icon: "nzbget", Category: "Downloads",
		Description: "Download-Geschwindigkeit, Warteschlange, Restgröße, Pause/Fortsetzen",
		Fields: []Field{
			{Key: "username", Label: "Benutzername", Type: "text", Required: true, Help: "Settings → Security → ControlUsername"},
			{Key: "password", Label: "Passwort", Type: "password", Required: true, Secret: true},
		},
		HasWidget: true, Images: []string{"nzbget"}, Port: 6789,
	}
}

func (n nzbget) rpc(ctx context.Context, in *Instance, method string, params []any, out any) error {
	var res struct {
		Result json.RawMessage `json:"result"`
		Error  *struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	err := in.do(ctx, request{method: "POST", path: "/jsonrpc", body: map[string]any{"method": method, "params": params, "id": 1},
		headers: map[string]string{"Authorization": "Basic " + basicAuth(in.V("username"), in.Values["password"])}}, &res)
	if err != nil {
		return err
	}
	if res.Error != nil {
		return errors.New("NZBGet: " + res.Error.Message)
	}
	if out != nil {
		return json.Unmarshal(res.Result, out)
	}
	return nil
}

func lohi(lo, hi float64) float64 { return hi*4294967296 + lo }

func (n nzbget) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var st struct {
		DownloadRate    float64 `json:"DownloadRate"`
		DownloadRateLo  float64 `json:"DownloadRateLo"`
		DownloadRateHi  float64 `json:"DownloadRateHi"`
		RemainingSizeLo float64 `json:"RemainingSizeLo"`
		RemainingSizeHi float64 `json:"RemainingSizeHi"`
		DownloadPaused  bool    `json:"DownloadPaused"`
		FreeDiskSpaceLo float64 `json:"FreeDiskSpaceLo"`
		FreeDiskSpaceHi float64 `json:"FreeDiskSpaceHi"`
	}
	if err := n.rpc(ctx, in, "status", []any{}, &st); err != nil {
		return nil, err
	}
	var groups []struct {
		ID       int     `json:"NZBID"`
		Name     string  `json:"NZBName"`
		Status   string  `json:"Status"`
		FileMB   float64 `json:"FileSizeMB"`
		RemainMB float64 `json:"RemainingSizeMB"`
		PausedMB float64 `json:"PausedSizeMB"`
		Health   int     `json:"Health"`
	}
	if err := n.rpc(ctx, in, "listgroups", []any{0}, &groups); err != nil {
		return nil, err
	}
	rate := st.DownloadRate
	if st.DownloadRateLo > 0 || st.DownloadRateHi > 0 {
		rate = lohi(st.DownloadRateLo, st.DownloadRateHi)
	}
	remaining := lohi(st.RemainingSizeLo, st.RemainingSizeHi)
	w := &Widget{ItemsTitle: in.T("Warteschlange")}
	w.Stats = []Stat{
		{Label: in.T("Tempo"), Value: in.Bytes(rate) + "/s"},
		{Label: in.T("Downloads"), Value: in.Num(len(groups))},
		{Label: in.T("Verbleibend"), Value: in.Bytes(remaining)},
	}
	if rate > 0 && remaining > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Restzeit"), Value: shortDuration(int(remaining / rate))})
	}
	if free := lohi(st.FreeDiskSpaceLo, st.FreeDiskSpaceHi); free > 0 && free < 10*1024*1024*1024 {
		w.Alerts = append(w.Alerts, in.T("Wenig freier Speicher: %s", in.Bytes(free)))
	}
	for i, g := range groups {
		if i == 5 {
			break
		}
		it := Item{Title: g.Name, Subtitle: in.Status(strings.ToLower(g.Status)), Badge: in.Bytes(g.FileMB * 1024 * 1024), URL: in.Link("/")}
		if g.FileMB > 0 {
			it.Progress = pct((g.FileMB - g.RemainMB) / g.FileMB * 100)
		}
		if g.Health > 0 && g.Health < 1000 {
			it.Subtitle += " · " + in.T("Zustand %s", fmt.Sprintf("%.1f %%", float64(g.Health)/10))
		}
		w.Items = append(w.Items, it)
	}
	if st.DownloadPaused {
		w.Alerts = append(w.Alerts, in.T("Downloads sind pausiert"))
		w.Actions = []Action{{ID: "resume", Label: in.T("Fortsetzen"), Tone: "primary"}}
	} else {
		w.Actions = []Action{{ID: "pause", Label: in.T("Pausieren")}}
	}
	return w, nil
}

func (n nzbget) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	switch action {
	case "pause":
		return n.rpc(ctx, in, "pausedownload", []any{}, nil)
	case "resume":
		return n.rpc(ctx, in, "resumedownload", []any{}, nil)
	}
	return in.Errorf("unbekannte Aktion %q", action)
}

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}
