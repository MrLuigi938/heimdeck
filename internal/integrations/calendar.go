package integrations

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// calendar shows upcoming dates: events from iCal calendars (Nextcloud,
// Google, Outlook, school holidays …) and, if wanted, the new episodes and
// films from the user's own Sonarr and Radarr tiles. Calendars are read at
// most every 15 minutes.

type calendar struct{}

func init() { register(calendar{}) }

func (calendar) Def() Definition {
	return Definition{
		ID: "calendar", Name: "Kalender", Icon: "📅", Category: "Allgemein",
		Description: "Anstehende Termine aus iCal-Kalendern (Nextcloud, Google, Outlook …) und neue Folgen und Filme aus deinen Sonarr- und Radarr-Kacheln",
		NoURL:       true, HasWidget: true,
		Setup: []string{
			"iCal-Adresse: In Nextcloud unter Kalender → … → „Link kopieren“ bzw. „Abonnement-Link“, in Google Kalender unter Einstellungen → Kalender → „Privatadresse im iCal-Format“, in Outlook unter Kalender freigeben → ICS-Link.",
			"Neuerscheinungen: Heimdeck nimmt dafür deine Sonarr- und Radarr-Kacheln mit ihren Zugangsdaten – du musst nichts doppelt eintragen.",
		},
		Fields: []Field{
			{Key: "ical", Label: "iCal-Adressen", Type: "textarea", Secret: true,
				Help: "Eine Adresse pro Zeile (https://… oder webcal://…), optional mit Namen davor: „Familie = https://…“. Private Kalender-Links sind geheim – sie werden verschlüsselt gespeichert."},
			{Key: "releases", Label: "Neue Folgen und Filme aus Sonarr und Radarr zeigen", Type: "checkbox"},
			{Key: "days", Label: "Zeitraum", Type: "select", Options: []string{"7", "14", "30"}, Display: true, Help: "Wie viele Tage im Voraus (Tage)"},
			{Key: "count", Label: "Einträge", Type: "select", Options: []string{"5", "8", "12", "20"}, Display: true},
		},
	}
}

// CalEvent is one date on the calendar tile.
type CalEvent struct {
	Title, Detail, Source, URL string
	At                         time.Time
	AllDay                     bool
}

// Releases is offered by integrations whose upcoming releases can appear
// on the calendar tile (Sonarr, Radarr).
type Releases interface {
	Releases(ctx context.Context, in *Instance, from, to time.Time) ([]CalEvent, error)
}

func (c calendar) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	days, _ := strconv.Atoi(in.V("days"))
	if days <= 0 {
		days = 7
	}
	now := time.Now()
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	to := from.AddDate(0, 0, days+1)
	var events []CalEvent
	var problems []string
	for _, line := range strings.Split(in.V("ical"), "\n") {
		name, u := calSource(line)
		if u == "" {
			continue
		}
		evs, err := remember(in, "ical\x00"+u, 15*time.Minute, func() ([]icalEvent, error) { return readICal(ctx, in, u) })
		if err != nil {
			problems = append(problems, in.T("Kalender „%s“: %v", firstNonEmpty(name, hostOf(u)), err))
			continue
		}
		for _, e := range evs {
			for _, at := range e.occurrences(from, to) {
				events = append(events, CalEvent{Title: e.Summary, Detail: e.Location, Source: name, At: at, AllDay: e.AllDay})
			}
		}
	}
	if in.V("releases") == "true" && in.Others != nil {
		for _, kind := range []string{"sonarr", "radarr"} {
			integ, ok := Get(kind)
			rel, ok2 := integ.(Releases)
			if !ok || !ok2 {
				continue
			}
			for _, other := range in.Others(kind) {
				evs, err := rel.Releases(ctx, other, from, to)
				if err != nil {
					problems = append(problems, in.T("%s: %v", integ.Def().Name, err))
					continue
				}
				events = append(events, evs...)
			}
		}
	}
	if len(events) == 0 && len(problems) > 0 {
		return nil, fmt.Errorf("%s", strings.Join(problems, " · "))
	}
	// Ongoing all-day events (holidays) stay until their day is over.
	kept := events[:0]
	for _, e := range events {
		if e.AllDay || !e.At.Before(now.Add(-time.Hour)) {
			kept = append(kept, e)
		}
	}
	events = kept
	sort.SliceStable(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })

	count, _ := strconv.Atoi(in.V("count"))
	if count <= 0 {
		count = 5
	}
	today, week := 0, 0
	for _, e := range events {
		d := dayDiff(now, e.At)
		if d == 0 {
			today++
		}
		if d < 7 {
			week++
		}
	}
	w := &Widget{Stats: []Stat{{Label: in.T("Heute"), Value: in.Num(today)}, {Label: in.T("Nächste 7 Tage"), Value: in.Num(week)}}}
	for i, e := range events {
		if i >= count {
			break
		}
		when := weekday(in, e.At)
		if !e.AllDay {
			when += " · " + e.At.In(time.Local).Format("15:04")
		}
		sub := when
		for _, s := range []string{e.Detail, e.Source} {
			if s != "" {
				sub += " · " + s
			}
		}
		badge := ""
		switch dayDiff(now, e.At) {
		case 0:
			badge = in.T("Heute")
		case 1:
			badge = in.T("Morgen")
		}
		w.Items = append(w.Items, Item{Title: e.Title, Subtitle: sub, Badge: badge, URL: e.URL})
	}
	if len(w.Items) > 0 {
		w.ItemsTitle = in.T("Als Nächstes")
	} else {
		w.ItemsTitle = in.T("Keine Termine in den nächsten %d Tagen", days)
	}
	w.Alerts = problems
	return w, nil
}

