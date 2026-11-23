package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// datasource: an address of its own to which scripts and services send
// numbers (temperatures, power, counters …). Heimdeck keeps them 90 days and
// shows the newest values and a chart – no Grafana, no InfluxDB needed.
// The values are stored by the server (store.Points); the integration reads
// them through the functions below.

type datasource struct{}

func init() { register(datasource{}) }

// DataPoint is one value sent to a data source.
type DataPoint struct {
	Name  string
	T     int64 // milliseconds
	Value float64
}

// The server hands the stored values in (set in server.New).
var (
	DataPoints       func(tileID int64, since time.Time) (map[string][]DataPoint, error)
	DataPointsLatest func(tileID int64) ([]DataPoint, error)
)

func (datasource) Def() Definition {
	return Definition{
		ID: "datasource", Name: "Datenquelle (eigene Werte)", Icon: "📈", Category: "Allgemein",
		Description: "Eine eigene Adresse, an die Skripte und Dienste Zahlen schicken – Heimdeck zeigt die neuesten Werte und ein Diagramm, ganz ohne Grafana",
		NoURL:       true,
		Setup: []string{
			"Heimdeck zeigt unten die Adresse dieser Kachel. Dorthin schicken Skripte, Home Assistant, Node-RED oder Telegraf ihre Werte per POST.",
			"Als JSON mit Name und Zahl, z. B. {\"temperatur\": 21.4, \"luftfeuchte\": 58} – oder {\"name\": \"temperatur\", \"value\": 21.4}.",
			"Im Format von InfluxDB (eine Zeile je Wert), z. B. „temperatur,raum=bad value=21.4“ – so schickt auch Telegraf mit dem Ausgang „http“.",
			"Oder einfach per GET: …?temperatur=21.4. Bis zu 20 Werte je Kachel; sie bleiben 90 Tage. Die Adresse enthält einen geheimen Schlüssel.",
		},
		Fields: []Field{
			{Key: "hookKey", Label: "Adresse für die Werte", Type: "hook"},
			{Key: "range", Label: "Zeitraum im Diagramm", Type: "select", Options: []string{"24 Stunden", "1 Stunde", "7 Tage", "30 Tage"}, Display: true},
			{Key: "kind", Label: "Diagramm", Type: "select", Options: []string{"Linie", "Fläche", "Balken", "kein Diagramm"}, Display: true},
			{Key: "unit", Label: "Einheit", Type: "text", Help: "Für alle Werte, z. B. °C – oder je Wert: temperatur=°C, luftfeuchte=%", Display: true},
			{Key: "names", Label: "Nur diese Werte", Type: "text", Help: "Namen mit Komma getrennt, z. B. temperatur, luftfeuchte – leer: alle", Display: true},
		},
		HasWidget: true, Refresh: 60,
	}
}

// ---- receiving ----

const dataNameMax = 40

