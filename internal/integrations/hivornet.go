package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// hivornet is the honeypot Hivornet (github.com/MrLuigi938/hivornet): what
// attacked it in a chosen time – connections, logins, commands, downloads,
// attackers, countries, active tarpits – and the latest attacks. It reads the
// hive's web API with an account of its own; the role "viewer" is enough.
type hivornet struct{}

func init() { register(hivornet{}) }

func (hivornet) Def() Definition {
	return Definition{
		ID: "hivornet", Name: "Hivornet", Icon: "🐝", Category: "Netzwerk",
		Description: "Honeypot: Angriffe, Logins, Befehle und Angreifer im gewählten Zeitraum, die letzten Angriffe und eine Suche nach IPs",
		Setup: []string{
			"Als lokale Adresse die Web-Oberfläche des Hive eintragen, z. B. http://192.168.178.20:8088.",
			"In Hivornet unter Benutzer ein Konto für Heimdeck mit der Rolle „viewer“ anlegen – damit kann Heimdeck nur lesen.",
			"Ist HIVE_ALLOWED_HOSTS gesetzt, muss die Adresse in Heimdeck einen dieser Namen verwenden.",
		},
		Fields: []Field{
			{Key: "username", Label: "Benutzername", Type: "text", Required: true},
			{Key: "password", Label: "Passwort", Type: "password", Required: true, Secret: true},
			{Key: "range", Label: "Zeitraum", Type: "select", Options: []string{"24h", "1h", "7d", "30d"}, Display: true},
			{Key: "showCountries", Label: "Häufigste Herkunftsländer", Type: "checkbox", Display: true},
		},
		HasWidget: true, HasSearch: true, Images: []string{"hivornet"}, Port: 8088, Refresh: 30,
	}
}

func (hivornet) login(ctx context.Context, in *Instance) (string, error) {
	body, _ := json.Marshal(map[string]string{"username": in.V("username"), "password": in.Values["password"]})
	status, h, _, err := in.rawDo(ctx, "POST", "/api/login", strings.NewReader(string(body)),
		map[string]string{"Content-Type": "application/json", "X-Requested-With": "hivornet"})
	if err != nil {
		return "", err
	}
	switch status {
	case http.StatusOK:
	case http.StatusUnauthorized:
		return "", in.Errorf("Benutzername oder Passwort falsch")
	case http.StatusTooManyRequests:
		return "", in.Errorf("Hivornet sperrt nach zu vielen Fehlversuchen für 15 Minuten")
	case http.StatusForbidden, http.StatusMisdirectedRequest, http.StatusBadRequest:
		return "", in.Errorf("Hivornet lehnt die Anfrage ab – steht der Name dieser Adresse in HIVE_ALLOWED_HOSTS?")
	default:
		return "", &HTTPError{Status: status, Lang: in.Lang}
	}
	for _, c := range (&http.Response{Header: h}).Cookies() {
		if c.Name == "hv_session" {
			cookie := c.Name + "=" + c.Value
			sessions.Store(sessionKey(in, "hivornet"), cookie)
			return cookie, nil
		}
	}
	return "", in.Errorf("Hivornet hat keine Sitzung zurückgegeben")
}

// get reads an API path, logging in (again) when needed.
func (hv hivornet) get(ctx context.Context, in *Instance, path string, q url.Values, out any) error {
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	for attempt := 0; attempt < 2; attempt++ {
		c, ok := sessions.Load(sessionKey(in, "hivornet"))
		if !ok {
			cookie, err := hv.login(ctx, in)
			if err != nil {
				return err
			}
			c = cookie
		}
		status, _, b, err := in.rawDo(ctx, "GET", path, nil, map[string]string{"Cookie": c.(string), "X-Requested-With": "hivornet"})
		if err != nil {
			return err
		}
		if status == http.StatusUnauthorized && attempt == 0 {
			sessions.Delete(sessionKey(in, "hivornet")) // the session ran out
			continue
		}
		if status != http.StatusOK {
			return &HTTPError{Status: status, Lang: in.Lang}
		}
		return json.Unmarshal(b, out)
	}
	return in.Errorf("Benutzername oder Passwort falsch")
}

func (hv hivornet) Test(ctx context.Context, in *Instance) error {
	sessions.Delete(sessionKey(in, "hivornet"))
	var me struct {
		Username string `json:"username"`
	}
	return hv.get(ctx, in, "/api/me", nil, &me)
}

// hvEvent is one row of /api/events.
type hvEvent struct {
	TS          int64  `json:"ts"`
	Service     string `json:"service"`
	Type        string `json:"type"`
	SrcIP       string `json:"src_ip"`
	DstPort     int    `json:"dst_port"`
	Username    string `json:"username"`
	Password    string `json:"password"`
	Data        any    `json:"data"`
	Country     string `json:"country"`
	CountryName string `json:"country_name"`
	City        string `json:"city"`
}

