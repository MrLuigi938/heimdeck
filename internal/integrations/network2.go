package integrations

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---- Pi-hole ----
// Pi-hole v6: REST API with a session (POST /api/auth). Sessions are limited
// (16 by default), so one is kept per address and only renewed on 401.
// Pi-hole v5 (admin/api.php with the API token) is used as a fallback.

type pihole struct{}

func init() { register(pihole{}) }

func (pihole) Def() Definition {
	return Definition{
		ID: "pihole", Name: "Pi-hole", Icon: "pi-hole", Category: "Netzwerk",
		Description: "DNS-Anfragen, Blockierquote, Blockieren pausieren, Abfrage-Log durchsuchen und Domains sperren oder freigeben",
		Fields: []Field{{Key: "password", Label: "App-Passwort", Type: "password", Secret: true,
			Help: "Pi-hole 6: Einstellungen → Web-Oberfläche/API → App-Passwort erstellen (oder das normale Passwort). Pi-hole 5: der API-Token. Leer lassen, wenn kein Passwort gesetzt ist."}},
		HasWidget: true, HasSearch: true, Images: []string{"pihole"}, Port: 80,
	}
}

func (p pihole) login(ctx context.Context, in *Instance) (string, error) {
	var res struct {
		Session struct {
			Valid   bool    `json:"valid"`
			SID     *string `json:"sid"`
			Message string  `json:"message"`
		} `json:"session"`
	}
	err := in.do(ctx, request{method: "POST", path: "/api/auth", body: map[string]string{"password": in.Values["password"]}}, &res)
	var he *HTTPError
	if errors.As(err, &he) && he.Status == 429 {
		return "", in.Errorf("Pi-hole: zu viele offene Sitzungen – in Pi-hole unter Einstellungen → API die Sitzungen aufräumen")
	}
	if errors.As(err, &he) && he.Status == 401 {
		return "", in.Errorf("Falsches Passwort")
	}
	if err != nil {
		return "", err
	}
	if !res.Session.Valid {
		return "", in.Errorf("Falsches Passwort")
	}
	sid := ""
	if res.Session.SID != nil {
		sid = *res.Session.SID
	}
	sessions.Store(sessionKey(in, "pihole"), sid)
	return sid, nil
}

// call talks to the v6 API; errLegacy means the server is Pi-hole 5.
var errLegacy = errors.New("pihole v5")

func (p pihole) call(ctx context.Context, in *Instance, method, path string, q url.Values, body, out any) error {
	for attempt := 0; attempt < 2; attempt++ {
		sid, ok := sessions.Load(sessionKey(in, "pihole"))
		if !ok {
			s, err := p.login(ctx, in)
			var he *HTTPError
			if errors.As(err, &he) && (he.Status == 404 || he.Status == 405) {
				return errLegacy
			}
			if err != nil {
				return err
			}
			sid = s
		}
		h := map[string]string{}
		if sid.(string) != "" {
			h["X-FTL-SID"] = sid.(string)
		}
		err := in.do(ctx, request{method: method, path: "/api" + path, query: q, body: body, headers: h}, out)
		var he *HTTPError
		if errors.As(err, &he) && he.Status == 401 && attempt == 0 {
			sessions.Delete(sessionKey(in, "pihole"))
			continue
		}
		return err
	}
	return &HTTPError{Status: 401, Lang: in.Lang}
}

func (p pihole) legacy(ctx context.Context, in *Instance, q string, out any) error {
	vals, _ := url.ParseQuery(q)
	vals.Set("auth", in.Values["password"])
	return in.do(ctx, request{path: "/admin/api.php", query: vals}, out)
}

