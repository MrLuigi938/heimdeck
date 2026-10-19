package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
)

// evcc: GET /api/state and the loadpoint routes of the evcc web server
// (github.com/evcc-io/evcc, server/http.go). Changing the charge mode needs no
// login; older versions wrap the state in {"result": …}.

type evcc struct{}

func init() { register(evcc{}) }

func (evcc) Def() Definition {
	return Definition{
		ID: "evcc", Name: "evcc", Icon: "evcc", Category: "Energie",
		Description: "PV-Leistung, Hausverbrauch, Netz und Batterie, Ladepunkte mit Lademodus zum Umschalten",
		HasWidget:   true, Images: []string{"evcc"}, Port: 7070,
	}
}

type evccLoadpoint struct {
	Title         string   `json:"title"`
	Mode          string   `json:"mode"`
	Charging      bool     `json:"charging"`
	Connected     bool     `json:"connected"`
	Enabled       bool     `json:"enabled"`
	ChargePower   float64  `json:"chargePower"`
	ChargedEnergy float64  `json:"chargedEnergy"`
	VehicleSoc    *float64 `json:"vehicleSoc"`
	VehicleTitle  string   `json:"vehicleTitle"`
	LimitSoc      float64  `json:"limitSoc"`
}

type evccState struct {
	SiteTitle string   `json:"siteTitle"`
	PVPower   *float64 `json:"pvPower"`
	HomePower *float64 `json:"homePower"`
	Grid      *struct {
		Power float64 `json:"power"`
	} `json:"grid"`
	GridPower *float64 `json:"gridPower"` // before 0.200
	Battery   *struct {
		Power float64  `json:"power"`
		Soc   *float64 `json:"soc"`
	} `json:"battery"`
	BatteryPower *float64        `json:"batteryPower"` // older versions
	BatterySoc   *float64        `json:"batterySoc"`
	GreenShare   *float64        `json:"greenShareHome"`
	Tariff       *float64        `json:"tariffGrid"`
	Currency     string          `json:"currency"`
	Loadpoints   []evccLoadpoint `json:"loadpoints"`
}

func (evcc) state(ctx context.Context, in *Instance) (*evccState, error) {
	var raw map[string]json.RawMessage
	if err := in.do(ctx, request{path: "/api/state"}, &raw); err != nil {
		return nil, err
	}
	b, _ := json.Marshal(raw)
	if r, ok := raw["result"]; ok && len(raw) == 1 {
		b = r
	}
	var st evccState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, in.Errorf("unerwartete Antwort (kein gültiges JSON) – stimmt der Dienst-Typ? (%v)", err)
	}
	if st.Loadpoints == nil && st.PVPower == nil && st.HomePower == nil {
		return nil, in.Errorf("unter dieser Adresse läuft %s, nicht %s", in.T("ein anderer Dienst"), "evcc")
	}
	return &st, nil
}

// kw formats watts as kW with one decimal (or W below 1 kW).
func kw(in *Instance, w float64) string {
	if math.Abs(w) < 1000 {
		return fmt.Sprintf("%.0f W", w)
	}
	s := fmt.Sprintf("%.1f kW", w/1000)
	if in.Lang != "en" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

var evccModes = []struct{ id, label string }{{"off", "Aus"}, {"pv", "PV"}, {"minpv", "Min+PV"}, {"now", "Schnell"}}

func evccMode(in *Instance, mode string) string {
	for _, m := range evccModes {
		if m.id == mode {
			return in.T(m.label)
		}
	}
	return mode
}

func (e evcc) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	st, err := e.state(ctx, in)
	if err != nil {
		return nil, err
	}
	w := &Widget{}
	if st.PVPower != nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("PV"), Value: kw(in, *st.PVPower), Tone: toneIf(*st.PVPower > 50, "ok")})
	}
	if st.HomePower != nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("Haus"), Value: kw(in, *st.HomePower)})
	}
	grid := st.GridPower
	if st.Grid != nil {
		grid = &st.Grid.Power
	}
	if grid != nil {
		if *grid < 0 {
			w.Stats = append(w.Stats, Stat{Label: in.T("Einspeisung"), Value: kw(in, -*grid), Tone: "ok"})
		} else {
			w.Stats = append(w.Stats, Stat{Label: in.T("Netzbezug"), Value: kw(in, *grid), Tone: toneIf(*grid > 1000, "warn")})
		}
	}
	soc, bat := st.BatterySoc, st.BatteryPower
	if st.Battery != nil {
		soc, bat = st.Battery.Soc, &st.Battery.Power
	}
	if soc != nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("Batterie"), Value: fmt.Sprintf("%.0f %%", *soc), Tone: toneIf(*soc < 15, "warn")})
	}
	if bat != nil && math.Abs(*bat) >= 50 {
		// Positive means the battery is discharging.
		label := in.T("Batterie lädt")
		if *bat > 0 {
			label = in.T("Batterie entlädt")
		}
		w.Stats = append(w.Stats, Stat{Label: label, Value: kw(in, math.Abs(*bat))})
	}
	if st.GreenShare != nil && st.PVPower != nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("Sonnenanteil"), Value: fmt.Sprintf("%.0f %%", *st.GreenShare*100)})
	}

	w.ItemsTitle = in.T("Ladepunkte")
	for i, lp := range st.Loadpoints {
		var sub []string
		if lp.VehicleTitle != "" && lp.Connected {
			sub = append(sub, lp.VehicleTitle)
		}
		switch {
		case lp.Charging:
			sub = append(sub, in.T("Lädt")+" "+kw(in, lp.ChargePower))
		case lp.Connected:
			sub = append(sub, in.T("Verbunden"))
		default:
			sub = append(sub, in.T("Nicht verbunden"))
		}
		if lp.ChargedEnergy > 0 {
			sub = append(sub, fmt.Sprintf("%.1f kWh", lp.ChargedEnergy/1000))
		}
		it := Item{Title: lp.Title, Subtitle: strings.Join(sub, " · "), Badge: evccMode(in, lp.Mode), URL: in.Link("/")}
		if it.Title == "" {
			it.Title = in.T("Ladepunkt %d", i+1)
		}
		if lp.VehicleSoc != nil && lp.Connected {
			it.Progress = pct(*lp.VehicleSoc)
		}
		for _, m := range evccModes {
			if m.id != lp.Mode {
				it.Actions = append(it.Actions, Action{ID: "mode", Label: in.T(m.label),
					Params: map[string]string{"loadpoint": strconv.Itoa(i + 1), "mode": m.id}})
			}
		}
		w.Items = append(w.Items, it)
	}
	return w, nil
}

func (e evcc) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	lp, err := strconv.Atoi(p["loadpoint"])
	if action != "mode" || err != nil || lp < 1 || lp > 100 {
		return in.Errorf("unbekannte Aktion %q", action)
	}
	valid := false
	for _, m := range evccModes {
		valid = valid || m.id == p["mode"]
	}
	if !valid {
		return in.Errorf("unbekannte Aktion %q", p["mode"])
	}
	return in.do(ctx, request{method: "POST", path: fmt.Sprintf("/api/loadpoints/%d/mode/%s", lp, p["mode"])}, nil)
}
