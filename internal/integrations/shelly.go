package integrations

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Shelly: the local HTTP interface of the device, no cloud needed
// (shelly-api-docs.shelly.cloud). Gen1 devices answer on /status and are
// switched with /relay/N?turn=…, Gen2 and newer (Plus, Pro, Gen3, Gen4) use
// JSON-RPC below /rpc. Protected Gen1 devices use Basic auth, newer ones
// Digest auth with SHA-256 and the fixed user "admin".

type shelly struct{}

func init() { register(shelly{}) }

func (shelly) Def() Definition {
	return Definition{
		ID: "shelly", Name: "Shelly", Icon: "shelly", Category: "Smart Home",
		Description: "Schalter, Lichter und Rollläden eines Shelly-Geräts direkt bedienen, Leistung, Energie, Temperatur und Updates (lokal, ohne Cloud)",
		Setup: []string{
			"Als lokale Adresse die IP des Shelly-Geräts eintragen, z. B. http://192.168.1.50. Für jedes Gerät eine eigene Kachel anlegen.",
			"Ist in der Shelly-Oberfläche ein Passwort gesetzt (Einstellungen → Authentifizierung), es hier eintragen. Bei Gen1-Geräten auch den Benutzernamen; neuere Geräte verwenden immer „admin“.",
		},
		Fields: []Field{
			{Key: "username", Label: "Benutzername", Type: "text", Help: "Nur Gen1-Geräte mit Passwortschutz; neuere Geräte: leer lassen"},
			{Key: "password", Label: "Passwort", Type: "password", Secret: true, Help: "Nur nötig, wenn im Gerät ein Passwort gesetzt ist"},
		},
		HasWidget: true, Images: []string{"shelly"}, Port: 80,
		Refresh: 10,
	}
}

// shellyGen remembers the generation per address: 1, or 2 for all RPC devices.
var shellyGen sync.Map

