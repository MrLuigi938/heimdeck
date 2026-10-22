package integrations

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AdGuard Home: REST API below /control with HTTP Basic auth
// (github.com/AdguardTeam/AdGuardHome/blob/master/openapi/openapi.yaml).

type adguard struct{}

func init() { register(adguard{}) }

func (adguard) Def() Definition {
	return Definition{
		ID: "adguard", Name: "AdGuard Home", Icon: "adguard-home", Category: "Netzwerk",
		Description: "DNS-Anfragen, Blockierquote, Schutz pausieren, Abfrage-Log durchsuchen und Domains sperren oder freigeben",
		Fields: []Field{
			{Key: "username", Label: "Benutzername", Type: "text", Required: true},
			{Key: "password", Label: "Passwort", Type: "password", Required: true, Secret: true},
		},
		HasWidget: true, HasSearch: true, Images: []string{"adguardhome", "adguard-home"}, Port: 3000,
	}
}

func (adguard) call(ctx context.Context, in *Instance, method, path string, q url.Values, body, out any) error {
	auth := base64.StdEncoding.EncodeToString([]byte(in.V("username") + ":" + in.Values["password"]))
	return in.do(ctx, request{method: method, path: "/control" + path, query: q, body: body,
		headers: map[string]string{"Authorization": "Basic " + auth}}, out)
}

type adguardStatus struct {
	Protection bool   `json:"protection_enabled"`
	DisabledMS int64  `json:"protection_disabled_duration"`
	Running    bool   `json:"running"`
	Version    string `json:"version"`
}

func (a adguard) Test(ctx context.Context, in *Instance) error {
	var st adguardStatus
	return a.call(ctx, in, "GET", "/status", nil, nil, &st)
}

func (a adguard) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var st adguardStatus
	if err := a.call(ctx, in, "GET", "/status", nil, nil, &st); err != nil {
		return nil, err
	}
	var stats struct {
		Queries    int                  `json:"num_dns_queries"`
		Blocked    int                  `json:"num_blocked_filtering"`
		Safebrowse int                  `json:"num_replaced_safebrowsing"`
		Parental   int                  `json:"num_replaced_parental"`
		AvgTime    float64              `json:"avg_processing_time"`
		TopBlocked []map[string]float64 `json:"top_blocked_domains"`
		TopClients []map[string]float64 `json:"top_clients"`
	}
	if err := a.call(ctx, in, "GET", "/stats", nil, nil, &stats); err != nil {
		return nil, err
	}
	blocked := stats.Blocked + stats.Safebrowse + stats.Parental
	ratio := 0.0
	if stats.Queries > 0 {
		ratio = float64(blocked) / float64(stats.Queries) * 100
	}
	w := &Widget{Stats: []Stat{
		{Label: in.T("DNS-Anfragen"), Value: in.Num(stats.Queries)},
		{Label: in.T("Blockiert"), Value: in.Num(blocked)},
		{Label: in.T("Quote"), Value: percent(in, ratio)},
		{Label: in.T("Antwortzeit"), Value: fmt.Sprintf("%.0f ms", stats.AvgTime*1000)},
	}}
	if st.Protection {
		w.Stats = append(w.Stats, Stat{Label: in.T("Schutz"), Value: in.T("An"), Tone: "ok"})
		w.Actions = []Action{
			{ID: "pause", Label: in.T("5 Min. pausieren"), Params: map[string]string{"minutes": "5"}},
			{ID: "pause", Label: in.T("30 Min. pausieren"), Params: map[string]string{"minutes": "30"}},
			{ID: "pause", Label: in.T("Ausschalten"), Tone: "danger", Params: map[string]string{"minutes": "0"},
				Confirm: in.T("DNS-Schutz dauerhaft ausschalten?")},
		}
	} else {
		value := in.T("Aus")
		if st.DisabledMS > 0 {
			value = in.T("Pausiert") + " · " + in.T("noch %s", shortDuration(int(st.DisabledMS/1000)))
		}
		w.Stats = append(w.Stats, Stat{Label: in.T("Schutz"), Value: value, Tone: "bad"})
		w.Actions = []Action{{ID: "resume", Label: in.T("Schutz einschalten"), Tone: "primary"}}
	}
	if !st.Running {
		w.Alerts = append(w.Alerts, in.T("Der DNS-Server läuft nicht"))
	}
	if clients := adguardTop(stats.TopClients, 100); len(clients) > 0 {
		w.Stats = append(w.Stats,
			Stat{Label: in.T("Aktive Geräte"), Value: in.Num(len(clients))},
			Stat{Label: in.T("Meiste Anfragen"), Value: clients[0].key})
	}
	var filtering struct {
		Filters []struct {
			Enabled    bool `json:"enabled"`
			RulesCount int  `json:"rules_count"`
		} `json:"filters"`
		UserRules []string `json:"user_rules"`
	}
	if a.call(ctx, in, "GET", "/filtering/status", nil, nil, &filtering) == nil {
		lists, rules := 0, len(filtering.UserRules)
		for _, f := range filtering.Filters {
			if f.Enabled {
				lists++
				rules += f.RulesCount
			}
		}
		w.Stats = append(w.Stats, Stat{Label: in.T("Filterlisten"), Value: in.Num(lists)}, Stat{Label: in.T("Regeln"), Value: in.Num(rules)})
	}
	w.ItemsTitle = in.T("Am häufigsten blockiert")
	for _, e := range adguardTop(stats.TopBlocked, 5) {
		w.Items = append(w.Items, Item{Title: e.key, Badge: in.Num(int(e.n)),
			URL: in.Link("/#logs?search=" + url.QueryEscape(e.key))})
	}
	return w, nil
}