func (p pihole) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var sum struct {
		Queries struct {
			Total     float64 `json:"total"`
			Blocked   float64 `json:"blocked"`
			Percent   float64 `json:"percent_blocked"`
			Forwarded float64 `json:"forwarded"`
			Cached    float64 `json:"cached"`
			Unique    float64 `json:"unique_domains"`
		} `json:"queries"`
		Clients struct {
			Active int `json:"active"`
			Total  int `json:"total"`
		} `json:"clients"`
		Gravity struct {
			Domains    float64 `json:"domains_being_blocked"`
			LastUpdate float64 `json:"last_update"`
		} `json:"gravity"`
	}
	legacy := false
	var blocking struct {
		Blocking string   `json:"blocking"`
		Timer    *float64 `json:"timer"`
	}
	err := p.call(ctx, in, "GET", "/stats/summary", nil, nil, &sum)
	if err == errLegacy {
		var v5 struct {
			Status  string  `json:"status"`
			Domains float64 `json:"domains_being_blocked"`
			Queries float64 `json:"dns_queries_today"`
			Blocked float64 `json:"ads_blocked_today"`
			Percent float64 `json:"ads_percentage_today"`
			Clients int     `json:"unique_clients"`
		}
		if err := p.legacy(ctx, in, "summaryRaw", &v5); err != nil {
			return nil, err
		}
		sum.Queries.Total, sum.Queries.Blocked, sum.Queries.Percent = v5.Queries, v5.Blocked, v5.Percent
		sum.Clients.Active, sum.Gravity.Domains = v5.Clients, v5.Domains
		blocking.Blocking = v5.Status
		legacy = true
	} else if err != nil {
		return nil, err
	} else {
		p.call(ctx, in, "GET", "/dns/blocking", nil, nil, &blocking)
	}
	w := &Widget{Stats: []Stat{
		{Label: in.T("DNS-Anfragen"), Value: in.Num(int(sum.Queries.Total))},
		{Label: in.T("Blockiert"), Value: in.Num(int(sum.Queries.Blocked))},
		{Label: in.T("Quote"), Value: percent(in, sum.Queries.Percent)},
		{Label: in.T("Clients"), Value: in.Num(sum.Clients.Active)},
	}}
	if blocking.Blocking == "disabled" {
		value := in.T("Aus")
		if blocking.Timer != nil && *blocking.Timer > 0 {
			value = in.T("Pausiert") + " · " + in.T("noch %s", shortDuration(int(*blocking.Timer)))
		}
		w.Stats = append(w.Stats, Stat{Label: in.T("Schutz"), Value: value, Tone: "bad"})
		w.Actions = []Action{{ID: "enable", Label: in.T("Schutz einschalten"), Tone: "primary"}}
	} else {
		w.Stats = append(w.Stats, Stat{Label: in.T("Schutz"), Value: in.T("An"), Tone: "ok"})
		w.Actions = []Action{
			{ID: "disable", Label: in.T("5 Min. pausieren"), Params: map[string]string{"minutes": "5"}},
			{ID: "disable", Label: in.T("30 Min. pausieren"), Params: map[string]string{"minutes": "30"}},
		}
	}
	w.Stats = append(w.Stats, Stat{Label: in.T("Domains auf der Sperrliste"), Value: in.Num(int(sum.Gravity.Domains))})
	if legacy {
		return w, nil
	}
	w.Stats = append(w.Stats,
		Stat{Label: in.T("Weitergeleitet"), Value: in.Num(int(sum.Queries.Forwarded))},
		Stat{Label: in.T("Aus dem Cache"), Value: in.Num(int(sum.Queries.Cached))},
		Stat{Label: in.T("Verschiedene Domains"), Value: in.Num(int(sum.Queries.Unique))},
	)
	if sum.Gravity.LastUpdate > 0 {
		upd := time.Unix(int64(sum.Gravity.LastUpdate), 0)
		w.Stats = append(w.Stats, Stat{Label: in.T("Listen aktualisiert"), Value: in.Since(upd), Tone: toneIf(time.Since(upd) > 14*24*time.Hour, "warn")})
	}
	// Updates of the three parts of Pi-hole.
	var ver struct {
		Version map[string]struct {
			Local struct {
				Version string `json:"version"`
			} `json:"local"`
			Remote struct {
				Version string `json:"version"`
			} `json:"remote"`
		} `json:"version"`
	}
	if p.call(ctx, in, "GET", "/info/version", nil, nil, &ver) == nil {
		for _, part := range []string{"core", "web", "ftl"} {
			v, ok := ver.Version[part]
			if ok && v.Local.Version != "" && v.Remote.Version != "" && v.Local.Version != v.Remote.Version {
				w.Alerts = append(w.Alerts, in.T("Update verfügbar: %s %s", "Pi-hole "+map[string]string{"core": "Core", "web": "Web", "ftl": "FTL"}[part], v.Remote.Version))
			}
		}
	}
	// The domains blocked most often today.
	var top struct {
		Domains []struct {
			Domain string `json:"domain"`
			Count  int    `json:"count"`
		} `json:"domains"`
	}
	if p.call(ctx, in, "GET", "/stats/top_domains", url.Values{"blocked": {"true"}, "count": {"5"}}, nil, &top) == nil && len(top.Domains) > 0 {
		w.ItemsTitle = in.T("Am häufigsten blockiert")
		for _, d := range top.Domains {
			w.Items = append(w.Items, Item{Title: d.Domain, Badge: in.Num(d.Count), URL: in.Link("/admin/queries?domain=" + url.QueryEscape(d.Domain))})
		}
	}
	return w, nil
}

