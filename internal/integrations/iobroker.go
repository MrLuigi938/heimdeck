package integrations

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
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
		Fields: []Field{
			{Key: "states", Label: "Datenpunkte auf der Kachel", Type: "text",
				Help: "Optional, durch Komma getrennt, z. B. hm-rpc.0.ABC123.1.STATE. Leer: Adapter-Status anzeigen. Als Adresse den REST-API-Adapter (Port 8093) oder Simple-API-Adapter (Port 8087) eintragen."},
			{Key: "username", Label: "Benutzername", Type: "text", Help: "Nur nötig, wenn im Adapter die Authentifizierung aktiv ist"},
			{Key: "password", Label: "Passwort", Type: "password", Secret: true},
		},
		HasWidget: true, HasSearch: true, Images: []string{"iobroker"}, Port: 8093,
	}
}

var iobrokerSimple sync.Map // BaseURL -> true when only simple-api answers

func (iobroker) get(ctx context.Context, in *Instance, path string, q url.Values, out any) error {
	h := map[string]string{}
	if in.V("username") != "" {
		h["Authorization"] = "Basic " + base64.StdEncoding.EncodeToString([]byte(in.V("username")+":"+in.Values["password"]))
	}
	return in.do(ctx, request{path: path, query: q, headers: h}, out)
}

// call picks the endpoint of the adapter in use: rest is the rest-api path,
// simple the simple-api one.
func (b iobroker) call(ctx context.Context, in *Instance, rest, simple string, q, qs url.Values, out any) error {
	if _, ok := iobrokerSimple.Load(in.BaseURL); ok {
		return b.get(ctx, in, simple, qs, out)
	}
	err := b.get(ctx, in, rest, q, out)
	var he *HTTPError
	if errors.As(err, &he) && he.Status == 404 && strings.HasPrefix(rest, "/v1/") {
		if err2 := b.get(ctx, in, simple, qs, out); err2 == nil {
			iobrokerSimple.Store(in.BaseURL, true)
			return nil
		}
	}
	return err
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

func splitIDs(list string) []string {
	var ids []string
	for _, id := range strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' || r == ';' || r == '\n' }) {
		if ioIDRe.MatchString(id) && !strings.Contains(id, "..") {
			ids = append(ids, id)
		}
	}
	return ids
}

// states loads values and object info for the given ids.
func (b iobroker) states(ctx context.Context, in *Instance, ids []string) (map[string]ioState, error) {
	escaped := make([]string, len(ids))
	for i, id := range ids {
		escaped[i] = url.PathEscape(id)
	}
	joined := strings.Join(escaped, ",")
	var raw json.RawMessage
	if err := b.call(ctx, in, "/v1/state/"+joined, "/get/"+joined, url.Values{"withInfo": {"true"}}, nil, &raw); err != nil {
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

func (b iobroker) item(in *Instance, id string, s ioState) Item {
	c := s.common()
	it := Item{Title: firstNonEmpty(c.name(in.Lang), id), Subtitle: id, Badge: ioValue(in, s)}
	if c.switchable() {
		it.Actions = []Action{{ID: "toggle", Label: in.T("Umschalten"), Params: map[string]string{"id": id}}}
	}
	return it
}

func (b iobroker) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	w := &Widget{}
	if ids := splitIDs(in.V("states")); len(ids) > 0 {
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
				w.Items = append(w.Items, b.item(in, id, s))
				continue
			}
			w.Stats = append(w.Stats, Stat{Label: firstNonEmpty(s.common().name(in.Lang), id), Value: ioValue(in, s)})
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

// Search finds data points whose ID contains the search term.
func (b iobroker) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	query = strings.TrimSpace(query)
	if !ioIDRe.MatchString(strings.ReplaceAll(query, " ", "_")) {
		return []Item{}, nil
	}
	pattern := "*" + strings.ReplaceAll(query, " ", "*") + "*"
	var objs map[string]struct {
		Common ioCommon `json:"common"`
	}
	if err := b.call(ctx, in, "/v1/objects", "/objects", url.Values{"filter": {pattern}, "type": {"state"}},
		url.Values{"pattern": {pattern}, "type": {"state"}}, &objs); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(objs))
	for id := range objs {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	if len(ids) > 8 {
		ids = ids[:8]
	}
	out := []Item{}
	if len(ids) == 0 {
		return out, nil
	}
	states, _ := b.states(ctx, in, ids)
	for _, id := range ids {
		s := states[id]
		s.Common = &ioCommon{}
		*s.Common = objs[id].Common
		out = append(out, b.item(in, id, s))
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