type adguardEntry struct {
	key string
	n   float64
}

// adguardTop flattens AdGuard's [{"domain": count}, …] lists.
func adguardTop(list []map[string]float64, max int) []adguardEntry {
	var out []adguardEntry
	for _, m := range list {
		for k, v := range m {
			out = append(out, adguardEntry{k, v})
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].n > out[j].n })
	if len(out) > max {
		out = out[:max]
	}
	return out
}

var domainRe = regexp.MustCompile(`^(?i)[a-z0-9_]([a-z0-9_-]{0,62}\.)*[a-z0-9_-]{1,63}\.?$`)

// Search looks through the query log by domain or client IP.
func (a adguard) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	var log struct {
		Data []struct {
			Question struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"question"`
			Client     string `json:"client"`
			ClientInfo *struct {
				Name string `json:"name"`
			} `json:"client_info"`
			Reason string `json:"reason"`
			Time   string `json:"time"`
		} `json:"data"`
	}
	if err := a.call(ctx, in, "GET", "/querylog", url.Values{"search": {query}, "limit": {"40"}}, nil, &log); err != nil {
		return nil, err
	}
	rules, _ := a.userRules(ctx, in)
	// The log has one line per request; show each domain once.
	seen := map[string]bool{}
	out := []Item{}
	for _, e := range log.Data {
		d := strings.TrimSuffix(e.Question.Name, ".")
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		client := e.Client
		if e.ClientInfo != nil && e.ClientInfo.Name != "" {
			client = e.ClientInfo.Name
		}
		blocked := strings.HasPrefix(e.Reason, "Filtered")
		sub := []string{client}
		if t, err := time.Parse(time.RFC3339Nano, e.Time); err == nil {
			sub = append(sub, in.Since(t))
		}
		it := Item{Title: d, Subtitle: strings.Join(sub, " · "), URL: in.Link("/#logs?search=" + url.QueryEscape(d))}
		params := map[string]string{"domain": d}
		switch {
		case slices.Contains(rules, "@@||"+d+"^"):
			it.Badge = in.T("Freigegeben")
			it.Actions = []Action{{ID: "unrule", Label: in.T("Freigabe aufheben"), Params: params}}
		case slices.Contains(rules, "||"+d+"^"):
			it.Badge = in.T("Gesperrt")
			it.Actions = []Action{{ID: "unrule", Label: in.T("Sperre aufheben"), Params: params}}
		case blocked:
			it.Badge = in.T("Blockiert")
			it.Actions = []Action{{ID: "allow", Label: in.T("Freigeben"), Tone: "primary", Params: params}}
		default:
			it.Actions = []Action{{ID: "block", Label: in.T("Sperren"), Tone: "danger", Params: params,
				Confirm: in.T("„%s“ für alle Geräte sperren?", d)}}
		}
		out = append(out, it)
		if len(out) == 8 {
			break
		}
	}
	return out, nil
}

func (a adguard) userRules(ctx context.Context, in *Instance) ([]string, error) {
	var f struct {
		UserRules []string `json:"user_rules"`
	}
	err := a.call(ctx, in, "GET", "/filtering/status", nil, nil, &f)
	return f.UserRules, err
}

func (a adguard) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	switch action {
	case "pause":
		min, err := strconv.Atoi(p["minutes"])
		if err != nil || min < 0 || min > 24*60 {
			return in.Errorf("ungültige Dauer")
		}
		body := map[string]any{"enabled": false}
		if min > 0 {
			body["duration"] = min * 60 * 1000
		}
		return a.call(ctx, in, "POST", "/protection", nil, body, nil)
	case "resume":
		return a.call(ctx, in, "POST", "/protection", nil, map[string]any{"enabled": true}, nil)
	case "block", "allow", "unrule":
		d := p["domain"]
		if !domainRe.MatchString(d) {
			return in.Errorf("ungültige Domain")
		}
		rules, err := a.userRules(ctx, in)
		if err != nil {
			return err
		}
		// Drop earlier rules for this domain so block and allow never clash.
		kept := slices.DeleteFunc(rules, func(r string) bool { return r == "||"+d+"^" || r == "@@||"+d+"^" })
		switch action {
		case "block":
			kept = append(kept, "||"+d+"^")
		case "allow":
			kept = append(kept, "@@||"+d+"^")
		}
		return a.call(ctx, in, "POST", "/filtering/set_rules", nil, map[string]any{"rules": kept}, nil)
	}
	return in.Errorf("unbekannte Aktion %q", action)
}

// percent formats a share with one decimal, e.g. "12,7 %".
func percent(in *Instance, v float64) string {
	s := fmt.Sprintf("%.1f %%", v)
	if in.Lang != "en" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}
