package integrations

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

// UniFi: the official Network Integration API with an API key
// (UniFi Network → Settings → Control Plane → Integrations). On UniFi OS
// consoles (Cloud Key, UDM, …) it lives below /proxy/network/integration,
// on a self-hosted Network Application below /integration.

type unifi struct{}

func init() { register(unifi{}) }

func (unifi) Def() Definition {
	return Definition{
		ID: "unifi", Name: "UniFi", Icon: "ubiquiti-unifi", Category: "Netzwerk",
		Description: "Clients (WLAN/LAN/VPN), Internet-Durchsatz, Auslastung des Gateways, Geräte und ihr Status, Firmware-Updates, Suche nach Clients und Geräten, Geräte neu starten",
		Fields: []Field{
			{Key: "apiKey", Label: "API-Key", Type: "password", Required: true, Secret: true,
				Help: "UniFi Network → Einstellungen → Control Plane → Integrationen → API-Schlüssel erstellen"},
			{Key: "site", Label: "Standort", Type: "text", Help: "Optional, Name des Standorts. Leer: der erste."},
		},
		HasWidget: true, HasSearch: true, Images: []string{"unifi"}, Port: 443,
	}
}

// unifiPrefix remembers which of the two API locations a controller uses.
var unifiPrefix sync.Map

func (unifi) call(ctx context.Context, in *Instance, method, path string, q url.Values, body, out any) error {
	h := map[string]string{"X-API-KEY": in.V("apiKey")}
	if p, ok := unifiPrefix.Load(in.BaseURL); ok {
		return in.do(ctx, request{method: method, path: p.(string) + path, query: q, body: body, headers: h}, out)
	}
	var err error
	for _, p := range []string{"/proxy/network/integration", "/integration"} {
		err = in.do(ctx, request{method: method, path: p + path, query: q, body: body, headers: h}, out)
		var he *HTTPError
		if errors.As(err, &he) && he.Status == 404 {
			continue
		}
		if err == nil {
			unifiPrefix.Store(in.BaseURL, p)
		}
		return err
	}
	return err
}

type unifiPage[T any] struct {
	TotalCount int `json:"totalCount"`
	Data       []T `json:"data"`
}

type unifiDevice struct {
	ID                string   `json:"id"`
	Name              string   `json:"name"`
	Model             string   `json:"model"`
	State             string   `json:"state"`
	IP                string   `json:"ipAddress"`
	FirmwareUpdatable bool     `json:"firmwareUpdatable"`
	Features          []string `json:"features"` // e.g. switching, accessPoint, gateway
}

// unifiDeviceStats is /devices/{id}/statistics/latest.
type unifiDeviceStats struct {
	Uptime float64  `json:"uptimeSec"`
	CPU    *float64 `json:"cpuUtilizationPct"`
	Memory *float64 `json:"memoryUtilizationPct"`
	Uplink *struct {
		TX float64 `json:"txRateBps"`
		RX float64 `json:"rxRateBps"`
	} `json:"uplink"`
}

type unifiClient struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	IP          string `json:"ipAddress"`
	MAC         string `json:"macAddress"`
	ConnectedAt string `json:"connectedAt"`
}

func (u unifi) Test(ctx context.Context, in *Instance) error {
	var info struct {
		Version string `json:"applicationVersion"`
	}
	return u.call(ctx, in, "GET", "/v1/info", nil, nil, &info)
}

func (u unifi) site(ctx context.Context, in *Instance) (string, error) {
	var sites unifiPage[struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Ref  string `json:"internalReference"`
	}]
	if err := u.call(ctx, in, "GET", "/v1/sites", url.Values{"limit": {"200"}}, nil, &sites); err != nil {
		return "", err
	}
	want := in.V("site")
	for _, s := range sites.Data {
		if want == "" || strings.EqualFold(s.Name, want) || strings.EqualFold(s.Ref, want) {
			return s.ID, nil
		}
	}
	if want != "" {
		return "", in.Errorf("Standort „%s“ nicht gefunden", want)
	}
	return "", in.Errorf("Keine Standorte gefunden")
}

