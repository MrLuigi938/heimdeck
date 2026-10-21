package integrations

import (
	"context"
	"encoding/json"
	"errors"
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
		HasWidget:   true, Images: []string{"evcc"}, Port: 7070, Refresh: 5,
	}
}

type evccLoadpoint struct {
	Title         string   `json:"title"`
	Mode          string   `json:"mode"`
	Charging      bool     `json:"charging"`
	Connected     bool     `json:"connected"`
	ChargePower   float64  `json:"chargePower"`
	ChargedEnergy float64  `json:"chargedEnergy"` // Wh
	VehicleSoc    *float64 `json:"vehicleSoc"`
	VehicleTitle  string   `json:"vehicleTitle"`
}

// evccState holds the values the tile shows. The layout of /api/state changed
// over the years (gridPower → grid.power, batteryPower/batterySoc → battery
// object, battery as a list of devices in older versions, a "result" wrapper),
// so the values are picked from whatever is there.
type evccState struct {
	PV, Home, Grid, BatPower, BatSoc, Green *float64
	Loadpoints                              []evccLoadpoint
	Smart                                   bool // the version knows the "smart" charge mode
}

func evccNum(raw json.RawMessage) *float64 {
	var f *float64
	if json.Unmarshal(raw, &f) != nil {
		return nil
	}
	return f
}

func parseEvccState(b []byte) (*evccState, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	if r, ok := raw["result"]; ok && len(raw) == 1 {
		raw = nil
		if err := json.Unmarshal(r, &raw); err != nil {
			return nil, err
		}
	}
	st := &evccState{PV: evccNum(raw["pvPower"]), Home: evccNum(raw["homePower"]), Green: evccNum(raw["greenShareHome"])}
	type powered struct {
		Power    float64  `json:"power"`
		Soc      *float64 `json:"soc"`
		Capacity float64  `json:"capacity"`
	}
	if st.PV == nil {
		var pv []powered
		if json.Unmarshal(raw["pv"], &pv) == nil && len(pv) > 0 {
			sum := 0.0
			for _, p := range pv {
				sum += p.Power
			}
			st.PV = &sum
		}
	}
	var grid powered
	if json.Unmarshal(raw["grid"], &grid) == nil && raw["grid"] != nil && string(raw["grid"]) != "null" {
		st.Grid = &grid.Power
	} else {
		st.Grid = evccNum(raw["gridPower"])
	}
	// battery: an object (current), a list of devices (older) or top-level values
	var bat powered
	var bats []powered
	switch {
	case json.Unmarshal(raw["battery"], &bat) == nil && raw["battery"] != nil && string(raw["battery"]) != "null":
		st.BatPower, st.BatSoc = &bat.Power, bat.Soc
	case json.Unmarshal(raw["battery"], &bats) == nil && len(bats) > 0:
		power, socSum, capSum := 0.0, 0.0, 0.0
		for _, b := range bats {
			power += b.Power
			if b.Soc != nil {
				c := b.Capacity
				if c <= 0 {
					c = 1
				}
				socSum += *b.Soc * c
				capSum += c
			}
		}
		st.BatPower = &power
		if capSum > 0 {
			soc := socSum / capSum
			st.BatSoc = &soc
		}
	}
	if v := evccNum(raw["batteryPower"]); v != nil {
		st.BatPower = v
	}
	if v := evccNum(raw["batterySoc"]); v != nil && st.BatSoc == nil {
		st.BatSoc = v
	}
	json.Unmarshal(raw["loadpoints"], &st.Loadpoints)
	_, st.Smart = raw["optimizer"]
	for _, lp := range st.Loadpoints {
		st.Smart = st.Smart || lp.Mode == "smart"
	}
	if st.Loadpoints == nil && st.PV == nil && st.Home == nil {
		return nil, errors.New("no evcc state")
	}
	return st, nil
}

func (evcc) state(ctx context.Context, in *Instance) (*evccState, error) {
	var raw json.RawMessage
	if err := in.do(ctx, request{path: "/api/state"}, &raw); err != nil {
		return nil, err
	}
	st, err := parseEvccState(raw)
	if err != nil {
		return nil, in.Errorf("unter dieser Adresse läuft %s, nicht %s", in.T("ein anderer Dienst"), "evcc")
	}
	return st, nil
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

var evccModes = []struct{ id, label string }{{"off", "Aus"}, {"pv", "PV"}, {"minpv", "Min+PV"}, {"now", "Schnell"}, {"smart", "Smart"}}

// energy formats Wh as "850 Wh" or "8,7 kWh".
func energy(in *Instance, wh float64) string {
	if wh < 1000 {
		return fmt.Sprintf("%.0f Wh", wh)
	}
	s := fmt.Sprintf("%.1f kWh", wh/1000)
	if in.Lang != "en" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

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
	if st.PV != nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("PV"), Value: kw(in, *st.PV), Tone: toneIf(*st.PV > 50, "ok")})
	}
	if st.Home != nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("Haus"), Value: kw(in, *st.Home)})
	}
	if g := st.Grid; g != nil {
		if *g < 0 {
			w.Stats = append(w.Stats, Stat{Label: in.T("Einspeisung"), Value: kw(in, -*g), Tone: "ok"})
		} else {
			w.Stats = append(w.Stats, Stat{Label: in.T("Netzbezug"), Value: kw(in, *g), Tone: toneIf(*g > 1000, "warn")})
		}
	}
	if soc := st.BatSoc; soc != nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("Batterie"), Value: fmt.Sprintf("%.0f %%", *soc), Tone: toneIf(*soc < 15, "warn")})
	}
	if bat := st.BatPower; bat != nil && math.Abs(*bat) >= 50 {
		// Positive means the battery is discharging.
		label := in.T("Batterie lädt")
		if *bat > 0 {
			label = in.T("Batterie entlädt")
		}
		w.Stats = append(w.Stats, Stat{Label: label, Value: kw(in, math.Abs(*bat))})
	}
	if st.Green != nil && st.PV != nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("Sonnenanteil"), Value: fmt.Sprintf("%.0f %%", *st.Green*100)})
	}

	w.ItemsTitle = in.T("Ladepunkte")
	for i, lp := range st.Loadpoints {
		var sub []string
		if lp.VehicleTitle != "" && lp.Connected {
			sub = append(sub, lp.VehicleTitle)
		}
		switch {
		case lp.Charging && lp.VehicleTitle != "":
			sub = append(sub, in.T("Lädt")+" "+kw(in, lp.ChargePower))
		case lp.ChargePower >= 50: // e.g. a heat pump without a vehicle
			sub = append(sub, in.T("Leistung")+" "+kw(in, lp.ChargePower))
		case lp.Connected:
			sub = append(sub, in.T("Verbunden"))
		case lp.VehicleTitle != "":
			sub = append(sub, in.T("Nicht verbunden"))
		default:
			sub = append(sub, in.T("Bereit"))
		}
		if lp.ChargedEnergy >= 1 {
			sub = append(sub, energy(in, lp.ChargedEnergy))
		}
		it := Item{Title: lp.Title, Subtitle: strings.Join(sub, " · "), Badge: evccMode(in, lp.Mode), URL: in.Link("/")}
		if it.Title == "" {
			it.Title = in.T("Ladepunkt %d", i+1)
		}
		if lp.VehicleSoc != nil && lp.Connected {
			it.Progress = pct(*lp.VehicleSoc)
		}
		for _, m := range evccModes {
			if m.id != lp.Mode && (m.id != "smart" || st.Smart) {
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
