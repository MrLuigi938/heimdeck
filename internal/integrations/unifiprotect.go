package integrations

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// UniFi Protect: the official Protect Integration API on UniFi OS consoles,
// below /proxy/protect/integration, with the same kind of API key as the
// Network API (github.com/uilibs/uiprotect documents the endpoints). Camera
// snapshots need the key and are loaded through Heimdeck's image proxy.

type unifiProtect struct{}

func init() { register(unifiProtect{}) }

func (unifiProtect) Def() Definition {
	return Definition{
		ID: "unifi-protect", Name: "UniFi Protect", Icon: "unifi-protect", Category: "Smart Home",
		Description: "Kameras mit aktuellem Standbild, Kameras offline, Suche nach Kameras",
		Setup: []string{
			"Als lokale Adresse die Adresse deiner UniFi-Konsole eintragen (z. B. https://192.168.1.1) und unter „Weitere Optionen“ „Zertifikat nicht prüfen“ anhaken.",
			"API-Schlüssel erstellen: UniFi OS → Einstellungen → Control Plane → Integrationen → API-Schlüssel erstellen. Derselbe Schlüssel funktioniert auch für die UniFi-Network-Kachel.",
		},
		Fields: []Field{
			{Key: "apiKey", Label: "API-Key", Type: "password", Required: true, Secret: true,
				Help: "Einstellungen → Control Plane → Integrationen → API-Schlüssel erstellen"},
		},
		HasWidget: true, HasSearch: true, Images: []string{"unifi-protect"}, Port: 443,
		Refresh: 30,
	}
}

const protectAPI = "/proxy/protect/integration"

type protectCamera struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"` // CONNECTED, CONNECTING, DISCONNECTED
}

func (unifiProtect) get(ctx context.Context, in *Instance, path string, out any) error {
	return in.do(ctx, request{path: protectAPI + path, headers: map[string]string{"X-API-KEY": in.V("apiKey")}}, out)
}

func (p unifiProtect) Test(ctx context.Context, in *Instance) error {
	var info struct {
		Version string `json:"applicationVersion"`
	}
	return p.get(ctx, in, "/v1/meta/info", &info)
}

func (p unifiProtect) cameras(ctx context.Context, in *Instance) ([]protectCamera, error) {
	var cams []protectCamera
	if err := p.get(ctx, in, "/v1/cameras", &cams); err != nil {
		return nil, err
	}
	sort.SliceStable(cams, func(i, j int) bool { return strings.ToLower(cams[i].Name) < strings.ToLower(cams[j].Name) })
	return cams, nil
}

var protectIDRe = regexp.MustCompile(`^[0-9a-zA-Z]{8,40}$`)

func (unifiProtect) item(in *Instance, c protectCamera) Item {
	it := Item{Title: firstNonEmpty(c.Name, in.T("Kamera")), URL: in.Link("/protect/dashboard")}
	switch c.State {
	case "CONNECTED":
		if protectIDRe.MatchString(c.ID) {
			// A new URL every five minutes, so the browser does not keep an old
			// picture without asking the camera all the time.
			it.Image = in.Proxy(fmt.Sprintf("%s/v1/cameras/%s/snapshot?t=%d", protectAPI, c.ID, time.Now().Unix()/300))
		}
	case "CONNECTING":
		it.Badge = in.T("Verbindet")
	default:
		it.Badge = in.T("Offline")
	}
	return it
}

func (p unifiProtect) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	cams, err := p.cameras(ctx, in)
	if err != nil {
		return nil, err
	}
	online := 0
	w := &Widget{}
	for _, c := range cams {
		if c.State == "CONNECTED" {
			online++
		} else {
			w.Alerts = append(w.Alerts, in.T("Kamera „%s“ ist offline", firstNonEmpty(c.Name, c.ID)))
		}
	}
	w.Stats = []Stat{{Label: in.T("Kameras online"), Value: fmt.Sprintf("%d/%d", online, len(cams)), Tone: toneIf(online < len(cams), "warn")}}
	// Sensors (doors, windows, motion), lights and chimes of the same console.
	var sensors []struct {
		Name     string `json:"name"`
		State    string `json:"state"`
		IsOpened bool   `json:"isOpened"`
		Battery  *struct {
			Percentage *float64 `json:"percentage"`
			IsLow      bool     `json:"isLow"`
		} `json:"batteryStatus"`
	}
	if p.get(ctx, in, "/v1/sensors", &sensors) == nil && len(sensors) > 0 {
		open := 0
		for _, s := range sensors {
			if s.IsOpened {
				open++
			}
			if s.Battery != nil && s.Battery.IsLow {
				w.Alerts = append(w.Alerts, in.T("Sensor „%s“: Batterie fast leer", s.Name))
			}
			if s.State != "" && s.State != "CONNECTED" {
				w.Alerts = append(w.Alerts, in.T("Sensor „%s“ ist offline", s.Name))
			}
		}
		w.Stats = append(w.Stats, Stat{Label: in.T("Sensoren"), Value: in.Num(len(sensors))}, Stat{Label: in.T("Offen"), Value: in.Num(open), Tone: toneIf(open > 0, "warn")})
	}
	for _, kind := range []struct{ path, label string }{{"/v1/lights", "Lichter"}, {"/v1/chimes", "Türgongs"}} {
		var list []struct{}
		if p.get(ctx, in, kind.path, &list) == nil && len(list) > 0 {
			w.Stats = append(w.Stats, Stat{Label: in.T(kind.label), Value: in.Num(len(list))})
		}
	}
	var info struct {
		Version string `json:"applicationVersion"`
	}
	if p.get(ctx, in, "/v1/meta/info", &info) == nil && info.Version != "" {
		w.Stats = append(w.Stats, Stat{Label: in.T("Version"), Value: info.Version})
	}
	// Offline cameras first, then the rest.
	sort.SliceStable(cams, func(i, j int) bool { return cams[i].State != "CONNECTED" && cams[j].State == "CONNECTED" })
	for _, c := range cams[:min(4, len(cams))] {
		w.Items = append(w.Items, p.item(in, c))
	}
	if len(w.Items) > 0 {
		w.ItemsTitle = in.T("Kameras")
		w.Thumbs = true
	}
	return w, nil
}

// Search finds cameras by name and shows their current picture.
func (p unifiProtect) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	cams, err := p.cameras(ctx, in)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(strings.TrimSpace(query))
	out := []Item{}
	for _, c := range cams {
		if strings.Contains(strings.ToLower(c.Name), q) && len(out) < 8 {
			out = append(out, p.item(in, c))
		}
	}
	return out, nil
}

var protectSnapRe = regexp.MustCompile(`^/proxy/protect/integration/v1/cameras/[0-9a-zA-Z]{8,40}/snapshot\?t=[0-9]+$`)

func (unifiProtect) ImageRequest(ctx context.Context, in *Instance, path string) (string, map[string]string, error) {
	if !protectSnapRe.MatchString(path) {
		return "", nil, in.Errorf("nicht gefunden")
	}
	// ?t= only makes the browser load a fresh picture; the API itself gets the
	// plain endpoint, which may reject parameters it does not know.
	path, _, _ = strings.Cut(path, "?")
	return path, map[string]string{"X-API-KEY": in.V("apiKey"), "Accept": "image/jpeg, image/*"}, nil
}
