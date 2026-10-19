package integrations

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// customAPI turns any HTTP/JSON endpoint into a tile: values for the stats
// row, a list, buttons and a search, all described with paths into the JSON
// response (see jsonpath.go). It is meant for everything without its own
// integration.

type customAPI struct{}

func init() { register(customAPI{}) }

// ErrNoSearch tells the search that a tile has nothing to search in.
var ErrNoSearch = errors.New("no search configured")

func (customAPI) Def() Definition {
	return Definition{
		ID: "customapi", Name: "Eigene API", Icon: "🔌", Category: "Allgemein",
		Description: "Beliebige JSON-Schnittstelle als Kachel: eigene Werte, Liste, Knöpfe und Suche – für alles ohne eigene Integration",
		Fields: []Field{
			{Key: "path", Label: "Abfrage", Type: "text",
				Help: "Pfad unter der Adresse der Kachel (z. B. /api/status) oder eine vollständige URL. Leer: die Adresse selbst."},
			{Key: "method", Label: "Methode", Type: "select", Options: []string{"GET", "POST"}},
			{Key: "headers", Label: "Header", Type: "textarea", Secret: true,
				Help: "Ein Header pro Zeile, z. B. „Authorization: Bearer abc“ oder „X-Api-Key: abc“. Wird verschlüsselt gespeichert."},
			{Key: "username", Label: "Benutzername", Type: "text", Help: "Optional, für HTTP-Basic-Anmeldung"},
			{Key: "password", Label: "Passwort", Type: "password", Secret: true},
			{Key: "body", Label: "Body (bei POST)", Type: "textarea"},
			{Key: "stats", Label: "Werte", Type: "textarea",
				Help: "Ein Wert pro Zeile: Name = Pfad | Format. Beispiele: „Temperatur = data.temp | zahl:1 °C“, „Speicher = disk.used | bytes“, „Online = devices[online=true].#“, „Status = status | text ok=up bad=down“. Formate: text, zahl, bytes, prozent, anteil, dauer, datum, seit, janein. Farben: ok/warn/bad mit = != > < >= <=."},
			{Key: "list", Label: "Liste (Pfad zum Array)", Type: "text", Help: "Optional, z. B. data.items"},
			{Key: "item", Label: "Listeneintrag", Type: "textarea",
				Help: "Felder je Eintrag, eins pro Zeile: title = name, subtitle = status, badge = size | bytes, progress = percent, url = link. Mehrere Pfade mit + verbinden: subtitle = artist + album"},
			{Key: "buttons", Label: "Knöpfe", Type: "textarea",
				Help: "Ein Knopf pro Zeile: Beschriftung = METHODE Pfad, z. B. „Neu starten = POST /api/restart“. Fragt vor dem Ausführen nach."},
			{Key: "search", Label: "Suche", Type: "text",
				Help: "Optional: Pfad für die gemeinsame Suche mit {q} als Platzhalter, z. B. /api/search?q={q}. Die Treffer nutzen „Suche: Liste“ und „Listeneintrag“."},
			{Key: "searchList", Label: "Suche: Liste", Type: "text", Help: "Pfad zum Array in der Suchantwort, z. B. results"},
		},
		HasWidget: true, HasSearch: true,
	}
}

func (c customAPI) request(ctx context.Context, in *Instance, method, target string, body string) (any, error) {
	u := target
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		if u != "" && !strings.HasPrefix(u, "/") {
			u = "/" + u
		}
		u = strings.TrimRight(in.BaseURL, "/") + u
	}
	if method == "" {
		method = http.MethodGet
	}
	var r io.Reader
	if body != "" && method != http.MethodGet {
		r = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, r)
	if err != nil {
		return nil, in.Errorf("ungültige URL")
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if r != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if in.V("username") != "" {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(in.V("username")+":"+in.Values["password"])))
	}
	for _, line := range strings.Split(in.Values["headers"], "\n") {
		if k, v, ok := strings.Cut(line, ":"); ok && strings.TrimSpace(k) != "" {
			req.Header.Set(strings.TrimSpace(k), strings.TrimSpace(v))
		}
	}
	resp, err := in.Client.Do(req)
	if err != nil {
		return nil, friendlyNetErr(in.Lang, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b := strings.TrimSpace(string(raw))
		if len(b) > 200 {
			b = b[:200]
		}
		return nil, &HTTPError{Status: resp.StatusCode, Body: b, Lang: in.Lang}
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		// Plain text answers are available as "." (e.g. a version string).
		return strings.TrimSpace(string(raw)), nil
	}
	return v, nil
}

