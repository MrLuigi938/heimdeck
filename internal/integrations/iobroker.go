package integrations

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
)

// ioBroker has no built-in HTTP API; it comes from an adapter. The rest-api
// adapter (port 8093, /v1/…) is preferred, the older simple-api adapter
// (port 8087) is used when /v1 does not exist
// (github.com/ioBroker/ioBroker.rest-api, ioBroker.simple-api).

type iobroker struct{}

func init() { register(iobroker{}) }

func (iobroker) Def() Definition {
	return Definition{
		ID: "iobroker", Name: "ioBroker", Icon: "iobroker", Category: "Smart Home",
		Description: "Gewählte Datenpunkte auf der Kachel, Schalter umschalten, Adapter-Status, Suche nach Datenpunkten (über den REST-API- oder Simple-API-Adapter)",
		Setup: []string{
			"ioBroker hat keine eigene Daten-Schnittstelle – sie kommt von einem Adapter. Öffne ioBroker Admin → Adapter, suche „REST API“ und installiere ihn (eine Instanz reicht, Standard-Einstellungen passen).",
			"Als lokale Adresse http://<ioBroker-IP>:8093 eintragen. Die Admin-Oberfläche (Port 8081) liefert keine Daten. Läuft der Adapter im Web-Adapter, lautet die Adresse http://<ioBroker-IP>:8082/rest-api. Der ältere Simple-API-Adapter (Port 8087) geht auch.",
			"Benutzer und Passwort nur angeben, wenn im Adapter „Authentifizierung“ aktiv ist.",
			"Mit „Auswählen“ unter den Datenpunkten Werte für die Kachel suchen und anhaken – ohne Auswahl zeigt die Kachel den Status deiner Adapter.",
		},
		Fields: []Field{
			{Key: "states", Label: "Datenpunkte auf der Kachel", Type: "text", Pick: true,
				Help: "Optional, durch Komma getrennt, z. B. hm-rpc.0.ABC123.1.STATE. Eigener Name: hm-rpc.0.ABC123.1.STATE = Flurlicht. Leer: Adapter-Status anzeigen."},
			{Key: "username", Label: "Benutzername", Type: "text", Help: "Nur nötig, wenn im Adapter die Authentifizierung aktiv ist"},
			{Key: "password", Label: "Passwort", Type: "password", Secret: true},
		},
		HasWidget: true, HasSearch: true, Images: []string{"iobroker"}, Port: 8093,
	}
}

var iobrokerSimple sync.Map // BaseURL -> true for simple-api, false for rest-api

func (iobroker) get(ctx context.Context, in *Instance, path string, q url.Values, out any) error {
	h := map[string]string{}
	if in.V("username") != "" {
		h["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(in.V("username")+":"+in.Values["password"]))
	}
	return in.do(ctx, request{path: path, query: q, headers: h}, out)
}

// wrongAddress reports answers that show there is no such adapter at the
// address (a 404 or a web page instead of JSON).
func wrongAddress(err error) bool {
	var he *HTTPError
	return errors.As(err, &he) && (he.Status == 404 || he.Status == 405) || errors.Is(err, ErrNotJSON)
}

// simple finds out which adapter answers: rest-api or the older simple-api.
func (b iobroker) simple(ctx context.Context, in *Instance) (bool, error) {
	if v, ok := iobrokerSimple.Load(in.BaseURL); ok {
		return v.(bool), nil
	}
	var objs map[string]json.RawMessage
	err := b.get(ctx, in, "/v1/objects", url.Values{"filter": {"system.config"}}, &objs)
	if err == nil {
		iobrokerSimple.Store(in.BaseURL, false)
		return false, nil
	}
	if !wrongAddress(err) {
		return false, err
	}
	err = b.get(ctx, in, "/objects", url.Values{"pattern": {"system.config"}}, &objs)
	if err == nil {
		iobrokerSimple.Store(in.BaseURL, true)
		return true, nil
	}
	if !wrongAddress(err) {
		return false, err
	}
	return false, in.Errorf("Unter dieser Adresse antwortet keine ioBroker-Schnittstelle. Installiere in ioBroker unter „Adapter“ den Adapter „REST API“ und trage http://<ioBroker-IP>:8093 ein – die Admin-Oberfläche (Port 8081) liefert keine Daten.")
}

// call uses the endpoint of the adapter in use: rest is the rest-api path,
// simple the simple-api one.
func (b iobroker) call(ctx context.Context, in *Instance, rest, simple string, q, qs url.Values, out any) error {
	isSimple, err := b.simple(ctx, in)
	if err != nil {
		return err
	}
	if isSimple {
		return b.get(ctx, in, simple, qs, out)
	}
	return b.get(ctx, in, rest, q, out)
}

type ioCommon struct {
	Name  json.RawMessage `json:"name"`
	Unit  string          `json:"unit"`
	Role  string          `json:"role"`
	Type  string          `json:"type"`
	Write *bool           `json:"write"`
}

// name handles both plain names and translated {"de": …, "en": …} objects.
func (c ioCommon) name(lang string) string {
	var s string
	if json.Unmarshal(c.Name, &s) == nil {
		return s
	}
	var m map[string]string
	if json.Unmarshal(c.Name, &m) == nil {
		if m[lang] != "" {
			return m[lang]
		}
		if m["en"] != "" {
			return m["en"]
		}
		for _, v := range m {
			return v
		}
	}
	return ""
}

func (c ioCommon) switchable() bool { return c.Type == "boolean" && (c.Write == nil || *c.Write) }

type ioState struct {
	Error  string    `json:"error"` // simple-api: unknown id in a list
	ID     string    `json:"id"`
	ID2    string    `json:"_id"`
	Val    any       `json:"val"`
	Common *ioCommon `json:"common"`
	ioCommon
}

func (s ioState) common() ioCommon {
	if s.Common != nil {
		return *s.Common
	}
	return s.ioCommon
}

var ioIDRe = regexp.MustCompile(`^[^\s/?#*,"'\\]+$`)

func validIOID(id string) bool { return ioIDRe.MatchString(id) && !strings.Contains(id, "..") }

func splitIDs(list string) []string {
	var ids []string
	for _, id := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' || r == ';' || r == '\n' }) {
		if validIOID(id) {
			ids = append(ids, id)
		}
	}
	return ids
}