// dayDiff counts calendar days from now to t (0 = today).
func dayDiff(now, t time.Time) int {
	a := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	t = t.In(time.Local)
	b := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local)
	return int(b.Sub(a).Hours() / 24)
}

// calSource reads "Name = https://…" or just the address; webcal:// is https.
func calSource(line string) (name, u string) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", ""
	}
	if n, rest, ok := strings.Cut(line, "="); ok && !strings.Contains(n, "://") {
		name, line = strings.TrimSpace(n), strings.TrimSpace(rest)
	}
	if strings.HasPrefix(strings.ToLower(line), "webcal://") {
		line = "https://" + line[len("webcal://"):]
	}
	return name, line
}

func hostOf(u string) string {
	if p, err := url.Parse(u); err == nil && p.Host != "" {
		return p.Host
	}
	return u
}

// ---- iCal ----

type icalEvent struct {
	Summary, Location string
	Start             time.Time
	AllDay            bool
	Rule              map[string]string // RRULE parts
	Exdates           map[string]bool   // excluded starts (date or date-time, as in the file)
}

func readICal(ctx context.Context, in *Instance, u string) ([]icalEvent, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
		return nil, in.Errorf("ungültige Adresse")
	}
	req.Header.Set("User-Agent", "Heimdeck (Kalender)")
	resp, err := in.Client.Do(req)
	if err != nil {
		return nil, friendlyNetErr(in.Lang, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, &HTTPError{Status: resp.StatusCode, Lang: in.Lang}
	}
	events, ok := parseICal(io.LimitReader(resp.Body, 10<<20))
	if !ok {
		return nil, in.Errorf("kein iCal-Kalender")
	}
	return events, nil
}

// parseICal reads the VEVENTs of a calendar file.
func parseICal(r io.Reader) ([]icalEvent, bool) {
	// Unfold continuation lines first (RFC 5545 3.1).
	var lines []string
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		l := strings.TrimRight(sc.Text(), "\r")
		if (strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t")) && len(lines) > 0 {
			lines[len(lines)-1] += l[1:]
			continue
		}
		lines = append(lines, l)
	}
	calendar := false
	var out []icalEvent
	var cur *icalEvent
	depth := 0 // nested components inside an event (VALARM)
	for _, l := range lines {
		name, params, value := icalLine(l)
		switch {
		case name == "BEGIN" && value == "VCALENDAR":
			calendar = true
		case name == "BEGIN" && value == "VEVENT":
			cur = &icalEvent{Exdates: map[string]bool{}}
		case name == "BEGIN" && cur != nil:
			depth++
		case name == "END" && value == "VEVENT" && cur != nil:
			if !cur.Start.IsZero() && cur.Summary != "" {
				out = append(out, *cur)
			}
			cur, depth = nil, 0
		case name == "END" && cur != nil && depth > 0:
			depth--
		case cur == nil || depth > 0:
		case name == "SUMMARY":
			cur.Summary = icalText(value)
		case name == "LOCATION":
			cur.Location = icalText(value)
		case name == "DTSTART":
			cur.Start, cur.AllDay = icalTime(value, params)
		case name == "RRULE":
			cur.Rule = map[string]string{}
			for _, part := range strings.Split(value, ";") {
				if k, v, ok := strings.Cut(part, "="); ok {
					cur.Rule[strings.ToUpper(k)] = v
				}
			}
		case name == "EXDATE":
			for _, v := range strings.Split(value, ",") {
				t, _ := icalTime(v, params)
				cur.Exdates[t.Format("20060102T1504")] = true
			}
		}
	}
	return out, calendar
}