// field reads a value of the event's data (an object, or JSON text).
func (e hvEvent) field(k string) string {
	m, ok := e.Data.(map[string]any)
	if !ok {
		if s, isStr := e.Data.(string); isStr {
			json.Unmarshal([]byte(s), &m)
		}
	}
	if v, ok := m[k]; ok && v != nil {
		return strings.TrimSpace(fmt.Sprint(v))
	}
	return ""
}

func flag(cc string) string {
	cc = strings.ToUpper(strings.TrimSpace(cc))
	if len(cc) != 2 || cc[0] < 'A' || cc[0] > 'Z' || cc[1] < 'A' || cc[1] > 'Z' {
		return ""
	}
	return string(rune(0x1F1E6+int(cc[0]-'A'))) + string(rune(0x1F1E6+int(cc[1]-'A')))
}

// item turns an attack into an entry: who, where from, through which service, what they tried.
func (hivornet) item(in *Instance, e hvEvent) Item {
	where := strings.TrimSpace(strings.Join(strings.Fields(flag(e.Country)+" "+firstNonEmpty(e.CountryName, e.Country)), " "))
	what := ""
	switch e.Type {
	case "login":
		what = in.T("Login %s / %s", clipText(e.Username, 30), clipText(e.Password, 30))
	case "command":
		what = "$ " + clipText(e.field("input"), 80)
	case "download":
		what = "⬇ " + clipText(firstNonEmpty(e.field("url"), e.field("filename")), 80)
	case "request":
		what = clipText(strings.TrimSpace(e.field("method")+" "+e.field("path")), 80)
	case "payload":
		what = clipText(e.field("text"), 60)
	default:
		what = e.Type
	}
	svc := e.Service
	if e.DstPort > 0 {
		svc += fmt.Sprintf(" :%d", e.DstPort)
	}
	sub := strings.Join(nonEmpty(where, what), " · ")
	return Item{Title: e.SrcIP + " · " + svc, Subtitle: sub, Badge: in.Since(time.UnixMilli(e.TS)), URL: in.PublicURL}
}

func nonEmpty(v ...string) []string {
	var out []string
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			out = append(out, s)
		}
	}
	return out
}

func clipText(s string, n int) string {
	r := []rune(strings.Join(strings.Fields(s), " "))
	if len(r) <= n {
		return string(r)
	}
	return string(r[:n-1]) + "…"
}

func (hv hivornet) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	rng := in.V("range")
	switch rng {
	case "1h", "7d", "30d":
	default:
		rng = "24h"
	}
	var sum map[string]float64
	if err := hv.get(ctx, in, "/api/stats/summary", url.Values{"range": {rng}}, &sum); err != nil {
		return nil, err
	}
	n := func(k string) string { return in.Num(int(sum[k])) }
	label := map[string]string{"1h": in.T("1 Std."), "24h": in.T("24 Std."), "7d": in.T("7 Tage"), "30d": in.T("30 Tage")}[rng]
	w := &Widget{Stats: []Stat{
		{Label: in.T("Verbindungen (%s)", label), Value: n("connect")},
		{Label: in.T("Logins"), Value: n("login")},
		{Label: in.T("Befehle"), Value: n("command")},
		{Label: in.T("Downloads"), Value: n("download"), Tone: toneIf(sum["download"] > 0, "warn")},
		{Label: in.T("Angreifer"), Value: n("unique_ips")},
		{Label: in.T("Länder"), Value: n("countries")},
	}}
	if t := sum["active_tarpits"]; t > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Im Tarpit"), Value: in.Num(int(t)), Tone: "ok"})
	}
	if in.V("showCountries") == "true" {
		var top []struct {
			Key   string  `json:"key"`
			Label string  `json:"label"`
			N     float64 `json:"n"`
		}
		if hv.get(ctx, in, "/api/stats/top", url.Values{"field": {"country"}, "range": {rng}, "limit": {"5"}}, &top) == nil && len(top) > 0 {
			sort.SliceStable(top, func(i, j int) bool { return top[i].N > top[j].N })
			var parts []string
			for _, c := range top {
				parts = append(parts, strings.TrimSpace(flag(c.Key)+" "+c.Key)+" "+in.Num(int(c.N)))
			}
			w.Stats = append(w.Stats, Stat{Label: in.T("Häufigste Länder"), Value: strings.Join(parts, " · ")})
		}
	}
	var evs []hvEvent
	if err := hv.get(ctx, in, "/api/events", url.Values{"hide_noise": {"1"}, "limit": {"8"}}, &evs); err == nil {
		w.ItemsTitle = in.T("Letzte Angriffe")
		for _, e := range evs {
			w.Items = append(w.Items, hv.item(in, e))
		}
	}
	return w, nil
}

// Search finds attacks by IP, user name, password or content (last 7 days).
func (hv hivornet) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	var evs []hvEvent
	if err := hv.get(ctx, in, "/api/events", url.Values{"q": {query}, "range": {"7d"}, "hide_noise": {"1"}, "limit": {"10"}}, &evs); err != nil {
		return nil, err
	}
	out := make([]Item, 0, len(evs))
	for _, e := range evs {
		out = append(out, hv.item(in, e))
	}
	return out, nil
}