// get calls the device and answers its auth challenge if there is one.
func (shelly) get(ctx context.Context, in *Instance, path string, out any) error {
	u := strings.TrimRight(in.BaseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	resp, err := in.Client.Do(req)
	if err != nil {
		return friendlyNetErr(in.Lang, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("WWW-Authenticate")
		resp.Body.Close()
		if in.Values["password"] == "" {
			return in.Errorf("Das Gerät ist mit einem Passwort geschützt – bitte das Passwort eintragen")
		}
		req, _ = http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if strings.HasPrefix(strings.ToLower(challenge), "digest") {
			req.Header.Set("Authorization", digestAuth(challenge, "admin", in.Values["password"], http.MethodGet, req.URL.RequestURI()))
		} else {
			req.SetBasicAuth(firstNonEmpty(in.V("username"), "admin"), in.Values["password"])
		}
		if resp, err = in.Client.Do(req); err != nil {
			return friendlyNetErr(in.Lang, err)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			resp.Body.Close()
			return in.Errorf("Benutzername oder Passwort falsch")
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return &HTTPError{Status: resp.StatusCode, Body: strings.TrimSpace(string(b)), Lang: in.Lang}
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return notJSON{in.Errorf("unerwartete Antwort (kein gültiges JSON) – stimmt der Dienst-Typ? (%v)", err)}
	}
	return nil
}

var digestParamRe = regexp.MustCompile(`(\w+)="?([^",]*)"?`)

// digestAuth answers an HTTP Digest challenge (RFC 7616): SHA-256 as Shelly
// Gen2+ asks for, MD5 as the FRITZ!Box (TR-064) does.
func digestAuth(challenge, user, pass, method, uri string) string {
	p := map[string]string{}
	for _, m := range digestParamRe.FindAllStringSubmatch(challenge, -1) {
		p[strings.ToLower(m[1])] = m[2]
	}
	algo := "MD5"
	h := func(s string) string { sum := md5.Sum([]byte(s)); return hex.EncodeToString(sum[:]) }
	if strings.EqualFold(p["algorithm"], "SHA-256") {
		algo = "SHA-256"
		h = func(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
	}
	cb := make([]byte, 8)
	rand.Read(cb)
	cnonce := base64.RawURLEncoding.EncodeToString(cb)
	ha1 := h(user + ":" + p["realm"] + ":" + pass)
	ha2 := h(method + ":" + uri)
	resp := h(ha1 + ":" + p["nonce"] + ":00000001:" + cnonce + ":auth:" + ha2)
	return fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", algorithm=%s, response="%s", qop=auth, nc=00000001, cnonce="%s"`,
		user, p["realm"], p["nonce"], uri, algo, resp, cnonce)
}

// gen finds out which interface the device speaks (/shelly needs no login).
func (s shelly) gen(ctx context.Context, in *Instance) (int, error) {
	if g, ok := shellyGen.Load(in.BaseURL); ok {
		return g.(int), nil
	}
	var info struct {
		Gen  int    `json:"gen"`
		Type string `json:"type"`
		App  string `json:"app"`
	}
	if err := s.get(ctx, in, "/shelly", &info); err != nil {
		if errorsIsNotJSON(err) {
			return 0, in.Errorf("unter dieser Adresse läuft %s, nicht %s", in.T("ein anderer Dienst"), "Shelly")
		}
		return 0, err
	}
	g := 1
	if info.Gen >= 2 {
		g = 2
	} else if info.Type == "" && info.App == "" {
		return 0, in.Errorf("unter dieser Adresse läuft %s, nicht %s", in.T("ein anderer Dienst"), "Shelly")
	}
	shellyGen.Store(in.BaseURL, g)
	return g, nil
}

func errorsIsNotJSON(err error) bool { _, ok := err.(notJSON); return ok }

func (s shelly) Test(ctx context.Context, in *Instance) error {
	_, err := s.Fetch(ctx, in)
	return err
}

// shellyChannel is one switch, light or cover of a device.
type shellyChannel struct {
	Kind  string // switch | light | cover
	ID    int
	Name  string
	On    bool
	State string // covers: open, closed, opening, closing, stopped
	Pos   *float64
}

// shellyReading collects what the device reports, whatever its generation.
type shellyReading struct {
	Channels         []shellyChannel
	Power, EnergyWh  *float64
	TempC, Humidity  *float64
	Battery, RSSI    *float64
	Voltage, Current *float64
	Uptime           *float64 // seconds
	Update           string
	Overtemp, Online bool
}

func addF(dst **float64, v float64) {
	if *dst == nil {
		*dst = new(float64)
	}
	**dst += v
}

func setF(dst **float64, v *float64) {
	if v != nil {
		*dst = v
	}
}

func (s shelly) read(ctx context.Context, in *Instance) (*shellyReading, error) {
	g, err := s.gen(ctx, in)
	if err != nil {
		return nil, err
	}
	if g == 1 {
		return s.readGen1(ctx, in)
	}
	return s.readGen2(ctx, in)
}

func (s shelly) readGen1(ctx context.Context, in *Instance) (*shellyReading, error) {
	var st struct {
		Relays []struct {
			IsOn      bool `json:"ison"`
			Overpower bool `json:"overpower"`
		} `json:"relays"`
		Lights []struct {
			IsOn bool `json:"ison"`
		} `json:"lights"`
		Rollers []struct {
			State string   `json:"state"`
			Pos   *float64 `json:"current_pos"`
		} `json:"rollers"`
		Meters []struct {
			Power *float64 `json:"power"`
			Total *float64 `json:"total"` // watt-minutes
		} `json:"meters"`
		EMeters []struct {
			Power   *float64 `json:"power"`
			Total   *float64 `json:"total"` // Wh
			Voltage *float64 `json:"voltage"`
			Current *float64 `json:"current"`
		} `json:"emeters"`
		Uptime      *float64 `json:"uptime"`
		Temperature *float64 `json:"temperature"`
		Overtemp    bool     `json:"overtemperature"`
		Tmp         *struct {
			TC *float64 `json:"tC"`
		} `json:"tmp"`
		Hum *struct {
			Value *float64 `json:"value"`
		} `json:"hum"`
		Bat *struct {
			Value *float64 `json:"value"`
		} `json:"bat"`
		WiFi *struct {
			RSSI *float64 `json:"rssi"`
		} `json:"wifi_sta"`
		Update *struct {
			HasUpdate  bool   `json:"has_update"`
			NewVersion string `json:"new_version"`
		} `json:"update"`
	}
	if err := s.get(ctx, in, "/status", &st); err != nil {
		return nil, err
	}
	var cfg struct {
		Relays []struct {
			Name string `json:"name"`
		} `json:"relays"`
		Lights []struct {
			Name string `json:"name"`
		} `json:"lights"`
		Rollers []struct {
			Name string `json:"name"`
		} `json:"rollers"`
	}
	s.get(ctx, in, "/settings", &cfg) // names only; the device works without them
	name := func(list []struct {
		Name string `json:"name"`
	}, i int) string {
		if i < len(list) {
			return list[i].Name
		}
		return ""
	}
	r := &shellyReading{Overtemp: st.Overtemp}
	for i, x := range st.Relays {
		r.Channels = append(r.Channels, shellyChannel{Kind: "switch", ID: i, Name: name(cfg.Relays, i), On: x.IsOn})
	}
	for i, x := range st.Lights {
		r.Channels = append(r.Channels, shellyChannel{Kind: "light", ID: i, Name: name(cfg.Lights, i), On: x.IsOn})
	}
	for i, x := range st.Rollers {
		r.Channels = append(r.Channels, shellyChannel{Kind: "cover", ID: i, Name: name(cfg.Rollers, i), State: x.State, Pos: x.Pos})
	}
	for _, m := range st.Meters {
		if m.Power != nil {
			addF(&r.Power, *m.Power)
		}
		if m.Total != nil {
			addF(&r.EnergyWh, *m.Total/60)
		}
	}
	for _, m := range st.EMeters {
		if m.Power != nil {
			addF(&r.Power, *m.Power)
		}
		if m.Total != nil {
			addF(&r.EnergyWh, *m.Total)
		}
		setF(&r.Voltage, m.Voltage)
		if m.Current != nil {
			addF(&r.Current, *m.Current)
		}
	}
	setF(&r.Uptime, st.Uptime)
	setF(&r.TempC, st.Temperature)
	if st.Tmp != nil {
		setF(&r.TempC, st.Tmp.TC)
	}
	if st.Hum != nil {
		setF(&r.Humidity, st.Hum.Value)
	}
	if st.Bat != nil {
		setF(&r.Battery, st.Bat.Value)
	}
	if st.WiFi != nil {
		setF(&r.RSSI, st.WiFi.RSSI)
	}
	if st.Update != nil && st.Update.HasUpdate {
		r.Update = firstNonEmpty(st.Update.NewVersion, "?")
	}
	return r, nil
}

func (s shelly) readGen2(ctx context.Context, in *Instance) (*shellyReading, error) {
	var st map[string]json.RawMessage
	if err := s.get(ctx, in, "/rpc/Shelly.GetStatus", &st); err != nil {
		return nil, err
	}
	var cfg map[string]struct {
		Name string `json:"name"`
	}
	s.get(ctx, in, "/rpc/Shelly.GetConfig", &cfg) // names only
	r := &shellyReading{}
	keys := make([]string, 0, len(st))
	for k := range st {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		kind, idStr, _ := strings.Cut(k, ":")
		id, _ := strconv.Atoi(idStr)
		var c struct {
			Output     *bool    `json:"output"`
			State      string   `json:"state"`
			Pos        *float64 `json:"current_pos"`
			APower     *float64 `json:"apower"`
			ActPower   *float64 `json:"act_power"`
			TotalPower *float64 `json:"total_act_power"`
			TC         *float64 `json:"tC"`
			RH         *float64 `json:"rh"`
			AEnergy    *struct {
				Total *float64 `json:"total"`
			} `json:"aenergy"`
			Temperature *struct {
				TC *float64 `json:"tC"`
			} `json:"temperature"`
			Battery *struct {
				Percent *float64 `json:"percent"`
			} `json:"battery"`
			RSSI    *float64 `json:"rssi"`
			Voltage *float64 `json:"voltage"`
			Current *float64 `json:"current"`
			Uptime  *float64 `json:"uptime"`
			Errors  []string `json:"errors"`
			Updates map[string]struct {
				Version string `json:"version"`
			} `json:"available_updates"`
		}
		if json.Unmarshal(st[k], &c) != nil {
			continue
		}
		name := cfg[k].Name
		switch kind {
		case "switch", "light":
			if c.Output != nil {
				r.Channels = append(r.Channels, shellyChannel{Kind: kind, ID: id, Name: name, On: *c.Output})
			}
		case "cover":
			r.Channels = append(r.Channels, shellyChannel{Kind: "cover", ID: id, Name: name, State: c.State, Pos: c.Pos})
		case "temperature":
			setF(&r.TempC, c.TC)
			continue
		case "humidity":
			setF(&r.Humidity, c.RH)
			continue
		case "devicepower":
			if c.Battery != nil {
				setF(&r.Battery, c.Battery.Percent)
			}
			continue
		case "wifi":
			setF(&r.RSSI, c.RSSI)
			continue
		case "sys":
			setF(&r.Uptime, c.Uptime)
			if u, ok := c.Updates["stable"]; ok {
				r.Update = firstNonEmpty(u.Version, "?")
			}
			continue
		}
		// power and energy of switches, covers, meters (pm1, em, em1)
		switch {
		case c.APower != nil:
			addF(&r.Power, *c.APower)
		case c.ActPower != nil:
			addF(&r.Power, *c.ActPower)
		case c.TotalPower != nil:
			addF(&r.Power, *c.TotalPower)
		}
		if c.AEnergy != nil && c.AEnergy.Total != nil {
			addF(&r.EnergyWh, *c.AEnergy.Total)
		}
		setF(&r.Voltage, c.Voltage)
		if c.Current != nil {
			addF(&r.Current, *c.Current)
		}
		if c.Temperature != nil && c.Temperature.TC != nil && r.TempC == nil {
			r.TempC = c.Temperature.TC
		}
		for _, e := range c.Errors {
			r.Overtemp = r.Overtemp || e == "overtemp"
		}
	}
	return r, nil
}

func (s shelly) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	r, err := s.read(ctx, in)
	if err != nil {
		return nil, err
	}
	num := func(v float64, dec int, unit string) string {
		str := strconv.FormatFloat(v, 'f', dec, 64)
		if in.Lang != "en" {
			str = strings.Replace(str, ".", ",", 1)
		}
		return str + " " + unit
	}
	w := &Widget{}
	if r.Power != nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("Leistung"), Value: num(*r.Power, 1, "W")})
	}
	if r.EnergyWh != nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("Energie"), Value: num(*r.EnergyWh/1000, 2, "kWh")})
	}
	if r.TempC != nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("Temperatur"), Value: num(*r.TempC, 1, "°C"), Tone: toneIf(r.Overtemp, "bad")})
	}
	if r.Humidity != nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("Luftfeuchte"), Value: num(*r.Humidity, 0, "%")})
	}
	if r.Battery != nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("Akku"), Value: num(*r.Battery, 0, "%"), Tone: toneIf(*r.Battery < 20, "warn")})
	}
	if r.RSSI != nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("WLAN"), Value: num(*r.RSSI, 0, "dBm"), Tone: toneIf(*r.RSSI < -80, "warn")})
	}
	if r.Voltage != nil && *r.Voltage > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Spannung"), Value: num(*r.Voltage, 0, "V")})
	}
	if r.Current != nil && *r.Current > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Strom"), Value: num(*r.Current, 2, "A")})
	}
	if r.Uptime != nil && *r.Uptime > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Laufzeit"), Value: uptimeText(in, *r.Uptime)})
	}
	for _, c := range r.Channels {
		w.Items = append(w.Items, s.channelItem(in, c, len(r.Channels)))
	}
	if r.Overtemp {
		w.Alerts = append(w.Alerts, in.T("Das Gerät ist überhitzt"))
	}
	if r.Update != "" {
		w.Alerts = append(w.Alerts, in.T("Firmware-Update verfügbar: %s", r.Update))
	}
	return w, nil
}