func (p pihole) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	var res struct {
		Queries []struct {
			Domain string  `json:"domain"`
			Status string  `json:"status"`
			Time   float64 `json:"time"`
			Client struct {
				IP   string `json:"ip"`
				Name string `json:"name"`
			} `json:"client"`
		} `json:"queries"`
	}
	err := p.call(ctx, in, "GET", "/queries", url.Values{"domain": {"*" + query + "*"}, "length": {"50"}}, nil, &res)
	if err == errLegacy {
		return nil, ErrNoSearch // Pi-hole 5 has no search API
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []Item{}
	for _, q := range res.Queries {
		if q.Domain == "" || seen[q.Domain] {
			continue
		}
		seen[q.Domain] = true
		client := q.Client.Name
		if client == "" {
			client = q.Client.IP
		}
		sub := client
		if q.Time > 0 {
			sub += " · " + in.Since(time.Unix(int64(q.Time), 0))
		}
		it := Item{Title: q.Domain, Subtitle: sub, URL: in.Link("/admin/queries?domain=" + url.QueryEscape(q.Domain))}
		params := map[string]string{"domain": q.Domain}
		blocked := strings.Contains(q.Status, "GRAVITY") || strings.Contains(q.Status, "DENYLIST") || strings.Contains(q.Status, "REGEX") || strings.Contains(q.Status, "BLACKLIST")
		if blocked {
			it.Badge = in.T("Blockiert")
			it.Actions = []Action{{ID: "allow", Label: in.T("Freigeben"), Tone: "primary", Params: params}}
		} else {
			it.Actions = []Action{{ID: "deny", Label: in.T("Sperren"), Tone: "danger", Params: params, Confirm: in.T("„%s“ für alle Geräte sperren?", q.Domain)}}
		}
		out = append(out, it)
		if len(out) == 8 {
			break
		}
	}
	return out, nil
}

func (p pihole) Action(ctx context.Context, in *Instance, action string, params map[string]string) error {
	switch action {
	case "disable", "enable":
		body := map[string]any{"blocking": action == "enable"}
		min := 0
		if action == "disable" {
			var err error
			if min, err = strconv.Atoi(params["minutes"]); err != nil || min < 1 || min > 24*60 {
				return in.Errorf("ungültige Dauer")
			}
			body["timer"] = min * 60
		}
		err := p.call(ctx, in, "POST", "/dns/blocking", nil, body, nil)
		if err == errLegacy {
			q := "enable"
			if action == "disable" {
				q = "disable=" + strconv.Itoa(min*60)
			}
			return p.legacy(ctx, in, q, nil)
		}
		return err
	case "allow", "deny":
		d := params["domain"]
		if !domainRe.MatchString(d) {
			return in.Errorf("ungültige Domain")
		}
		return p.call(ctx, in, "POST", "/domains/"+action+"/exact", nil, map[string]any{"domain": d, "comment": "Heimdeck"}, nil)
	}
	return in.Errorf("unbekannte Aktion %q", action)
}

// ---- OPNsense ----

type opnsense struct{}

func init() { register(opnsense{}) }

func (opnsense) Def() Definition {
	return Definition{
		ID: "opnsense", Name: "OPNsense", Icon: "opnsense", Category: "Netzwerk",
		Description: "WAN-Datenrate, Arbeitsspeicher, Last, Laufzeit, Gateways und verfügbare Updates",
		Fields: []Field{
			{Key: "key", Label: "API-Key", Type: "text", Required: true,
				Help: "System → Zugang → Benutzer → Benutzer bearbeiten → API-Schlüssel „+“. Die heruntergeladene Datei enthält key und secret."},
			{Key: "secret", Label: "API-Secret", Type: "password", Required: true, Secret: true},
			{Key: "wan", Label: "WAN-Schnittstelle", Type: "text", Help: "Optional, Standard: wan"},
		},
		HasWidget: true, Port: 443,
	}
}