// cleanName keeps names readable and short: letters, digits, space and . _ - : / °.
func cleanName(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len([]rune(s)) > dataNameMax {
		return "", false
	}
	for _, r := range s {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && !strings.ContainsRune(" ._-:/°", r) {
			return "", false
		}
	}
	return s, true
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

// ParseDataPoints reads values from a request: JSON (an object of numbers,
// {"name": …, "value": …} or a list of those), InfluxDB line protocol, or the
// query (?name=value). Entries that are not numbers are left out.
func ParseDataPoints(body []byte, query url.Values, now time.Time) ([]DataPoint, error) {
	var out []DataPoint
	add := func(name string, v float64, t int64) {
		if n, ok := cleanName(name); ok && finite(v) {
			if t <= 0 || t > now.Add(time.Hour).UnixMilli() {
				t = now.UnixMilli()
			}
			out = append(out, DataPoint{Name: n, T: t, Value: v})
		}
	}
	for k, vs := range query {
		if v, err := strconv.ParseFloat(strings.Replace(vs[0], ",", ".", 1), 64); err == nil {
			add(k, v, 0)
		}
	}
	text := strings.TrimSpace(string(body))
	switch {
	case text == "":
	case strings.HasPrefix(text, "{") || strings.HasPrefix(text, "["):
		var raw any
		if err := json.Unmarshal([]byte(text), &raw); err != nil {
			return nil, errors.New("Das JSON ist ungültig")
		}
		list, ok := raw.([]any)
		if !ok {
			list = []any{raw}
		}
		for _, e := range list {
			obj, ok := e.(map[string]any)
			if !ok {
				continue
			}
			if name, ok := obj["name"].(string); ok {
				if v, ok := obj["value"].(float64); ok {
					add(name, v, 0)
				}
				continue
			}
			for k, v := range obj {
				if f, ok := v.(float64); ok {
					add(k, f, 0)
				}
			}
		}
	default:
		for _, line := range strings.Split(text, "\n") {
			parseInfluxLine(strings.TrimSpace(line), add)
		}
	}
	if len(out) == 0 {
		return nil, errors.New("Keine Werte gefunden – erwartet z. B. {\"temperatur\": 21.4}")
	}
	return out, nil
}

// parseInfluxLine: "measurement[,tag=x] field=1.5[,other=2i] [timestamp ns]".
// The field "value" keeps the measurement's name, others become "measurement.field".
func parseInfluxLine(line string, add func(string, float64, int64)) {
	if line == "" || strings.HasPrefix(line, "#") {
		return
	}
	parts := strings.Fields(line)
	if len(parts) < 2 {
		return
	}
	measure, _, _ := strings.Cut(parts[0], ",")
	var ts int64
	if len(parts) >= 3 {
		if ns, err := strconv.ParseInt(parts[2], 10, 64); err == nil {
			ts = ns / 1e6
		}
	}
	for _, f := range strings.Split(parts[1], ",") {
		k, v, ok := strings.Cut(f, "=")
		if !ok {
			continue
		}
		v = strings.TrimSuffix(strings.TrimSuffix(v, "i"), "u")
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			continue
		}
		name := measure
		if k != "value" {
			name = measure + "." + k
		}
		add(name, n, ts)
	}
}

// ---- showing ----

var dsRanges = map[string]struct {
	d      time.Duration
	bucket time.Duration
}{
	"1 Stunde":   {time.Hour, time.Minute},
	"24 Stunden": {24 * time.Hour, 15 * time.Minute},
	"7 Tage":     {7 * 24 * time.Hour, 2 * time.Hour},
	"30 Tage":    {30 * 24 * time.Hour, 8 * time.Hour},
}

func (datasource) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	if DataPoints == nil || DataPointsLatest == nil {
		return nil, errors.New("not available")
	}
	unitOf := dsUnits(in.V("unit"))
	want := map[string]bool{}
	for _, n := range strings.Split(in.V("names"), ",") {
		if n = strings.TrimSpace(n); n != "" {
			want[n] = true
		}
	}
	shown := func(name string) bool { return len(want) == 0 || want[name] }
	latest, err := DataPointsLatest(in.TileID)
	if err != nil {
		return nil, err
	}
	w := &Widget{}
	var newest int64
	for _, p := range latest {
		if !shown(p.Name) || len(w.Stats) == 8 {
			continue
		}
		w.Stats = append(w.Stats, Stat{Label: p.Name, Value: in.dsValue(p.Value, unitOf(p.Name))})
		newest = max(newest, p.T)
	}
	if len(w.Stats) == 0 {
		w.Alerts = []string{in.T("Noch keine Werte – schicke sie an die Adresse dieser Kachel (siehe Kachel bearbeiten).")}
		return w, nil
	}
	w.Stats = append(w.Stats, Stat{Label: in.T("Zuletzt"), Value: in.Since(time.UnixMilli(newest))})

	kind := map[string]string{"Linie": "line", "Fläche": "area", "Balken": "stacked", "kein Diagramm": ""}[in.V("kind")]
	if in.V("kind") == "" {
		kind = "line"
	}
	if kind == "" {
		return w, nil
	}
	rg, ok := dsRanges[in.V("range")]
	if !ok {
		rg = dsRanges["24 Stunden"]
	}
	now := time.Now()
	from := now.Add(-rg.d).Truncate(rg.bucket)
	pts, err := DataPoints(in.TileID, from)
	if err != nil {
		return w, nil
	}
	names := make([]string, 0, len(pts))
	for n := range pts {
		if shown(n) {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) > 6 {
		names = names[:6]
	}
	c := dsChart(pts, names, from, now, rg.bucket)
	if c == nil {
		return w, nil
	}
	// One unit for the chart only when all its values share it.
	unit := unitOf(names[0])
	for _, n := range names[1:] {
		if unitOf(n) != unit {
			unit = ""
		}
	}
	c.ID, c.Kind, c.Unit = "values", kind, unit
	c.Title = strings.Join(names, " · ")
	if len(names) > 3 {
		c.Title = in.T("Verlauf")
	}
	w.Charts = []Chart{*c}
	return w, nil
}