func (u unifi) devices(ctx context.Context, in *Instance, site string) ([]unifiDevice, error) {
	var page unifiPage[unifiDevice]
	err := u.call(ctx, in, "GET", "/v1/sites/"+site+"/devices", url.Values{"limit": {"200"}}, nil, &page)
	return page.Data, err
}

func (u unifi) countClients(ctx context.Context, in *Instance, site, filter string) (int, error) {
	q := url.Values{"limit": {"1"}}
	if filter != "" {
		q.Set("filter", filter)
	}
	var page unifiPage[unifiClient]
	err := u.call(ctx, in, "GET", "/v1/sites/"+site+"/clients", q, nil, &page)
	return page.TotalCount, err
}

func (u unifi) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	site, err := u.site(ctx, in)
	if err != nil {
		return nil, err
	}
	total, err := u.countClients(ctx, in, site, "")
	if err != nil {
		return nil, err
	}
	wifi, _ := u.countClients(ctx, in, site, "type.eq('WIRELESS')")
	wired, errWired := u.countClients(ctx, in, site, "type.eq('WIRED')")
	vpn, errVPN := u.countClients(ctx, in, site, "type.eq('VPN')")
	devices, err := u.devices(ctx, in, site)
	if err != nil {
		return nil, err
	}
	online, updates := 0, 0
	var gateway *unifiDevice
	w := &Widget{}
	for i, d := range devices {
		if d.State == "ONLINE" {
			online++
		} else {
			w.Items = append(w.Items, u.deviceItem(in, site, d))
		}
		if d.FirmwareUpdatable {
			updates++
		}
		if gateway == nil && slices.Contains(d.Features, "gateway") {
			gateway = &devices[i]
		}
	}
	w.Stats = []Stat{
		{Label: in.T("Clients"), Value: in.Num(total)},
		{Label: in.T("WLAN"), Value: in.Num(wifi)},
	}
	if errWired == nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("LAN"), Value: in.Num(wired)})
	}
	if errVPN == nil && vpn > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("VPN"), Value: in.Num(vpn)})
	}
	w.Stats = append(w.Stats, Stat{Label: in.T("Geräte online"), Value: fmt.Sprintf("%d/%d", online, len(devices)), Tone: toneIf(online < len(devices), "warn")})
	if updates > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Updates"), Value: in.Num(updates), Tone: "warn"})
	}
	// The gateway's uplink is the internet connection.
	if gateway != nil && gateway.State == "ONLINE" {
		var st unifiDeviceStats
		if u.call(ctx, in, "GET", "/v1/sites/"+site+"/devices/"+gateway.ID+"/statistics/latest", nil, nil, &st) == nil {
			if st.Uplink != nil {
				w.Stats = append(w.Stats,
					Stat{Label: "↓ " + in.T("Internet"), Value: mbitText(in, st.Uplink.RX)},
					Stat{Label: "↑ " + in.T("Internet"), Value: mbitText(in, st.Uplink.TX)})
			}
			if st.CPU != nil {
				w.Stats = append(w.Stats, Stat{Label: in.T("Gateway-CPU"), Value: fmt.Sprintf("%.0f %%", *st.CPU), Tone: toneIf(*st.CPU > 90, "warn")})
			}
			if st.Memory != nil {
				w.Stats = append(w.Stats, Stat{Label: in.T("Gateway-RAM"), Value: fmt.Sprintf("%.0f %%", *st.Memory), Tone: toneIf(*st.Memory > 90, "warn")})
			}
			if st.Uptime > 0 {
				w.Stats = append(w.Stats, Stat{Label: in.T("Laufzeit"), Value: uptimeText(in, st.Uptime)})
			}
		}
	}
	if len(w.Items) > 0 {
		w.ItemsTitle = in.T("Geräte mit Problemen")
	} else if len(devices) > 0 {
		// Everything fine: an overview of the devices instead of an empty list.
		w.ItemsTitle = in.T("Geräte")
		sorted := slices.Clone(devices)
		slices.SortFunc(sorted, func(a, b unifiDevice) int { return strings.Compare(strings.ToLower(a.Name), strings.ToLower(b.Name)) })
		for _, d := range sorted[:min(6, len(sorted))] {
			w.Items = append(w.Items, u.deviceItem(in, site, d))
		}
	}
	return w, nil
}