func (o opnsense) get(ctx context.Context, in *Instance, path string, out any) error {
	return in.do(ctx, request{path: "/api" + path,
		headers: map[string]string{"Authorization": "Basic " + basicAuth(in.V("key"), in.Values["secret"])}}, out)
}

func (o opnsense) Test(ctx context.Context, in *Instance) error {
	var v map[string]any
	return o.get(ctx, in, "/core/firmware/status", &v)
}

type trafficSample struct {
	at     float64
	rx, tx float64
}

var opnTraffic sync.Map // BaseURL → trafficSample

func numStr(v any) float64 {
	switch x := v.(type) {
	case float64:
		return x
	case string:
		f, _ := strconv.ParseFloat(x, 64)
		return f
	}
	return 0
}

func (o opnsense) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var fw struct {
		Status  string `json:"status"`
		Product struct {
			Version string `json:"product_version"`
			Latest  string `json:"product_latest"`
		} `json:"product"`
	}
	if err := o.get(ctx, in, "/core/firmware/status", &fw); err != nil {
		return nil, err
	}
	w := &Widget{}

	// WAN rate from the difference of the cumulative counters.
	var traffic struct {
		Interfaces map[string]map[string]any `json:"interfaces"`
		Time       float64                   `json:"time"`
	}
	if o.get(ctx, in, "/diagnostics/traffic/interface", &traffic) == nil {
		wan := strings.ToLower(in.V("wan"))
		if wan == "" {
			wan = "wan"
		}
		if ifc, ok := traffic.Interfaces[wan]; ok {
			cur := trafficSample{at: traffic.Time, rx: numStr(ifc["bytes received"]), tx: numStr(ifc["bytes transmitted"])}
			if prev, ok := opnTraffic.Load(in.BaseURL); ok {
				p := prev.(trafficSample)
				if dt := cur.at - p.at; dt > 0 && cur.rx >= p.rx && cur.tx >= p.tx {
					w.Stats = append(w.Stats,
						Stat{Label: "WAN ↓", Value: in.Bytes((cur.rx-p.rx)/dt) + "/s"},
						Stat{Label: "WAN ↑", Value: in.Bytes((cur.tx-p.tx)/dt) + "/s"})
				}
			}
			opnTraffic.Store(in.BaseURL, cur)
		}
	}

	var res struct {
		Memory struct {
			Total any     `json:"total"`
			Used  float64 `json:"used"`
		} `json:"memory"`
	}
	if o.get(ctx, in, "/diagnostics/system/system_resources", &res) == nil {
		if total := numStr(res.Memory.Total); total > 0 {
			w.Stats = append(w.Stats, Stat{Label: in.T("RAM"), Value: fmt.Sprintf("%.0f %%", res.Memory.Used/total*100)})
		}
	}
	var tm struct {
		Uptime  string `json:"uptime"`
		LoadAvg string `json:"loadavg"`
	}
	if o.get(ctx, in, "/diagnostics/system/system_time", &tm) == nil {
		if load, _, _ := strings.Cut(tm.LoadAvg, ","); load != "" {
			w.Stats = append(w.Stats, Stat{Label: in.T("Last"), Value: strings.TrimSpace(load)})
		}
		if tm.Uptime != "" {
			w.Stats = append(w.Stats, Stat{Label: in.T("Laufzeit"), Value: tm.Uptime})
		}
	}
	if fw.Status == "update" || fw.Status == "upgrade" {
		w.Stats = append(w.Stats, Stat{Label: in.T("Updates"), Value: fw.Product.Latest, Tone: "warn"})
	}
	if fw.Product.Version != "" {
		w.Stats = append(w.Stats, Stat{Label: in.T("Version"), Value: fw.Product.Version})
	}
	// The hottest sensor.
	var temps []struct {
		Device      string `json:"device"`
		Temperature any    `json:"temperature"`
	}
	if o.get(ctx, in, "/diagnostics/system/system_temperature", &temps) == nil {
		hot := 0.0
		for _, t := range temps {
			hot = max(hot, numStr(t.Temperature))
		}
		if hot > 0 {
			w.Stats = append(w.Stats, Stat{Label: in.T("Temperatur"), Value: fmt.Sprintf("%.0f °C", hot), Tone: toneIf(hot >= 80, "warn")})
		}
	}
	// Disk usage of the system partition.
	var disk struct {
		Devices []struct {
			Mount   string `json:"mountpoint"`
			UsedPct any    `json:"used_pct"`
		} `json:"devices"`
	}
	if o.get(ctx, in, "/diagnostics/system/system_disk", &disk) == nil {
		for _, d := range disk.Devices {
			if d.Mount == "/" {
				used := numStr(d.UsedPct)
				w.Stats = append(w.Stats, Stat{Label: in.T("Festplatte"), Value: fmt.Sprintf("%.0f %%", used), Tone: toneIf(used >= 90, "warn")})
			}
		}
	}

	var gw struct {
		Items []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Trans  string `json:"status_translated"`
			Delay  string `json:"delay"`
			Loss   string `json:"loss"`
		} `json:"items"`
	}
	if o.get(ctx, in, "/routes/gateway/status", &gw) == nil && len(gw.Items) > 0 {
		w.ItemsTitle = in.T("Gateways")
		sort.SliceStable(gw.Items, func(i, j int) bool { return gw.Items[i].Status != "none" && gw.Items[j].Status == "none" })
		for _, g := range gw.Items {
			it := Item{Title: g.Name, URL: in.Link("/ui/routing/configuration")}
			if g.Delay != "" && g.Delay != "~" {
				it.Subtitle = in.T("Latenz") + " " + g.Delay + " · " + in.T("Verlust") + " " + g.Loss
			}
			if g.Status == "none" {
				it.Badge = in.T("Online")
			} else {
				it.Badge = g.Trans
				if it.Badge == "" {
					it.Badge = g.Status
				}
				if strings.Contains(g.Status, "down") {
					w.Alerts = append(w.Alerts, in.T("Gateway %s ist offline", g.Name))
				}
			}
			w.Items = append(w.Items, it)
			if len(w.Items) == 5 {
				break
			}
		}
	}
	return w, nil
}