// tileStates reads the "states" field: valid IDs with the user's own names.
func tileStates(list string) (ids []string, names map[string]string) {
	names = map[string]string{}
	for _, e := range namedIDs(list) {
		if validIOID(e.ID) {
			ids = append(ids, e.ID)
			names[e.ID] = e.Name
		}
	}
	return ids, names
}

// states loads values and object info for the given ids. Unknown ids are
// missing from the result.
func (b iobroker) states(ctx context.Context, in *Instance, ids []string) (map[string]ioState, error) {
	escaped := make([]string, len(ids))
	for i, id := range ids {
		escaped[i] = url.PathEscape(id)
	}
	joined := strings.Join(escaped, ",")
	var raw json.RawMessage
	err := b.call(ctx, in, "/v1/state/"+joined, "/get/"+joined, url.Values{"withInfo": {"true"}}, nil, &raw)
	var he *HTTPError
	if errors.As(err, &he) && he.Status == 404 && len(ids) > 1 {
		// rest-api answers 404 for the whole list when one id is unknown.
		out := map[string]ioState{}
		for _, id := range ids {
			one, err := b.states(ctx, in, []string{id})
			if errors.As(err, &he) && he.Status == 404 {
				continue
			} else if err != nil {
				return nil, err
			}
			maps.Copy(out, one)
		}
		return out, nil
	}
	if errors.As(err, &he) && he.Status == 404 {
		return map[string]ioState{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []ioState
	if json.Unmarshal(raw, &list) != nil {
		var one ioState
		if err := json.Unmarshal(raw, &one); err != nil {
			return nil, in.Errorf("unerwartete Antwort (kein gültiges JSON) – stimmt der Dienst-Typ? (%v)", err)
		}
		list = []ioState{one}
	}
	out := map[string]ioState{}
	for i, s := range list {
		if s.Error != "" {
			continue
		}
		id := firstNonEmpty(s.ID, s.ID2)
		if id == "" && i < len(ids) {
			id = ids[i]
		}
		out[id] = s
	}
	return out, nil
}

func ioValue(in *Instance, s ioState) string {
	c := s.common()
	switch v := s.Val.(type) {
	case nil:
		return "–"
	case bool:
		if v {
			return in.T("An")
		}
		return in.T("Aus")
	case float64:
		str := fmt.Sprintf("%.2f", v)
		str = strings.TrimRight(strings.TrimRight(str, "0"), ".")
		if in.Lang != "en" {
			str = strings.Replace(str, ".", ",", 1)
		}
		if c.Unit != "" {
			str += " " + c.Unit
		}
		return str
	}
	return strings.TrimSpace(fmt.Sprint(s.Val) + " " + c.Unit)
}

// item shows a data point; label is the user's own name for it, if any.
func (b iobroker) item(in *Instance, id string, s ioState, label string) Item {
	c := s.common()
	it := Item{Title: firstNonEmpty(label, c.name(in.Lang), id), Subtitle: id, Badge: ioValue(in, s)}
	if c.switchable() {
		it.Actions = []Action{{ID: "toggle", Label: in.T("Umschalten"), Params: map[string]string{"id": id}}}
	}
	return it
}

func (b iobroker) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	w := &Widget{}
	if ids, names := tileStates(in.V("states")); len(ids) > 0 {
		states, err := b.states(ctx, in, ids)
		if err != nil {
			return nil, err
		}
		for _, id := range ids {
			s, ok := states[id]
			if !ok {
				w.Alerts = append(w.Alerts, in.T("Datenpunkt %s nicht gefunden", id))
				continue
			}
			if s.common().switchable() {
				w.Items = append(w.Items, b.item(in, id, s, names[id]))
				continue
			}
			w.Stats = append(w.Stats, Stat{Label: firstNonEmpty(names[id], s.common().name(in.Lang), id), Value: ioValue(in, s)})
		}
		return w, nil
	}

	// Overview: which enabled adapter instances are running.
	var objs map[string]struct {
		Common struct {
			Enabled bool `json:"enabled"`
		} `json:"common"`
	}
	if err := b.call(ctx, in, "/v1/objects", "/objects", url.Values{"filter": {"system.adapter.*"}, "type": {"instance"}},
		url.Values{"pattern": {"system.adapter.*"}, "type": {"instance"}}, &objs); err != nil {
		return nil, err
	}
	var alive map[string]struct {
		Val any `json:"val"`
	}
	b.call(ctx, in, "/v1/states", "/states", url.Values{"filter": {"system.adapter.*.alive"}},
		url.Values{"pattern": {"system.adapter.*.alive"}}, &alive)
	enabled, running := 0, 0
	var down []string
	for id, o := range objs {
		if !o.Common.Enabled {
			continue
		}
		enabled++
		if v, _ := alive[id+".alive"].Val.(bool); v {
			running++
		} else {
			down = append(down, strings.TrimPrefix(id, "system.adapter."))
		}
	}
	sort.Strings(down)
	w.Stats = []Stat{
		{Label: in.T("Adapter aktiv"), Value: fmt.Sprintf("%d/%d", running, enabled), Tone: toneIf(running < enabled, "warn")},
	}
	if len(down) > 0 {
		w.ItemsTitle = in.T("Gestoppte Adapter")
		for _, d := range down {
			w.Items = append(w.Items, Item{Title: d, Badge: in.T("Gestoppt")})
		}
	}
	return w, nil
}

// find returns data points whose ID contains the query (in the spelling
// given, lower case or capitalised, as ioBroker's filter is case-sensitive).
// Without a query it lists the user's own data points and aliases.
func (b iobroker) find(ctx context.Context, in *Instance, query string, limit int) (map[string]ioCommon, []string, error) {
	query = strings.TrimSpace(query)
	patterns := []string{"0_userdata.0.*", "alias.0.*"}
	if query != "" {
		q := strings.ReplaceAll(query, " ", "*")
		if !ioIDRe.MatchString(q) {
			return nil, nil, nil
		}
		patterns = []string{"*" + q + "*"}
		for _, v := range []string{strings.ToLower(q), strings.ToUpper(q[:1]) + strings.ToLower(q[1:])} {
			if !slices.Contains(patterns, "*"+v+"*") {
				patterns = append(patterns, "*"+v+"*")
			}
		}
	}
	found := map[string]ioCommon{}
	for _, p := range patterns {
		var objs map[string]struct {
			Common ioCommon `json:"common"`
		}
		if err := b.call(ctx, in, "/v1/objects", "/objects", url.Values{"filter": {p}, "type": {"state"}},
			url.Values{"pattern": {p}, "type": {"state"}}, &objs); err != nil {
			return nil, nil, err
		}
		for id, o := range objs {
			found[id] = o.Common
		}
	}
	ids := slices.Sorted(maps.Keys(found))
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return found, ids, nil
}

// Search finds data points whose ID contains the search term.
func (b iobroker) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	if strings.TrimSpace(query) == "" {
		return []Item{}, nil
	}
	found, ids, err := b.find(ctx, in, query, 8)
	if err != nil || len(ids) == 0 {
		return []Item{}, err
	}
	states, _ := b.states(ctx, in, ids)
	_, names := tileStates(in.V("states")) // own names from the tile also apply in the search
	out := []Item{}
	for _, id := range ids {
		s := states[id]
		c := found[id]
		s.Common = &c
		out = append(out, b.item(in, id, s, names[id]))
	}
	return out, nil
}

// Choices lists data points for the "states" field, grouped by adapter instance.
func (b iobroker) Choices(ctx context.Context, in *Instance, field, query string) ([]Choice, error) {
	found, ids, err := b.find(ctx, in, query, 60)
	if err != nil {
		return nil, err
	}
	out := []Choice{}
	if len(ids) == 0 {
		return out, nil
	}
	states, _ := b.states(ctx, in, ids)
	for _, id := range ids {
		s := states[id]
		c := found[id]
		s.Common = &c
		parts := strings.SplitN(id, ".", 3)
		out = append(out, Choice{ID: id, Name: c.name(in.Lang), Value: ioValue(in, s), Group: strings.Join(parts[:min(2, len(parts))], ".")})
	}
	return out, nil
}

func (b iobroker) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	id := p["id"]
	if action != "toggle" || len(splitIDs(id)) != 1 || splitIDs(id)[0] != id {
		return in.Errorf("unbekannte Aktion %q", action)
	}
	return b.call(ctx, in, "/v1/state/"+url.PathEscape(id)+"/toggle", "/toggle/"+url.PathEscape(id), nil, nil, nil)
}
