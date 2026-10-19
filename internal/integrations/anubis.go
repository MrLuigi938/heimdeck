package integrations

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

// Anubis has no API, but it exports Prometheus metrics on a separate port
// (default :9090, github.com/TecharoHQ/anubis lib/anubis.go and lib/policy).
// The counters start at zero whenever Anubis restarts.

type anubis struct{}

func init() { register(anubis{}) }

func (anubis) Def() Definition {
	return Definition{
		ID: "anubis", Name: "Anubis", Icon: "anubis", Category: "Netzwerk",
		Description: "Bot-Schutz: ausgestellte, gelöste und fehlgeschlagene Challenges, häufigste Regeln (aus den Prometheus-Metriken)",
		Fields: []Field{{Key: "metrics", Label: "Metrik-Adresse", Type: "text", Required: true,
			Help: "Der Metrik-Port von Anubis, z. B. http://anubis:9090 (Variable METRICS_BIND, Standard :9090). Er sollte nur intern erreichbar sein."}},
		HasWidget: true, Images: []string{"techarohq/anubis", "anubis"}, Port: 8923,
	}
}

// metric is one sample line: name, labels and value.
type metric struct {
	name   string
	labels map[string]string
	value  float64
}

// parseMetrics reads the Prometheus text format (comments, histograms and
// escaped label values are handled well enough for counters).
func parseMetrics(r io.Reader) []metric {
	var out []metric
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' {
			continue
		}
		m := metric{labels: map[string]string{}}
		rest := line
		if i := strings.IndexByte(line, '{'); i >= 0 {
			j := strings.LastIndexByte(line, '}')
			if j < i {
				continue
			}
			m.name = line[:i]
			parseLabels(line[i+1:j], m.labels)
			rest = strings.TrimSpace(line[j+1:])
		} else {
			name, val, ok := strings.Cut(line, " ")
			if !ok {
				continue
			}
			m.name, rest = name, strings.TrimSpace(val)
		}
		val, _, _ := strings.Cut(rest, " ") // drop an optional timestamp
		v, err := strconv.ParseFloat(val, 64)
		if err != nil {
			continue
		}
		m.value = v
		out = append(out, m)
	}
	return out
}

func parseLabels(s string, into map[string]string) {
	for s != "" {
		key, rest, ok := strings.Cut(s, `="`)
		if !ok {
			return
		}
		var val strings.Builder
		i := 0
		for ; i < len(rest); i++ {
			c := rest[i]
			if c == '\\' && i+1 < len(rest) {
				i++
				switch rest[i] {
				case 'n':
					val.WriteByte('\n')
				default:
					val.WriteByte(rest[i])
				}
				continue
			}
			if c == '"' {
				break
			}
			val.WriteByte(c)
		}
		into[strings.TrimSpace(strings.TrimLeft(key, ","))] = val.String()
		if i+1 >= len(rest) {
			return
		}
		s = strings.TrimLeft(rest[i+1:], ", ")
	}
}

func (anubis) metrics(ctx context.Context, in *Instance) ([]metric, error) {
	u := strings.TrimRight(in.V("metrics"), "/")
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = "http://" + u
	}
	if !strings.HasSuffix(u, "/metrics") {
		u += "/metrics"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := in.Client.Do(req)
	if err != nil {
		return nil, friendlyNetErr(in.Lang, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, &HTTPError{Status: resp.StatusCode, Lang: in.Lang}
	}
	ms := parseMetrics(io.LimitReader(resp.Body, 20<<20))
	for _, m := range ms {
		if strings.HasPrefix(m.name, "anubis_") {
			return ms, nil
		}
	}
	return nil, in.Errorf("unter dieser Adresse läuft %s, nicht %s", in.T("ein anderer Dienst"), "Anubis")
}

func (a anubis) Test(ctx context.Context, in *Instance) error {
	_, err := a.metrics(ctx, in)
	return err
}

func (a anubis) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	ms, err := a.metrics(ctx, in)
	if err != nil {
		return nil, err
	}
	sum := map[string]float64{}
	rules := map[string]float64{}
	actions := map[string]string{}
	for _, m := range ms {
		sum[m.name] += m.value
		if m.name == "anubis_policy_results" && m.labels["rule"] != "" {
			rules[m.labels["rule"]] += m.value
			actions[m.labels["rule"]] = m.labels["action"]
		}
	}
	issued, passed, failed := sum["anubis_challenges_issued"], sum["anubis_challenges_validated"], sum["anubis_failed_validations"]
	w := &Widget{Stats: []Stat{
		{Label: in.T("Challenges"), Value: in.Num(int(issued))},
		{Label: in.T("Gelöst"), Value: in.Num(int(passed)), Tone: "ok"},
		{Label: in.T("Fehlgeschlagen"), Value: in.Num(int(failed)), Tone: toneIf(failed > 0, "warn")},
	}}
	if p := sum["anubis_proxied_requests_total"]; p > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Durchgelassen"), Value: in.Num(int(p))})
	}

	type rule struct {
		name string
		n    float64
	}
	var list []rule
	for k, v := range rules {
		if v > 0 {
			list = append(list, rule{k, v})
		}
	}
	sort.Slice(list, func(i, j int) bool { return list[i].n > list[j].n })
	w.ItemsTitle = in.T("Häufigste Regeln (seit Neustart)")
	for i, r := range list {
		if i == 5 {
			break
		}
		w.Items = append(w.Items, Item{Title: r.name, Subtitle: anubisAction(in, actions[r.name]), Badge: in.Num(int(r.n))})
	}
	return w, nil
}

func anubisAction(in *Instance, a string) string {
	switch strings.ToUpper(a) {
	case "ALLOW":
		return in.T("Erlauben")
	case "DENY":
		return in.T("Blockieren")
	case "CHALLENGE":
		return in.T("Challenge")
	case "WEIGH":
		return in.T("Gewichten")
	}
	return fmt.Sprint(a)
}