// ---- Tdarr ----

type tdarr struct{}

func init() { register(tdarr{}) }

func (tdarr) Def() Definition {
	return Definition{
		ID: "tdarr", Name: "Tdarr", Icon: "tdarr", Category: "Medien",
		Description: "Transcodes und Health-Checks, gesparter Speicher, laufende Worker mit Fortschritt",
		Fields: []Field{{Key: "apiKey", Label: "API-Key", Type: "password", Secret: true,
			Help: "Nur nötig, wenn in Tdarr die Authentifizierung aktiv ist"}},
		HasWidget: true, Images: []string{"tdarr"}, Port: 8265,
	}
}

func (t tdarr) call(ctx context.Context, in *Instance, method, path string, body, out any) error {
	h := map[string]string{}
	if k := in.V("apiKey"); k != "" {
		h["x-api-key"] = k
	}
	return in.do(ctx, request{method: method, path: "/api/v2" + path, body: body, headers: h}, out)
}

func (t tdarr) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	w := &Widget{}
	var pies struct {
		PieStats struct {
			TotalFiles     float64 `json:"totalFiles"`
			TotalTranscode float64 `json:"totalTranscodeCount"`
			TotalHealth    float64 `json:"totalHealthCheckCount"`
			SizeDiff       float64 `json:"sizeDiff"`
			Status         struct {
				Transcode []struct {
					Name  string  `json:"name"`
					Value float64 `json:"value"`
				} `json:"transcode"`
				Healthcheck []struct {
					Name  string  `json:"name"`
					Value float64 `json:"value"`
				} `json:"healthcheck"`
			} `json:"status"`
		} `json:"pieStats"`
	}
	err := t.call(ctx, in, "POST", "/stats/get-pies", map[string]any{"data": map[string]string{"libraryId": ""}}, &pies)
	if err == nil {
		ps := pies.PieStats
		queued, failed := 0.0, 0.0
		for _, s := range ps.Status.Transcode {
			n := strings.ToLower(s.Name)
			switch {
			case strings.Contains(n, "queue"):
				queued += s.Value
			case strings.Contains(n, "error"):
				failed += s.Value
			}
		}
		w.Stats = []Stat{
			{Label: in.T("Dateien"), Value: in.Num(int(ps.TotalFiles))},
			{Label: in.T("Transcodes"), Value: in.Num(int(ps.TotalTranscode))},
			{Label: in.T("Wartet"), Value: in.Num(int(queued))},
			{Label: in.T("Gespart"), Value: in.Bytes(ps.SizeDiff * 1e9)},
		}
		if failed > 0 {
			w.Stats = append(w.Stats, Stat{Label: in.T("Fehler"), Value: in.Num(int(failed)), Tone: "bad"})
		}
		unhealthy := 0.0
		for _, s := range ps.Status.Healthcheck {
			if strings.Contains(strings.ToLower(s.Name), "error") {
				unhealthy += s.Value
			}
		}
		w.Stats = append(w.Stats, Stat{Label: in.T("Health-Checks"), Value: in.Num(int(ps.TotalHealth))})
		if unhealthy > 0 {
			w.Stats = append(w.Stats, Stat{Label: in.T("Defekte Dateien"), Value: in.Num(int(unhealthy)), Tone: "bad"})
		}
	} else {
		// Older servers: the statistics document.
		var st map[string]any
		if err2 := t.call(ctx, in, "POST", "/cruddb", map[string]any{"data": map[string]string{"collection": "StatisticsJSONDB", "mode": "getById", "docID": "statistics"}}, &st); err2 != nil {
			return nil, err
		}
		tbl := func(n int) float64 {
			if v, ok := st[fmt.Sprintf("table%dViewableCount", n)]; ok {
				return numStr(v)
			}
			return numStr(st[fmt.Sprintf("table%dCount", n)])
		}
		w.Stats = []Stat{
			{Label: in.T("Wartet"), Value: in.Num(int(tbl(1) + tbl(4)))},
			{Label: in.T("Fertig"), Value: in.Num(int(tbl(2) + tbl(5)))},
			{Label: in.T("Fehler"), Value: in.Num(int(tbl(3) + tbl(6))), Tone: toneIf(tbl(3)+tbl(6) > 0, "bad")},
			{Label: in.T("Gespart"), Value: in.Bytes(numStr(st["sizeDiff"]) * 1e9)},
		}
	}

	var nodes map[string]struct {
		Name    string `json:"nodeName"`
		Paused  bool   `json:"nodePaused"`
		Workers map[string]struct {
			File       string  `json:"file"`
			Percentage float64 `json:"percentage"`
			FPS        float64 `json:"fps"`
			ETA        string  `json:"ETA"`
			Job        struct {
				Type string `json:"type"`
			} `json:"job"`
		} `json:"workers"`
	}
	if t.call(ctx, in, "GET", "/get-nodes", nil, &nodes) == nil {
		active := 0
		for _, n := range nodes {
			for _, wk := range n.Workers {
				active++
				name := wk.File
				if i := strings.LastIndexAny(name, `/\`); i >= 0 {
					name = name[i+1:]
				}
				sub := []string{n.Name}
				if wk.FPS > 0 {
					sub = append(sub, fmt.Sprintf("%.0f fps", wk.FPS))
				}
				if wk.ETA != "" {
					sub = append(sub, in.T("noch %s", wk.ETA))
				}
				w.Items = append(w.Items, Item{Title: name, Subtitle: strings.Join(sub, " · "), Progress: pct(wk.Percentage), URL: in.Link("/")})
			}
			if n.Paused {
				w.Alerts = append(w.Alerts, in.T("Node %s ist pausiert", n.Name))
			}
		}
		w.Stats = append(w.Stats, Stat{Label: in.T("Worker aktiv"), Value: in.Num(active)}, Stat{Label: in.T("Nodes"), Value: in.Num(len(nodes))})
		if len(w.Items) > 0 {
			w.ItemsTitle = in.T("Läuft gerade")
			sort.Slice(w.Items, func(i, j int) bool { return *w.Items[i].Progress > *w.Items[j].Progress })
			if len(w.Items) > 5 {
				w.Items = w.Items[:5]
			}
		}
	}
	return w, nil
}
