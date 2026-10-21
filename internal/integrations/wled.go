package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// WLED: the JSON API of the controller (/json, /json/state), no login
// (github.com/wled/WLED, wled00/json.cpp). Presets come from /presets.json.

type wled struct{}

func init() { register(wled{}) }

func (wled) Def() Definition {
	return Definition{
		ID: "wled", Name: "WLED", Icon: "wled", Category: "Smart Home",
		Description: "An/Aus, Helligkeit, Effekt und Presets deiner LED-Streifen, Presets in der Suche",
		Setup: []string{
			"Als lokale Adresse die IP des WLED-Controllers eintragen, z. B. http://192.168.1.60. Eine Anmeldung braucht WLED nicht.",
		},
		HasWidget: true, HasSearch: true, Images: []string{"wled"}, Port: 80,
		Refresh: 10,
	}
}

type wledState struct {
	On  bool `json:"on"`
	Bri int  `json:"bri"`
	PS  int  `json:"ps"`
	Seg []struct {
		FX int  `json:"fx"`
		On bool `json:"on"`
	} `json:"seg"`
}

type wledAll struct {
	State wledState `json:"state"`
	Info  struct {
		Name string `json:"name"`
		Ver  string `json:"ver"`
		Leds struct {
			Count int `json:"count"`
			Pwr   int `json:"pwr"` // mA
		} `json:"leds"`
		Wifi struct {
			Signal int `json:"signal"`
		} `json:"wifi"`
	} `json:"info"`
	Effects []string `json:"effects"`
}

// presets returns preset ID → name; playlists and empty slots included.
func (wled) presets(ctx context.Context, in *Instance) map[int]string {
	var raw map[string]json.RawMessage
	if in.do(ctx, request{path: "/presets.json"}, &raw) != nil {
		return nil
	}
	out := map[int]string{}
	for k, v := range raw {
		id, err := strconv.Atoi(k)
		if err != nil || id < 1 || id > 250 {
			continue
		}
		var p struct {
			N string `json:"n"`
		}
		json.Unmarshal(v, &p)
		out[id] = firstNonEmpty(p.N, in.T("Preset %d", id))
	}
	return out
}

func (w wled) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var all wledAll
	if err := in.do(ctx, request{path: "/json"}, &all); err != nil {
		return nil, err
	}
	if all.Info.Ver == "" {
		return nil, in.Errorf("unter dieser Adresse läuft %s, nicht %s", in.T("ein anderer Dienst"), "WLED")
	}
	st := all.State
	state, tone := in.T("Aus"), ""
	if st.On {
		state, tone = in.T("An"), "ok"
	}
	wd := &Widget{Stats: []Stat{
		{Label: in.T("Status"), Value: state, Tone: tone},
		{Label: in.T("Helligkeit"), Value: fmt.Sprintf("%d %%", (st.Bri*100+127)/255)},
	}}
	if len(st.Seg) > 0 && st.Seg[0].FX >= 0 && st.Seg[0].FX < len(all.Effects) {
		wd.Stats = append(wd.Stats, Stat{Label: in.T("Effekt"), Value: all.Effects[st.Seg[0].FX]})
	}
	if all.Info.Leds.Pwr > 0 {
		wd.Stats = append(wd.Stats, Stat{Label: in.T("Strom"), Value: strings.Replace(fmt.Sprintf("%.1f A", float64(all.Info.Leds.Pwr)/1000), ".", map[bool]string{true: ".", false: ","}[in.Lang == "en"], 1)})
	}
	if all.Info.Leds.Count > 0 {
		wd.Stats = append(wd.Stats, Stat{Label: in.T("LEDs"), Value: in.Num(all.Info.Leds.Count)})
	}

	toggle := Action{ID: "toggle", Label: in.T("Einschalten"), Tone: "primary"}
	if st.On {
		toggle = Action{ID: "toggle", Label: in.T("Ausschalten")}
	}
	wd.Actions = []Action{toggle,
		{ID: "bri", Label: "−", Params: map[string]string{"step": "-"}},
		{ID: "bri", Label: "+", Params: map[string]string{"step": "+"}},
	}

	presets := w.presets(ctx, in)
	ids := make([]int, 0, len(presets))
	for id := range presets {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		if len(wd.Items) == 6 {
			break
		}
		it := Item{Title: presets[id]}
		if id == st.PS {
			it.Badge = in.T("Aktiv")
		} else {
			it.Actions = []Action{{ID: "preset", Label: in.T("Anwenden"), Params: map[string]string{"id": strconv.Itoa(id)}}}
		}
		wd.Items = append(wd.Items, it)
	}
	if len(wd.Items) > 0 {
		wd.ItemsTitle = in.T("Presets")
	}
	return wd, nil
}

// Search finds presets by name.
func (w wled) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	presets := w.presets(ctx, in)
	q := strings.ToLower(strings.TrimSpace(query))
	ids := []int{}
	for id, name := range presets {
		if strings.Contains(strings.ToLower(name), q) {
			ids = append(ids, id)
		}
	}
	sort.Ints(ids)
	out := []Item{}
	for _, id := range ids[:min(8, len(ids))] {
		out = append(out, Item{Title: presets[id], Subtitle: in.T("WLED-Preset"),
			Actions: []Action{{ID: "preset", Label: in.T("Anwenden"), Params: map[string]string{"id": strconv.Itoa(id)}}}})
	}
	return out, nil
}

func (w wled) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	var body map[string]any
	switch action {
	case "toggle":
		body = map[string]any{"on": "t"}
	case "preset":
		id, err := strconv.Atoi(p["id"])
		if err != nil || id < 1 || id > 250 {
			return in.Errorf("unbekannte Aktion %q", action)
		}
		body = map[string]any{"ps": id}
	case "bri":
		// WLED accepts "~25" / "~-25" for relative brightness steps.
		step := map[string]string{"+": "~26", "-": "~-26"}[p["step"]]
		if step == "" {
			return in.Errorf("unbekannte Aktion %q", action)
		}
		body = map[string]any{"bri": step}
		if p["step"] == "+" {
			body["on"] = true
		}
	default:
		return in.Errorf("unbekannte Aktion %q", action)
	}
	return in.do(ctx, request{method: "POST", path: "/json/state", body: body}, nil)
}