// mbitText shows a rate in bits per second as Mbit/s.
func mbitText(in *Instance, bps float64) string {
	s := fmt.Sprintf("%.1f Mbit/s", bps/1e6)
	if in.Lang != "en" {
		s = strings.Replace(s, ".", ",", 1)
	}
	return s
}

func (u unifi) deviceState(in *Instance, s string) string {
	switch s {
	case "ONLINE":
		return in.T("Online")
	case "OFFLINE":
		return in.T("Offline")
	case "UPDATING":
		return in.T("Update läuft")
	case "PENDING_ADOPTION":
		return in.T("Wartet auf Übernahme")
	case "CONNECTION_INTERRUPTED":
		return in.T("Verbindung unterbrochen")
	}
	return strings.ToLower(strings.ReplaceAll(s, "_", " "))
}

func (u unifi) deviceItem(in *Instance, site string, d unifiDevice) Item {
	sub := []string{d.Model}
	if d.IP != "" {
		sub = append(sub, d.IP)
	}
	it := Item{Title: d.Name, Subtitle: strings.Join(sub, " · "), Badge: u.deviceState(in, d.State), URL: in.Link("/")}
	if d.State == "ONLINE" || d.State == "CONNECTION_INTERRUPTED" {
		it.Actions = []Action{{ID: "restart", Label: in.T("Neu starten"), Tone: "danger",
			Params: map[string]string{"site": site, "device": d.ID}, Confirm: in.T("„%s“ neu starten?", d.Name)}}
	}
	return it
}

// Search matches devices and clients by name, IP or MAC address. The API cannot
// filter clients by name, so up to 1000 clients are loaded and filtered here.
func (u unifi) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	site, err := u.site(ctx, in)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	match := func(s ...string) bool {
		for _, v := range s {
			if v != "" && strings.Contains(strings.ToLower(v), q) {
				return true
			}
		}
		return false
	}
	out := []Item{}
	devices, err := u.devices(ctx, in, site)
	if err != nil {
		return nil, err
	}
	for _, d := range devices {
		if match(d.Name, d.Model, d.IP) {
			out = append(out, u.deviceItem(in, site, d))
		}
	}
	for offset := 0; offset < 1000 && len(out) < 8; offset += 200 {
		var page unifiPage[unifiClient]
		if err := u.call(ctx, in, "GET", "/v1/sites/"+site+"/clients", url.Values{"limit": {"200"}, "offset": {strconv.Itoa(offset)}}, nil, &page); err != nil {
			return out, err
		}
		for _, c := range page.Data {
			if !match(c.Name, c.IP, c.MAC) {
				continue
			}
			kind := map[string]string{"WIRED": "LAN", "WIRELESS": "WLAN", "VPN": "VPN"}[c.Type]
			sub := []string{}
			for _, s := range []string{kind, c.IP, c.MAC} {
				if s != "" {
					sub = append(sub, s)
				}
			}
			if t, err := time.Parse(time.RFC3339, c.ConnectedAt); err == nil {
				sub = append(sub, in.T("verbunden %s", in.Since(t)))
			}
			name := c.Name
			if name == "" {
				name = c.MAC
			}
			out = append(out, Item{Title: name, Subtitle: strings.Join(sub, " · "), URL: in.Link("/")})
			if len(out) == 8 {
				break
			}
		}
		if offset+200 >= page.TotalCount {
			break
		}
	}
	return out, nil
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F-]{8,64}$`)

func (u unifi) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	if action != "restart" {
		return in.Errorf("unbekannte Aktion %q", action)
	}
	if !uuidRe.MatchString(p["site"]) || !uuidRe.MatchString(p["device"]) {
		return in.Errorf("ungültige ID")
	}
	return u.call(ctx, in, "POST", "/v1/sites/"+p["site"]+"/devices/"+p["device"]+"/actions", nil, map[string]string{"action": "RESTART"}, nil)
}