// dsUnits reads "°C" (for all) or "temperatur=°C, luftfeuchte=%" (per value).
func dsUnits(spec string) func(string) string {
	spec = strings.TrimSpace(spec)
	if !strings.Contains(spec, "=") {
		return func(string) string { return spec }
	}
	m := map[string]string{}
	for _, part := range strings.Split(spec, ",") {
		if k, v, ok := strings.Cut(part, "="); ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return func(name string) string { return m[name] }
}

func (in *Instance) dsValue(v float64, unit string) string {
	s := strconv.FormatFloat(v, 'f', -1, 64)
	if math.Abs(v) >= 100 || v != math.Trunc(v*100)/100 {
		s = strconv.FormatFloat(v, 'f', map[bool]int{true: 0, false: 2}[math.Abs(v) >= 100], 64)
		s = strings.TrimRight(strings.TrimRight(s, "0"), ".")
		if s == "" || s == "-" {
			s = "0"
		}
	}
	if in.Lang != "en" {
		s = strings.Replace(s, ".", ",", 1)
	}
	if unit != "" {
		if unit == "%" {
			return s + " %"
		}
		return s + " " + unit
	}
	return s
}

// dsChart puts the values into buckets (the mean of each), from the first
// bucket with a value on; an empty bucket keeps the value before it.
func dsChart(pts map[string][]DataPoint, names []string, from, now time.Time, bucket time.Duration) *Chart {
	n := int(now.Sub(from)/bucket) + 1
	if n < 2 || len(names) == 0 {
		return nil
	}
	step := bucket.Milliseconds()
	first := n
	sums := make(map[string][]float64, len(names))
	counts := make(map[string][]int, len(names))
	for _, name := range names {
		sums[name], counts[name] = make([]float64, n), make([]int, n)
		for _, p := range pts[name] {
			i := int((p.T - from.UnixMilli()) / step)
			if i < 0 || i >= n {
				continue
			}
			sums[name][i] += p.Value
			counts[name][i]++
			first = min(first, i)
		}
	}
	// The line ends with the newest value, not at "now".
	last := first
	for _, name := range names {
		for i := n - 1; i > last; i-- {
			if counts[name][i] > 0 {
				last = i
				break
			}
		}
	}
	n = last + 1
	if first >= n-1 {
		first = max(0, n-2) // one point only: still a short line
		n = max(n, 2)
	}
	c := &Chart{Step: step}
	for i := first; i < n; i++ {
		c.Times = append(c.Times, from.UnixMilli()+int64(i)*step)
	}
	for _, name := range names {
		s := ChartSeries{Name: name}
		last, seen := 0.0, false
		for i := first; i < n; i++ {
			if counts[name][i] > 0 {
				last, seen = sums[name][i]/float64(counts[name][i]), true
			} else if !seen {
				// before the first value of this series: take its first one
				for j := i; j < n; j++ {
					if counts[name][j] > 0 {
						last = sums[name][j] / float64(counts[name][j])
						break
					}
				}
			}
			s.Values = append(s.Values, last)
		}
		c.Series = append(c.Series, s)
	}
	return c
}

// String for logs and tests.
func (p DataPoint) String() string { return fmt.Sprintf("%s=%g@%d", p.Name, p.Value, p.T) }
