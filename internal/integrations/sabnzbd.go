package integrations

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

type sabnzbd struct{}

func init() { register(sabnzbd{}) }

func (sabnzbd) Def() Definition {
	return Definition{
		ID: "sabnzbd", Name: "SABnzbd", Icon: "sabnzbd", Category: "Downloads",
		Description: "Download-Geschwindigkeit, Warteschlange, Pause/Fortsetzen",
		Fields: []Field{{Key: "apiKey", Label: "API-Key", Type: "password", Required: true, Secret: true,
			Help: "Config → General → Security → API Key"}},
		HasWidget: true, Images: []string{"sabnzbd"}, Port: 8080,
	}
}

func (sabnzbd) api(ctx context.Context, in *Instance, mode string, extra url.Values, out any) error {
	q := url.Values{"mode": {mode}, "output": {"json"}, "apikey": {in.V("apiKey")}}
	for k, v := range extra {
		q[k] = v
	}
	var wrapped struct {
		Error string `json:"error"`
	}
	if out == nil {
		out = &wrapped
	}
	if err := in.do(ctx, request{path: "/api", query: q}, out); err != nil {
		return err
	}
	if wrapped.Error != "" {
		return fmt.Errorf("SABnzbd: %s", wrapped.Error)
	}
	return nil
}

func (s sabnzbd) Test(ctx context.Context, in *Instance) error {
	var res struct {
		Error string    `json:"error"`
		Queue *struct{} `json:"queue"`
	}
	if err := s.api(ctx, in, "queue", url.Values{"limit": {"1"}}, &res); err != nil {
		return err
	}
	if res.Error != "" {
		return fmt.Errorf("SABnzbd: %s", res.Error)
	}
	return nil
}

func (s sabnzbd) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var res struct {
		Error string `json:"error"`
		Queue struct {
			Status   string `json:"status"`
			Paused   bool   `json:"paused"`
			KBPerSec string `json:"kbpersec"`
			MBLeft   string `json:"mbleft"`
			Timeleft string `json:"timeleft"`
			Total    int    `json:"noofslots_total"`
			Slots    []struct {
				Filename   string `json:"filename"`
				Percentage string `json:"percentage"`
				Timeleft   string `json:"timeleft"`
				MB         string `json:"mb"`
				Status     string `json:"status"`
			} `json:"slots"`
		} `json:"queue"`
	}
	if err := s.api(ctx, in, "queue", url.Values{"limit": {"5"}}, &res); err != nil {
		return nil, err
	}
	if res.Error != "" {
		return nil, fmt.Errorf("SABnzbd: %s", res.Error)
	}
	q := res.Queue
	kbps, _ := strconv.ParseFloat(q.KBPerSec, 64)
	mbleft, _ := strconv.ParseFloat(q.MBLeft, 64)

	w := &Widget{ItemsTitle: "Warteschlange"}
	speed := humanBytes(kbps*1024) + "/s"
	if q.Paused {
		speed = "Pausiert"
	}
	w.Stats = []Stat{
		{Label: "Tempo", Value: speed, Tone: toneIf(q.Paused, "warn")},
		{Label: "Downloads", Value: fmt.Sprint(q.Total)},
		{Label: "Verbleibend", Value: humanBytes(mbleft * 1024 * 1024)},
	}
	if q.Total > 0 && !q.Paused {
		w.Stats = append(w.Stats, Stat{Label: "Restzeit", Value: q.Timeleft})
	}
	for _, sl := range q.Slots {
		p, _ := strconv.ParseFloat(sl.Percentage, 64)
		mb, _ := strconv.ParseFloat(sl.MB, 64)
		w.Items = append(w.Items, Item{
			Title:    strings.ReplaceAll(sl.Filename, ".", " "),
			Subtitle: fmt.Sprintf("%s · %s · noch %s", sl.Status, humanBytes(mb*1024*1024), sl.Timeleft),
			Progress: pct(p),
			URL:      in.Link("/"),
		})
	}
	if q.Paused {
		w.Actions = []Action{{ID: "resume", Label: "Fortsetzen", Tone: "primary"}}
	} else {
		w.Actions = []Action{{ID: "pause", Label: "Pausieren"}}
	}
	return w, nil
}

func (s sabnzbd) Action(ctx context.Context, in *Instance, action string, _ map[string]string) error {
	if action != "pause" && action != "resume" {
		return fmt.Errorf("unbekannte Aktion %q", action)
	}
	return s.api(ctx, in, action, nil, nil)
}
