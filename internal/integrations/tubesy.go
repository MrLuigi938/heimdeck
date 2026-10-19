package integrations

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Tubesy (github.com/mrluigi938/tubesy) is a multi-user yt-dlp web UI.
// It is queried with an API key from Tubesy's account page.

type tubesy struct{}

func init() { register(tubesy{}) }

// tubesyIcon is Tubesy's logo; it is not part of the dashboard-icons collection.
const tubesyIcon = "data:image/svg+xml;base64,PHN2ZyB4bWxucz0iaHR0cDovL3d3dy53My5vcmcvMjAwMC9zdmciIHZpZXdCb3g9IjAgMCA2NCA2NCI+PHJlY3Qgd2lkdGg9IjY0IiBoZWlnaHQ9IjY0IiByeD0iMTQiIGZpbGw9IiNlNTQ4NGQiLz48cGF0aCBkPSJNMjYgMjB2MjRsMTgtMTJ6IiBmaWxsPSIjZmZmIi8+PHBhdGggZD0iTTE4IDUwaDI4IiBzdHJva2U9IiNmZmYiIHN0cm9rZS13aWR0aD0iNCIgc3Ryb2tlLWxpbmVjYXA9InJvdW5kIi8+PC9zdmc+Cg=="

func (tubesy) Def() Definition {
	return Definition{
		ID: "tubesy", Name: "Tubesy", Icon: tubesyIcon, Category: "Downloads",
		Description: "Video-Downloads (yt-dlp): Warteschlange, Tempo, Bibliothekssuche, URLs aus der Suche herunterladen",
		Fields: []Field{{Key: "apiKey", Label: "API-Schlüssel", Type: "password", Required: true, Secret: true,
			Help: "In Tubesy: Konto → API-Schlüssel → Neuer Schlüssel. „Nur lesen“ genügt für die Anzeige, für Aktionen „Lesen & Downloads“ wählen."}},
		HasWidget: true, HasSearch: true, Images: []string{"tubesy"}, Port: 8080,
	}
}

func (tubesy) call(ctx context.Context, in *Instance, method, path string, q url.Values, body, out any) error {
	err := in.do(ctx, request{method: method, path: path, query: q, body: body,
		headers: map[string]string{"X-Api-Key": in.V("apiKey")}}, out)
	var he *HTTPError
	if errors.As(err, &he) {
		switch {
		case he.Status == 401:
			return in.Errorf("Tubesy lehnt den API-Schlüssel ab – in Tubesy unter Konto → API-Schlüssel einen neuen erstellen")
		case he.Status == 403 && method != "GET":
			return in.Errorf("Der API-Schlüssel darf nur lesen – für Aktionen einen Schlüssel mit „Lesen & Downloads“ erstellen")
		case he.Status == 422 || he.Status == 404 || he.Status == 409:
			if msg := tubesyDetail(he.Body); msg != "" {
				return errors.New("Tubesy: " + msg)
			}
		}
	}
	return err
}

// tubesyDetail pulls FastAPI's {"detail": "..."} message out of an error body,
// which HTTPError has already cut to a few hundred bytes (so it may not parse as JSON).
func tubesyDetail(body string) string {
	_, rest, ok := strings.Cut(body, `"detail":"`)
	if !ok {
		return ""
	}
	msg, _, _ := strings.Cut(rest, `"}`)
	msg = strings.ReplaceAll(msg, `\"`, `"`)
	if r := []rune(msg); len(r) > 160 {
		msg = string(r[:160]) + "…"
	}
	return msg
}

type tubesyDownload struct {
	ID         int      `json:"id"`
	Username   string   `json:"username"`
	Title      string   `json:"title"`
	Uploader   string   `json:"uploader"`
	Thumbnail  string   `json:"thumbnail"`
	Status     string   `json:"status"`
	Progress   float64  `json:"progress"`
	Speed      *float64 `json:"speed"`
	ETA        *float64 `json:"eta"`
	Stage      string   `json:"stage"`
	Playlist   string   `json:"playlist_title"`
	FileSize   *float64 `json:"file_size"`
	FinishedAt string   `json:"finished_at"`
}

type tubesyCounts struct {
	Queued      int `json:"queued"`
	Active      int `json:"active"`
	Completed   int `json:"completed"`
	Failed      int `json:"failed"`
	Completed24 int `json:"completed_24h"`
}

type tubesyStats struct {
	User struct {
		Role string `json:"role"`
	} `json:"user"`
	Downloads tubesyCounts `json:"downloads"`
	Speed     float64      `json:"speed_bytes"`
	Instance  *struct {
		Downloads tubesyCounts `json:"downloads"`
		DiskFree  float64      `json:"disk_free_bytes"`
	} `json:"instance"`
}

func (t tubesy) Test(ctx context.Context, in *Instance) error {
	var st tubesyStats
	return t.call(ctx, in, "GET", "/api/v1/stats", nil, nil, &st)
}

