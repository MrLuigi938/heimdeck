package integrations

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
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
			Paused   bool   `json:"paused"`
			KBPerSec string `json:"kbpersec"`
			MBLeft   string `json:"mbleft"`
			Timeleft string `json:"timeleft"`
			Total    int    `json:"noofslots_total"`
			Slots    []struct {
				ID         string `json:"nzo_id"`
				Filename   string `json:"filename"`
				Percentage string `json:"percentage"`
				Timeleft   string `json:"timeleft"`
				MB         string `json:"mb"`
				MBLeft     string `json:"mbleft"`
				Status     string `json:"status"`
			} `json:"slots"`
		} `json:"queue"`
	}
	// Load the whole queue so paused jobs and the remaining size can be counted.
	if err := s.api(ctx, in, "queue", url.Values{"limit": {"100"}}, &res); err != nil {
		return nil, err
	}
	if res.Error != "" {
		return nil, fmt.Errorf("SABnzbd: %s", res.Error)
	}
	q := res.Queue
	kbps, _ := strconv.ParseFloat(q.KBPerSec, 64)
	mbleft, _ := strconv.ParseFloat(q.MBLeft, 64)

	// Jobs can be paused one by one while the queue itself keeps running;
	// SABnzbd then reports "not paused", 0 jobs and 0 MB left.
	var pausedIDs []string
	slotsLeft := 0.0
	for _, sl := range q.Slots {
		if strings.EqualFold(sl.Status, "Paused") {
			pausedIDs = append(pausedIDs, sl.ID)
		}
		l, _ := strconv.ParseFloat(sl.MBLeft, 64)
		slotsLeft += l
	}
	allJobsPaused := len(q.Slots) > 0 && len(pausedIDs) == len(q.Slots)
	paused := q.Paused || allJobsPaused
	total := max(q.Total, len(q.Slots))
	if mbleft == 0 {
		mbleft = slotsLeft
	}

	w := &Widget{ItemsTitle: in.T("Warteschlange")}
	speed := in.Bytes(kbps*1024) + "/s"
	if paused {
		speed = in.T("Pausiert")
	}
	w.Stats = []Stat{
		{Label: in.T("Tempo"), Value: speed, Tone: toneIf(paused, "warn")},
		{Label: in.T("Downloads"), Value: fmt.Sprint(total)},
		{Label: in.T("Verbleibend"), Value: in.Bytes(mbleft * 1024 * 1024)},
	}
	if total > 0 && !paused && q.Timeleft != "" && q.Timeleft != "0:00:00" {
		w.Stats = append(w.Stats, Stat{Label: in.T("Restzeit"), Value: q.Timeleft})
	}
	for i, sl := range q.Slots {
		if i == 5 {
			break
		}
		p, _ := strconv.ParseFloat(sl.Percentage, 64)
		mb, _ := strconv.ParseFloat(sl.MB, 64)
		sub := in.Status(sl.Status) + " · " + in.Bytes(mb*1024*1024)
		if sl.Timeleft != "" && sl.Timeleft != "0:00:00" {
			sub += " · " + in.T("noch %s", sl.Timeleft)
		}
		w.Items = append(w.Items, Item{
			Title:    strings.ReplaceAll(sl.Filename, ".", " "),
			Subtitle: sub,
			Progress: pct(p),
			URL:      in.Link("/"),
		})
	}
	switch {
	case q.Paused:
		w.Actions = []Action{{ID: "resume", Label: in.T("Fortsetzen"), Tone: "primary"}}
	case allJobsPaused:
		w.Actions = []Action{{ID: "resume_jobs", Label: in.T("Fortsetzen"), Tone: "primary",
			Params: map[string]string{"ids": strings.Join(pausedIDs, ",")}}}
	case total > 0:
		w.Actions = []Action{{ID: "pause", Label: in.T("Pausieren")}}
	}
	return w, nil
}

var nzoID = regexp.MustCompile(`^[A-Za-z0-9_]+$`)

func (s sabnzbd) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	switch action {
	case "pause", "resume":
		return s.api(ctx, in, action, nil, nil)
	case "resume_jobs":
		ids := strings.Split(p["ids"], ",")
		for _, id := range ids {
			if !nzoID.MatchString(id) {
				return in.Errorf("ungültige Anfrage-ID")
			}
		}
		return s.api(ctx, in, "queue", url.Values{"name": {"resume"}, "value": {strings.Join(ids, ",")}}, nil)
	}
	return in.Errorf("unbekannte Aktion %q", action)
}