func (shelly) channelItem(in *Instance, c shellyChannel, total int) Item {
	kind := map[string]string{"switch": "Schalter", "light": "Licht", "cover": "Rollladen"}[c.Kind]
	title := c.Name
	if title == "" {
		title = in.T(kind)
		if total > 1 {
			title += fmt.Sprintf(" %d", c.ID+1)
		}
	}
	it := Item{Title: title}
	params := func(op string) map[string]string {
		return map[string]string{"kind": c.Kind, "id": strconv.Itoa(c.ID), "op": op}
	}
	if c.Kind == "cover" {
		it.Badge = in.T(map[string]string{"open": "Offen", "closed": "Geschlossen", "opening": "Öffnet", "closing": "Schließt", "stopped": "Gestoppt"}[c.State])
		if c.Pos != nil {
			it.Progress = pct(*c.Pos)
			it.Badge = fmt.Sprintf("%.0f %%", *c.Pos)
		}
		it.Actions = []Action{{ID: "set", Label: in.T("Öffnen"), Params: params("open")}, {ID: "set", Label: in.T("Stopp"), Params: params("stop")}, {ID: "set", Label: in.T("Schließen"), Params: params("close")}}
		return it
	}
	it.Badge = in.T("Aus")
	if c.On {
		it.Badge = in.T("An")
	}
	it.Actions = []Action{{ID: "set", Label: in.T("Umschalten"), Params: params("toggle")}}
	return it
}