func (t tubesy) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var st tubesyStats
	if err := t.call(ctx, in, "GET", "/api/v1/stats", nil, nil, &st); err != nil {
		return nil, err
	}
	var queue []tubesyDownload
	if err := t.call(ctx, in, "GET", "/api/v1/queue", url.Values{"limit": {"5"}}, nil, &queue); err != nil {
		return nil, err
	}

	// Admin keys see the whole instance, everyone else only their own downloads.
	c := st.Downloads
	if st.Instance != nil {
		c = st.Instance.Downloads
	}
	w := &Widget{ItemsTitle: in.T("Warteschlange")}
	if c.Active > 0 && st.Speed > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Tempo"), Value: in.Bytes(st.Speed) + "/s"})
	}
	w.Stats = append(w.Stats,
		Stat{Label: in.T("Aktiv"), Value: in.Num(c.Active)},
		Stat{Label: in.T("Wartet"), Value: in.Num(c.Queued)},
		Stat{Label: in.T("Heute fertig"), Value: in.Num(c.Completed24)},
		Stat{Label: in.T("Fehler"), Value: in.Num(c.Failed), Tone: toneIf(c.Failed > 0, "bad")},
	)
	if st.Instance != nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("Frei"), Value: in.Bytes(st.Instance.DiskFree), Tone: toneIf(st.Instance.DiskFree < 10*1024*1024*1024, "warn")})
	}

	for _, d := range queue {
		parts := []string{}
		if st.Instance != nil && d.Username != "" {
			parts = append(parts, "@"+d.Username)
		}
		switch d.Status {
		case "downloading":
			parts = append(parts, in.Status("downloading"))
			if d.Speed != nil && *d.Speed > 0 {
				parts = append(parts, in.Bytes(*d.Speed)+"/s")
			}
			if d.ETA != nil && *d.ETA > 0 {
				parts = append(parts, in.T("noch %s", shortDuration(int(*d.ETA))))
			}
		case "processing":
			stage := d.Stage
			if stage == "" {
				stage = in.T("Verarbeitung")
			}
			parts = append(parts, stage)
		default:
			parts = append(parts, in.Status(d.Status))
		}
		it := Item{Title: d.Title, Subtitle: strings.Join(parts, " · "), Image: d.Thumbnail, URL: in.Link("/")}
		if d.Status == "downloading" {
			it.Progress = pct(d.Progress)
		}
		it.Actions = []Action{{ID: "cancel", Label: in.T("Abbrechen"), Tone: "danger",
			Params: map[string]string{"id": strconv.Itoa(d.ID)}, Confirm: in.T("Download „%s“ abbrechen?", d.Title)}}
		w.Items = append(w.Items, it)
	}
	return w, nil
}

func shortDuration(sec int) string {
	switch {
	case sec < 60:
		return fmt.Sprintf("%d s", sec)
	case sec < 3600:
		return fmt.Sprintf("%d min", (sec+30)/60)
	case sec < 2*86400:
		return fmt.Sprintf("%d:%02d h", sec/3600, sec%3600/60)
	}
	return fmt.Sprintf("%d d %d h", sec/86400, sec%86400/3600)
}

func (t tubesy) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	query = strings.TrimSpace(query)
	// A pasted link is offered for download instead of being searched for.
	if u, err := url.Parse(query); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" {
		return []Item{{
			Title: query, Subtitle: in.T("Mit dem Standard-Preset herunterladen"), URL: in.Link("/"),
			Actions: []Action{{ID: "download", Label: in.T("Herunterladen"), Tone: "primary", Params: map[string]string{"url": query}}},
		}}, nil
	}
	var res struct {
		Items []tubesyDownload `json:"items"`
	}
	q := url.Values{"view": {"library"}, "q": {query}, "limit": {"8"}}
	if err := t.call(ctx, in, "GET", "/api/downloads", q, nil, &res); err != nil {
		return nil, err
	}
	out := []Item{}
	for _, d := range res.Items {
		sub := d.Uploader
		if d.Playlist != "" {
			sub = strings.TrimPrefix(sub+" · "+d.Playlist, " · ")
		}
		if d.FileSize != nil {
			sub = strings.TrimPrefix(sub+" · "+in.Bytes(*d.FileSize), " · ")
		}
		out = append(out, Item{Title: d.Title, Subtitle: sub, Image: d.Thumbnail,
			URL: in.Link(fmt.Sprintf("/api/downloads/%d/file?inline=1", d.ID))})
	}
	return out, nil
}

func (t tubesy) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	switch action {
	case "cancel":
		if _, err := strconv.Atoi(p["id"]); err != nil {
			return in.Errorf("ungültige Download-ID")
		}
		return t.call(ctx, in, "POST", "/api/downloads/"+p["id"]+"/cancel", nil, map[string]any{}, nil)
	case "download":
		u, err := url.Parse(p["url"])
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			return in.Errorf("ungültige URL")
		}
		return t.call(ctx, in, "POST", "/api/v1/download", nil, map[string]string{"url": p["url"]}, nil)
	}
	return in.Errorf("unbekannte Aktion %q", action)
}
