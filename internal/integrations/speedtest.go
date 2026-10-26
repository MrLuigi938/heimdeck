package integrations

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"
)

// Speedtest Tracker: API v1 with a Sanctum token
// (github.com/alexjustesen/speedtest-tracker, routes/api/v1). Without a token
// the deprecated public endpoint /api/speedtest/latest is used.

type speedtest struct{}

func init() { register(speedtest{}) }

func (speedtest) Def() Definition {
	return Definition{
		ID: "speedtest-tracker", Name: "Speedtest Tracker", Icon: "speedtest-tracker", Category: "Netzwerk",
		Description: "Letzte Messung (Download, Upload, Ping), Verlauf, Test per Knopfdruck starten",
		Fields: []Field{{Key: "apiKey", Label: "API-Token", Type: "password", Secret: true,
			Help: "Einstellungen → API-Tokens: Rechte „results:read“ und für den Knopf „speedtests:run“. Ohne Token wird nur das letzte Ergebnis angezeigt."}},
		HasWidget: true, Images: []string{"speedtest-tracker"}, Port: 80,
	}
}

func (speedtest) call(ctx context.Context, in *Instance, method, path string, q url.Values, body, out any) error {
	return in.do(ctx, request{method: method, path: path, query: q, body: body,
		headers: map[string]string{"Authorization": "Bearer " + in.V("apiKey")}}, out)
}

type speedResult struct {
	ID        int      `json:"id"`
	Ping      *float64 `json:"ping"`
	Download  *float64 `json:"download"` // bytes/s (v1) or Mbit/s (legacy)
	Upload    *float64 `json:"upload"`
	Healthy   *bool    `json:"healthy"`
	Status    string   `json:"status"`
	Failed    bool     `json:"failed"`
	CreatedAt string   `json:"created_at"`
}

func mbit(v *float64, bytes bool) string {
	if v == nil {
		return "–"
	}
	n := *v
	if bytes {
		n = n * 8 / 1e6
	}
	return fmt.Sprintf("%.0f Mbit/s", n)
}

func (s speedtest) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var latest struct {
		Data speedResult `json:"data"`
	}
	v1 := in.V("apiKey") != ""
	path := "/api/speedtest/latest"
	if v1 {
		path = "/api/v1/results/latest"
	}
	err := s.call(ctx, in, "GET", path, nil, nil, &latest)
	var he *HTTPError
	if errors.As(err, &he) && he.Status == 404 && path == "/api/v1/results/latest" {
		err = nil // no results yet
	}
	if err != nil {
		return nil, err
	}
	r := latest.Data
	w := &Widget{}
	if r.ID != 0 {
		ping := "–"
		if r.Ping != nil {
			ping = fmt.Sprintf("%.0f ms", *r.Ping)
		}
		bad := r.Failed || r.Status == "failed" || (r.Healthy != nil && !*r.Healthy)
		w.Stats = []Stat{
			{Label: in.T("Download"), Value: mbit(r.Download, v1), Tone: toneIf(bad, "warn")},
			{Label: in.T("Upload"), Value: mbit(r.Upload, v1), Tone: toneIf(bad, "warn")},
			{Label: in.T("Ping"), Value: ping},
		}
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", r.CreatedAt, time.Local); err == nil {
			w.Stats = append(w.Stats, Stat{Label: in.T("Gemessen"), Value: in.Since(t)})
		} else if t, err := time.Parse(time.RFC3339Nano, r.CreatedAt); err == nil {
			w.Stats = append(w.Stats, Stat{Label: in.T("Gemessen"), Value: in.Since(t)})
		}
		if r.Failed || r.Status == "failed" {
			w.Alerts = append(w.Alerts, in.T("Die letzte Messung ist fehlgeschlagen"))
		} else if r.Healthy != nil && !*r.Healthy {
			w.Alerts = append(w.Alerts, in.T("Die letzte Messung lag unter den eingestellten Grenzwerten"))
		}
	}
	if !v1 {
		return w, nil
	}

	var list struct {
		Data []speedResult `json:"data"`
	}
	q := url.Values{"sort": {"-created_at"}, "page[size]": {"20"}}
	if err := s.call(ctx, in, "GET", "/api/v1/results", q, nil, &list); err == nil && len(list.Data) > 1 {
		// Averages and failures of the last (up to) 20 measurements.
		var down, up, ping float64
		n, pings, failed := 0, 0, 0
		for _, h := range list.Data {
			if h.Failed || h.Status == "failed" {
				failed++
				continue
			}
			if h.Download != nil && h.Upload != nil {
				down += *h.Download
				up += *h.Upload
				n++
			}
			if h.Ping != nil {
				ping += *h.Ping
				pings++
			}
		}
		if n > 1 {
			avgDown, avgUp := down/float64(n), up/float64(n)
			w.Stats = append(w.Stats, Stat{Label: in.T("Ø Download"), Value: mbit(&avgDown, true)}, Stat{Label: in.T("Ø Upload"), Value: mbit(&avgUp, true)})
		}
		if pings > 1 {
			w.Stats = append(w.Stats, Stat{Label: in.T("Ø Ping"), Value: fmt.Sprintf("%.0f ms", ping/float64(pings))})
		}
		w.Stats = append(w.Stats, Stat{Label: in.T("Fehlgeschlagen"), Value: in.Num(failed), Tone: toneIf(failed > 0, "warn")})
		w.ItemsTitle = in.T("Verlauf")
		for _, h := range list.Data[:min(5, len(list.Data))] {
			it := Item{Title: "↓ " + mbit(h.Download, true) + "  ↑ " + mbit(h.Upload, true), URL: in.Link("/admin/results")}
			if t, err := time.ParseInLocation("2006-01-02 15:04:05", h.CreatedAt, time.Local); err == nil {
				it.Subtitle = in.Since(t)
			}
			if h.Ping != nil {
				it.Badge = fmt.Sprintf("%.0f ms", *h.Ping)
			}
			if h.Status != "" && h.Status != "completed" {
				it.Badge = in.Status(h.Status)
			}
			w.Items = append(w.Items, it)
		}
	}
	w.Actions = []Action{{ID: "run", Label: in.T("Test starten"), Tone: "primary"}}
	return w, nil
}

func (s speedtest) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	if action != "run" {
		return in.Errorf("unbekannte Aktion %q", action)
	}
	if in.V("apiKey") == "" {
		return in.Errorf("Dafür wird ein API-Token mit dem Recht „speedtests:run“ benötigt")
	}
	err := s.call(ctx, in, "POST", "/api/v1/speedtests/run", nil, map[string]any{}, nil)
	var he *HTTPError
	if errors.As(err, &he) && he.Status == 403 {
		return in.Errorf("Dafür wird ein API-Token mit dem Recht „speedtests:run“ benötigt")
	}
	return err
}