func (c customAPI) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	method := strings.ToUpper(in.V("method"))
	data, err := c.request(ctx, in, method, in.V("path"), in.Values["body"])
	if err != nil {
		return nil, err
	}
	w := &Widget{}
	for _, line := range lines(in.Values["stats"]) {
		label, expr, ok := strings.Cut(line, "=")
		if !ok {
			w.Alerts = append(w.Alerts, in.T("Zeile ohne „=“: %s", line))
			continue
		}
		val, tone, err := evalExpr(in, data, expr)
		if err != nil {
			val = "–"
		}
		w.Stats = append(w.Stats, Stat{Label: strings.TrimSpace(label), Value: val, Tone: tone})
	}
	if lp := in.V("list"); lp != "" {
		items, err := c.items(in, data, lp, 8)
		if err != nil {
			w.Alerts = append(w.Alerts, err.Error())
		}
		w.Items = items
	}
	for i, b := range parseButtons(in.Values["buttons"]) {
		w.Actions = append(w.Actions, Action{ID: "button", Label: b.label, Params: map[string]string{"n": strconv.Itoa(i)},
			Confirm: in.T("„%s“ ausführen?", b.label)})
	}
	if len(w.Stats) == 0 && len(w.Items) == 0 && len(w.Actions) == 0 {
		// Nothing configured yet: show what came back, to help writing the paths.
		w.Alerts = append(w.Alerts, in.T("Antwort: %s", preview(data)))
	}
	return w, nil
}

func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(l); l != "" && !strings.HasPrefix(l, "#") {
			out = append(out, l)
		}
	}
	return out
}

func preview(v any) string {
	b, _ := json.Marshal(v)
	s := string(b)
	if r := []rune(s); len(r) > 300 {
		s = string(r[:300]) + " …"
	}
	return s
}

func (c customAPI) items(in *Instance, data any, path string, max int) ([]Item, error) {
	v, ok := lookup(data, path)
	arr, isArr := v.([]any)
	if !ok || !isArr {
		// Objects keyed by id are lists too.
		if m, isMap := v.(map[string]any); ok && isMap {
			for k, el := range m {
				if obj, ok := el.(map[string]any); ok {
					if _, has := obj["key"]; !has {
						obj["key"] = k
					}
				}
				arr = append(arr, el)
			}
		} else {
			return nil, in.Errorf("„%s“ ist keine Liste", path)
		}
	}
	fields := map[string]string{}
	for _, l := range lines(in.Values["item"]) {
		if k, v, ok := strings.Cut(l, "="); ok {
			fields[strings.ToLower(strings.TrimSpace(k))] = v
		}
	}
	if fields["title"] == "" {
		fields["title"] = "name + title"
	}
	var out []Item
	for _, el := range arr {
		it := Item{}
		get := func(k string) (string, string) {
			if fields[k] == "" {
				return "", ""
			}
			v, tone, _ := evalExpr(in, el, fields[k])
			return v, tone
		}
		it.Title, _ = get("title")
		if it.Title == "" || it.Title == "–" {
			it.Title = preview(el)
		}
		it.Subtitle, _ = get("subtitle")
		it.Badge, _ = get("badge")
		if p := fields["progress"]; p != "" {
			if v, ok := lookup(el, strings.TrimSpace(strings.Split(p, "|")[0])); ok {
				if f, ok := toFloat(v); ok {
					if f <= 1 && strings.Contains(p, "anteil") {
						f *= 100
					}
					it.Progress = pct(f)
				}
			}
		}
		if u, _ := get("url"); strings.HasPrefix(u, "http") {
			it.URL = u
		} else if strings.HasPrefix(u, "/") {
			it.URL = in.Link(u)
		}
		if u, _ := get("image"); strings.HasPrefix(u, "http") {
			it.Image = u
		}
		for _, s := range []*string{&it.Subtitle, &it.Badge} {
			if *s == "–" {
				*s = ""
			}
		}
		out = append(out, it)
		if len(out) == max {
			break
		}
	}
	return out, nil
}

var toneRe = regexp.MustCompile(`^(ok|warn|bad)(>=|<=|!=|=|>|<)(.+)$`)

// evalExpr evaluates "path [+ path …] | format[:decimals] [unit] [ok=… warn>… bad<…]".
func evalExpr(in *Instance, data any, expr string) (string, string, error) {
	pathPart, fmtPart, _ := strings.Cut(expr, "|")
	var raws []any
	for _, p := range strings.Split(pathPart, "+") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if strings.HasPrefix(p, `"`) && strings.HasSuffix(p, `"`) && len(p) >= 2 { // literal text
			raws = append(raws, p[1:len(p)-1])
			continue
		}
		if v, ok := lookup(data, p); ok && v != nil {
			raws = append(raws, v)
		}
	}
	if len(raws) == 0 {
		return "–", "", errors.New("not found")
	}

	format, decimals, unit := "", -1, ""
	var tones [][]string
	for i, tok := range strings.Fields(fmtPart) {
		if m := toneRe.FindStringSubmatch(tok); m != nil {
			tones = append(tones, m[1:])
			continue
		}
		if i == 0 {
			f, d, hasD := strings.Cut(strings.ToLower(tok), ":")
			format = f
			if hasD {
				decimals, _ = strconv.Atoi(d)
			}
			continue
		}
		unit = strings.TrimSpace(unit + " " + tok)
	}

	tone := ""
	for _, t := range tones {
		if compare(raws[0], t[1], t[2]) {
			tone = t[0]
		}
	}
	var parts []string
	for _, v := range raws {
		parts = append(parts, formatValue(in, v, format, decimals))
	}
	out := strings.Join(parts, " · ")
	if unit != "" {
		out += " " + unit
	}
	return out, tone, nil
}

