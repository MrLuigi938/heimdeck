package integrations

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

// Home Assistant: REST API with a long-lived access token
// (developers.home-assistant.io/docs/api/rest).

type homeAssistant struct{}

func init() { register(homeAssistant{}) }

func (homeAssistant) Def() Definition {
	return Definition{
		ID: "home-assistant", Name: "Home Assistant", Icon: "home-assistant", Category: "Smart Home",
		Description: "Gewählte Sensoren und Schalter auf der Kachel, alle Geräte in der Suche schalten, verfügbare Updates",
		Setup: []string{
			"Als lokale Adresse die Adresse deiner Home-Assistant-Oberfläche eintragen, meist http://<IP>:8123 (oder http://homeassistant.local:8123).",
			"Token erstellen: in Home Assistant unten links auf dein Profil → Reiter „Sicherheit“ → ganz unten „Langlebige Zugriffstokens“ → „Token erstellen“. Den Token gleich kopieren, er wird nur einmal angezeigt.",
			"Mit „Auswählen“ unter den Entitäten Sensoren und Schalter für die Kachel suchen und anhaken – ohne Auswahl zeigt die Kachel eine Übersicht.",
		},
		Fields: []Field{
			{Key: "token", Label: "Zugriffstoken", Type: "password", Required: true, Secret: true,
				Help: "Profil (unten links) → Sicherheit → Langlebige Zugriffstokens → Token erstellen"},
			{Key: "entities", Label: "Entitäten auf der Kachel", Type: "text", Pick: true,
				Help: "Optional, durch Komma getrennt, z. B. sensor.wohnzimmer_temperatur, light.kueche. Eigener Name: sensor.wohnzimmer_temperatur = Wohnzimmer. Leer: Übersicht anzeigen."},
		},
		HasWidget: true, HasSearch: true, Images: []string{"home-assistant", "homeassistant", "haos"}, Port: 8123,
	}
}

type haState struct {
	EntityID   string `json:"entity_id"`
	State      string `json:"state"`
	Attributes struct {
		FriendlyName string `json:"friendly_name"`
		Unit         string `json:"unit_of_measurement"`
		Title        string `json:"title"`
		Installed    string `json:"installed_version"`
		Latest       string `json:"latest_version"`
	} `json:"attributes"`
}

func (s haState) domain() string { d, _, _ := strings.Cut(s.EntityID, "."); return d }

func (s haState) name() string {
	if s.Attributes.FriendlyName != "" {
		return s.Attributes.FriendlyName
	}
	return s.EntityID
}

func (homeAssistant) get(ctx context.Context, in *Instance, path string, out any) error {
	return in.do(ctx, request{path: path, headers: map[string]string{"Authorization": "Bearer " + in.V("token")}}, out)
}

func (h homeAssistant) Test(ctx context.Context, in *Instance) error {
	var cfg struct {
		Version string `json:"version"`
	}
	return h.get(ctx, in, "/api/config", &cfg)
}

func (h homeAssistant) states(ctx context.Context, in *Instance) ([]haState, error) {
	var states []haState
	err := h.get(ctx, in, "/api/states", &states)
	return states, err
}

// haServices lists the domains that can be switched from the dashboard and the
// service each button calls.
var haServices = map[string][]struct{ service, label string }{
	"light":         {{"toggle", "Umschalten"}},
	"switch":        {{"toggle", "Umschalten"}},
	"fan":           {{"toggle", "Umschalten"}},
	"input_boolean": {{"toggle", "Umschalten"}},
	"automation":    {{"toggle", "Umschalten"}},
	"cover":         {{"open_cover", "Öffnen"}, {"close_cover", "Schließen"}},
	"lock":          {{"unlock", "Aufschließen"}, {"lock", "Abschließen"}},
	"script":        {{"turn_on", "Ausführen"}},
	"scene":         {{"turn_on", "Aktivieren"}},
	"button":        {{"press", "Drücken"}},
	"input_button":  {{"press", "Drücken"}},
}

// item shows an entity; label is the user's own name for it, if any.
func (h homeAssistant) item(in *Instance, s haState, label string) Item {
	it := Item{Title: firstNonEmpty(label, s.name()), Subtitle: h.value(in, s), URL: in.Link("/history?entity_id=" + url.QueryEscape(s.EntityID))}
	for _, sv := range haServices[s.domain()] {
		// Locks have no toggle: only offer the opposite of the current state.
		if (sv.service == "lock" && s.State == "locked") || (sv.service == "unlock" && s.State == "unlocked") {
			continue
		}
		a := Action{ID: "service", Label: in.T(sv.label), Params: map[string]string{"entity": s.EntityID, "service": sv.service}}
		if s.domain() == "lock" && sv.service == "unlock" {
			a.Confirm = in.T("„%s“ aufschließen?", it.Title)
		}
		it.Actions = append(it.Actions, a)
	}
	return it
}

func (homeAssistant) value(in *Instance, s haState) string {
	v := s.State
	switch v {
	case "on":
		v = in.T("An")
	case "off":
		v = in.T("Aus")
	case "unavailable":
		v = in.T("Nicht verfügbar")
	case "open":
		v = in.T("Offen")
	case "closed":
		v = in.T("Geschlossen")
	case "locked":
		v = in.T("Abgeschlossen")
	case "unlocked":
		v = in.T("Aufgeschlossen")
	}
	if s.domain() == "scene" || s.domain() == "button" || s.domain() == "input_button" || s.domain() == "script" {
		return ""
	}
	if s.Attributes.Unit != "" {
		v += " " + s.Attributes.Unit
	}
	return v
}