// icalLine splits "DTSTART;TZID=Europe/Berlin:20261027T180000".
func icalLine(l string) (name string, params map[string]string, value string) {
	head, value, _ := strings.Cut(l, ":")
	parts := strings.Split(head, ";")
	params = map[string]string{}
	for _, p := range parts[1:] {
		if k, v, ok := strings.Cut(p, "="); ok {
			params[strings.ToUpper(k)] = strings.Trim(v, `"`)
		}
	}
	return strings.ToUpper(parts[0]), params, strings.TrimSpace(value)
}

func icalText(s string) string {
	return strings.NewReplacer(`\n`, " ", `\N`, " ", `\,`, ",", `\;`, ";", `\\`, `\`).Replace(s)
}

// icalTime reads a DATE or DATE-TIME value; times without a zone are local.
func icalTime(v string, params map[string]string) (time.Time, bool) {
	if params["VALUE"] == "DATE" || len(v) == 8 {
		t, err := time.ParseInLocation("20060102", v, time.Local)
		if err != nil {
			return time.Time{}, true
		}
		return t, true
	}
	if strings.HasSuffix(v, "Z") {
		t, _ := time.Parse("20060102T150405Z", v)
		return t, false
	}
	loc := time.Local
	if tz := params["TZID"]; tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	t, _ := time.ParseInLocation("20060102T150405", v, loc)
	return t, false
}

var icalDays = map[string]time.Weekday{"SU": time.Sunday, "MO": time.Monday, "TU": time.Tuesday, "WE": time.Wednesday, "TH": time.Thursday, "FR": time.Friday, "SA": time.Saturday}

// occurrences returns the starts of the event between from and to. Repeating
// events support the usual rules: daily, weekly (with BYDAY), monthly and
// yearly, with INTERVAL, COUNT and UNTIL – enough for birthdays, bin
// collection days and weekly appointments.
func (e icalEvent) occurrences(from, to time.Time) []time.Time {
	visible := func(t time.Time) bool {
		end := t
		if e.AllDay {
			end = t.AddDate(0, 0, 1)
		}
		return end.After(from) && t.Before(to) && !e.Exdates[t.Format("20060102T1504")]
	}
	if e.Rule == nil {
		if visible(e.Start) {
			return []time.Time{e.Start}
		}
		return nil
	}
	interval, _ := strconv.Atoi(e.Rule["INTERVAL"])
	if interval < 1 {
		interval = 1
	}
	count, _ := strconv.Atoi(e.Rule["COUNT"])
	var until time.Time
	if u := e.Rule["UNTIL"]; u != "" {
		until, _ = icalTime(u, nil)
		if len(u) == 8 {
			until = until.AddDate(0, 0, 1)
		}
	}
	var byDay []time.Weekday
	for _, d := range strings.Split(e.Rule["BYDAY"], ",") {
		if wd, ok := icalDays[strings.TrimLeft(d, "+-0123456789")]; ok && len(d) == 2 {
			byDay = append(byDay, wd)
		}
	}
	step := func(t time.Time, n int) time.Time {
		switch e.Rule["FREQ"] {
		case "DAILY":
			return t.AddDate(0, 0, n)
		case "WEEKLY":
			return t.AddDate(0, 0, 7*n)
		case "MONTHLY":
			return t.AddDate(0, n, 0)
		case "YEARLY":
			return t.AddDate(n, 0, 0)
		}
		return time.Time{}
	}
	var out []time.Time
	seen := 0
	for i := 0; i < 5000; i++ {
		base := step(e.Start, i*interval)
		if base.IsZero() || !until.IsZero() && base.After(until) || base.After(to) {
			break
		}
		starts := []time.Time{base}
		if e.Rule["FREQ"] == "WEEKLY" && len(byDay) > 0 {
			starts = starts[:0]
			monday := base.AddDate(0, 0, -((int(base.Weekday()) + 6) % 7))
			for _, wd := range byDay {
				t := monday.AddDate(0, 0, (int(wd)+6)%7)
				if !t.Before(e.Start) {
					starts = append(starts, t)
				}
			}
			sort.Slice(starts, func(a, b int) bool { return starts[a].Before(starts[b]) })
		}
		for _, t := range starts {
			if !until.IsZero() && t.After(until) {
				break
			}
			seen++
			if count > 0 && seen > count {
				return out
			}
			if visible(t) {
				out = append(out, t)
			}
		}
	}
	return out
}