func formatValue(in *Instance, v any, format string, decimals int) string {
	num := func(f float64, d int) string {
		if d < 0 {
			if f == math.Trunc(f) && math.Abs(f) < 1e15 {
				return in.Num(int(f))
			}
			d = 1
		}
		s := strconv.FormatFloat(f, 'f', d, 64)
		if in.Lang != "en" {
			s = strings.Replace(s, ".", ",", 1)
		}
		return s
	}
	f, isNum := toFloat(v)
	switch format {
	case "zahl", "number":
		if isNum {
			return num(f, decimals)
		}
	case "bytes":
		if isNum {
			return in.Bytes(f)
		}
	case "prozent", "percent":
		if isNum {
			if decimals < 0 {
				decimals = 0
			}
			return num(f, decimals) + " %"
		}
	case "anteil", "ratio":
		if isNum {
			if decimals < 0 {
				decimals = 0
			}
			return num(f*100, decimals) + " %"
		}
	case "dauer", "duration":
		if isNum {
			return shortDuration(int(f))
		}
	case "datum", "date", "seit", "since":
		var t time.Time
		if isNum {
			if f > 1e12 { // milliseconds
				t = time.UnixMilli(int64(f))
			} else {
				t = time.Unix(int64(f), 0)
			}
		} else if s, ok := v.(string); ok {
			for _, layout := range []string{time.RFC3339Nano, "2006-01-02 15:04:05", "2006-01-02"} {
				if p, err := time.Parse(layout, s); err == nil {
					t = p
					break
				}
			}
		}
		if !t.IsZero() {
			if format == "seit" || format == "since" {
				return in.Since(t)
			}
			return in.Date(t.Format(time.RFC3339))
		}
	case "janein", "bool", "yesno":
		if b, ok := v.(bool); ok {
			return map[bool]string{true: in.T("Ja"), false: in.T("Nein")}[b]
		}
		if isNum {
			return map[bool]string{true: in.T("Ja"), false: in.T("Nein")}[f != 0]
		}
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return num(x, decimals)
	case bool:
		return map[bool]string{true: in.T("Ja"), false: in.T("Nein")}[x]
	case []any:
		var parts []string
		for _, el := range x {
			if s, ok := el.(string); ok {
				parts = append(parts, s)
			} else if f, ok := el.(float64); ok {
				parts = append(parts, num(f, decimals))
			}
		}
		if len(parts) == len(x) && len(parts) > 0 {
			return strings.Join(parts, ", ")
		}
		return in.Num(len(x))
	}
	return preview(v)
}

type customButton struct{ label, method, path, body string }

// parseButtons reads "Label = METHOD /path [json body]" lines.
func parseButtons(s string) []customButton {
	var out []customButton
	for _, l := range lines(s) {
		label, rest, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		f := strings.Fields(strings.TrimSpace(rest))
		if len(f) == 0 {
			continue
		}
		b := customButton{label: strings.TrimSpace(label), method: "POST", path: f[0]}
		if m := strings.ToUpper(f[0]); m == "GET" || m == "POST" || m == "PUT" || m == "PATCH" || m == "DELETE" {
			if len(f) < 2 {
				continue
			}
			b.method, b.path = m, f[1]
			b.body = strings.TrimSpace(strings.Join(f[2:], " "))
		}
		out = append(out, b)
	}
	return out
}

func (c customAPI) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	n, err := strconv.Atoi(p["n"])
	buttons := parseButtons(in.Values["buttons"])
	if action != "button" || err != nil || n < 0 || n >= len(buttons) {
		return in.Errorf("unbekannte Aktion %q", action)
	}
	b := buttons[n]
	_, err = c.request(ctx, in, b.method, b.path, b.body)
	return err
}

func (c customAPI) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	tpl := in.V("search")
	if tpl == "" || in.V("searchList") == "" {
		return nil, ErrNoSearch
	}
	target := strings.ReplaceAll(tpl, "{q}", url.QueryEscape(query))
	data, err := c.request(ctx, in, http.MethodGet, target, "")
	if err != nil {
		return nil, err
	}
	return c.items(in, data, in.V("searchList"), 8)
}