func (h homeAssistant) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	states, err := h.states(ctx, in)
	if err != nil {
		return nil, err
	}
	byID := map[string]haState{}
	for _, s := range states {
		byID[s.EntityID] = s
	}
	w := &Widget{}

	if list := in.V("entities"); list != "" {
		// Values go into the stats row, things you can switch into the list.
		for _, e := range namedIDs(list) {
			s, ok := byID[strings.ToLower(e.ID)]
			if !ok {
				w.Alerts = append(w.Alerts, in.T("Entität %s nicht gefunden", e.ID))
				continue
			}
			if _, ok := haServices[s.domain()]; ok {
				w.Items = append(w.Items, h.item(in, s, e.Name))
				continue
			}
			w.Stats = append(w.Stats, Stat{Label: firstNonEmpty(e.Name, s.name()), Value: h.value(in, s), Tone: toneIf(s.State == "unavailable", "warn")})
		}
		return w, nil
	}

	lights, switches, unavailable := 0, 0, 0
	var updates []Item
	for _, s := range states {
		switch {
		case s.domain() == "light" && s.State == "on":
			lights++
		case s.domain() == "switch" && s.State == "on":
			switches++
		case s.domain() == "update" && s.State == "on":
			title := s.Attributes.Title
			if title == "" {
				title = s.name()
			}
			updates = append(updates, Item{Title: title, Subtitle: s.Attributes.Installed + " → " + s.Attributes.Latest, URL: in.Link("/config/updates")})
		}
		if s.State == "unavailable" {
			unavailable++
		}
	}
	w.Stats = []Stat{
		{Label: in.T("Lichter an"), Value: in.Num(lights)},
		{Label: in.T("Schalter an"), Value: in.Num(switches)},
		{Label: in.T("Updates"), Value: in.Num(len(updates)), Tone: toneIf(len(updates) > 0, "warn")},
		{Label: in.T("Nicht verfügbar"), Value: in.Num(unavailable), Tone: toneIf(unavailable > 0, "warn")},
	}
	if len(updates) > 0 {
		sort.Slice(updates, func(i, j int) bool { return updates[i].Title < updates[j].Title })
		w.ItemsTitle = in.T("Updates verfügbar")
		w.Items = updates
		if len(w.Items) > 5 {
			w.Items = w.Items[:5]
		}
	}
	return w, nil
}

// matching returns the entities whose name or ID contains every word of the query.
func (h homeAssistant) matching(ctx context.Context, in *Instance, query string) ([]haState, error) {
	states, err := h.states(ctx, in)
	if err != nil {
		return nil, err
	}
	words := strings.Fields(strings.ToLower(query))
	var hits []haState
	for _, s := range states {
		if s.domain() == "update" || s.domain() == "zone" {
			continue
		}
		hay := strings.ToLower(s.name() + " " + s.EntityID)
		ok := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				ok = false
				break
			}
		}
		if ok {
			hits = append(hits, s)
		}
	}
	return hits, nil
}

// Search finds entities by name or ID; every word has to match.
func (h homeAssistant) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	hits, err := h.matching(ctx, in, query)
	if err != nil {
		return nil, err
	}
	// Switchable entities first, they are what you usually look for.
	sort.SliceStable(hits, func(i, j int) bool {
		_, a := haServices[hits[i].domain()]
		_, b := haServices[hits[j].domain()]
		return a && !b
	})
	out := []Item{}
	names := map[string]string{} // own names from the tile also apply in the search
	for _, e := range namedIDs(in.V("entities")) {
		names[strings.ToLower(e.ID)] = e.Name
	}
	for _, s := range hits {
		out = append(out, h.item(in, s, names[s.EntityID]))
		if len(out) == 8 {
			break
		}
	}
	return out, nil
}

// Choices lists entities for the "entities" field, grouped by domain.
func (h homeAssistant) Choices(ctx context.Context, in *Instance, field, query string) ([]Choice, error) {
	hits, err := h.matching(ctx, in, query)
	if err != nil {
		return nil, err
	}
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].domain() != hits[j].domain() {
			return hits[i].domain() < hits[j].domain()
		}
		return strings.ToLower(hits[i].name()) < strings.ToLower(hits[j].name())
	})
	out := []Choice{}
	for _, s := range hits {
		out = append(out, Choice{ID: s.EntityID, Name: s.name(), Value: h.value(in, s), Group: s.domain()})
		if len(out) == 200 {
			break
		}
	}
	return out, nil
}

var entityRe = regexp.MustCompile(`^[a-z_]+\.[a-z0-9_]+$`)

func (h homeAssistant) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	if action != "service" || !entityRe.MatchString(p["entity"]) {
		return in.Errorf("unbekannte Aktion %q", action)
	}
	domain, _, _ := strings.Cut(p["entity"], ".")
	allowed := false
	for _, sv := range haServices[domain] {
		allowed = allowed || sv.service == p["service"]
	}
	if !allowed {
		return in.Errorf("unbekannte Aktion %q", p["service"])
	}
	return in.do(ctx, request{method: "POST", path: fmt.Sprintf("/api/services/%s/%s", domain, p["service"]),
		headers: map[string]string{"Authorization": "Bearer " + in.V("token")},
		body:    map[string]string{"entity_id": p["entity"]}}, nil)
}