func (s shelly) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	id, err := strconv.Atoi(p["id"])
	kind, op := p["kind"], p["op"]
	valid := map[string][]string{"switch": {"toggle"}, "light": {"toggle"}, "cover": {"open", "close", "stop"}}
	ok := false
	for _, o := range valid[kind] {
		ok = ok || o == op
	}
	if action != "set" || err != nil || id < 0 || id > 16 || !ok {
		return in.Errorf("unbekannte Aktion %q", action)
	}
	g, err := s.gen(ctx, in)
	if err != nil {
		return err
	}
	var path string
	if g == 1 {
		switch kind {
		case "switch":
			path = fmt.Sprintf("/relay/%d?turn=toggle", id)
		case "light":
			path = fmt.Sprintf("/light/%d?turn=toggle", id)
		case "cover":
			path = fmt.Sprintf("/roller/%d?go=%s", id, op)
		}
	} else {
		method := map[string]string{"switch": "Switch.Toggle", "light": "Light.Toggle"}[kind]
		if kind == "cover" {
			method = "Cover." + strings.ToUpper(op[:1]) + op[1:]
		}
		path = "/rpc/" + method + "?" + url.Values{"id": {strconv.Itoa(id)}}.Encode()
	}
	return s.get(ctx, in, path, nil)
}
