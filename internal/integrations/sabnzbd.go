package integrations

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type sabnzbd struct{}

func init() { register(sabnzbd{}) }

func (sabnzbd) Def() Definition {
	return Definition{
		ID: "sabnzbd", Name: "SABnzbd", Icon: "sabnzbd", Category: "Downloads",
		Description: "Download-Geschwindigkeit, Warteschlange, freier Speicher, heute und diese Woche geladen, fehlgeschlagene Downloads, Pause/Fortsetzen",
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
			Paused     bool   `json:"paused"`
			KBPerSec   string `json:"kbpersec"`
			MBLeft     string `json:"mbleft"`
			Timeleft   string `json:"timeleft"`
			Total      int    `json:"noofslots_total"`
			DiskFree   string `json:"diskspace1"` // GB on the download folder
			SpeedLimit string `json:"speedlimit"` // percent of the maximum, 100 = none
			Warnings   string `json:"have_warnings"`
			Slots      []struct {
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
	if gb, err := strconv.ParseFloat(q.DiskFree, 64); err == nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("Freier Speicher"), Value: in.Bytes(gb * 1024 * 1024 * 1024), Tone: toneIf(gb < 10, "warn")})
	}
	if l, err := strconv.Atoi(strings.TrimSpace(q.SpeedLimit)); err == nil && l > 0 && l < 100 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Tempolimit"), Value: fmt.Sprintf("%d %%", l)})
	}
	if n, _ := strconv.Atoi(q.Warnings); n > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Warnungen"), Value: fmt.Sprint(n), Tone: "warn"})
	}
	recent := s.history(ctx, in, w)
	if len(q.Slots) == 0 && len(recent) > 0 {
		// Nothing to download: what finished last instead of an empty list.
		w.ItemsTitle = in.T("Zuletzt fertig")
		w.Items = recent
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

// history adds what was downloaded today and this week, alerts for failed
// downloads of the last day, and returns the latest finished jobs.
func (s sabnzbd) history(ctx context.Context, in *Instance, w *Widget) []Item {
	var res struct {
		History struct {
			DaySize  string `json:"day_size"` // e.g. "12.3 G"
			WeekSize string `json:"week_size"`
			Slots    []struct {
				Name      string `json:"name"`
				Status    string `json:"status"` // Completed | Failed | …
				Bytes     int64  `json:"bytes"`
				Completed int64  `json:"completed"` // unix seconds
				FailMsg   string `json:"fail_message"`
			} `json:"slots"`
		} `json:"history"`
	}
	if s.api(ctx, in, "history", url.Values{"limit": {"10"}}, &res) != nil {
		return nil
	}
	h := res.History
	// SABnzbd writes sizes like "1.2 G"; shown like every other size here.
	size := func(v string) string {
		num, unit, _ := strings.Cut(strings.TrimSpace(v), " ")
		f, _ := strconv.ParseFloat(num, 64)
		mult := map[string]float64{"K": 1 << 10, "M": 1 << 20, "G": 1 << 30, "T": 1 << 40, "P": 1 << 50}[strings.ToUpper(strings.TrimSuffix(strings.TrimSpace(unit), "B"))]
		if mult == 0 {
			mult = 1
		}
		return in.Bytes(f * mult)
	}
	if h.DaySize != "" {
		w.Stats = append(w.Stats, Stat{Label: in.T("Heute geladen"), Value: size(h.DaySize)})
	}
	if h.WeekSize != "" {
		w.Stats = append(w.Stats, Stat{Label: in.T("Diese Woche"), Value: size(h.WeekSize)})
	}
	var items []Item
	for _, sl := range h.Slots {
		done := time.Unix(sl.Completed, 0)
		failed := strings.EqualFold(sl.Status, "Failed")
		if failed && sl.Completed > 0 && time.Since(done) < 24*time.Hour {
			w.Alerts = append(w.Alerts, in.T("Fehlgeschlagen: %s", strings.ReplaceAll(sl.Name, ".", " ")))
		}
		if len(items) < 5 {
			sub := in.Bytes(float64(sl.Bytes))
			if sl.Completed > 0 {
				sub += " · " + in.Since(done)
			}
			badge := ""
			if failed {
				badge = in.T("Fehlgeschlagen")
			}
			items = append(items, Item{Title: strings.ReplaceAll(sl.Name, ".", " "), Subtitle: sub, Badge: badge, URL: in.Link("/")})
		}
	}
	return items
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
