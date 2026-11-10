package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/i18n"
	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/logging"
)

// More for workflows: a trigger on the entries of a tile's list (a new
// request, a download that left the queue), calling an address as a step,
// and the AI's review of the week.

// itemHolds: something new appeared in the tile's list – or left it.
func (s *Server) itemHolds(user int64, key, wfKey string, tr ruleTrigger, lang string) (bool, string, bool) {
	t, err := s.store.Tile(tr.Tile)
	if err != nil || !s.ruleTileOK(user, t.ID) {
		return false, "", false
	}
	w, pending, err := s.widget(t, lang, 8*time.Second)
	if pending || err != nil || w == nil {
		return false, "", false
	}
	w = hideParts(w, t.Hide, lang)
	match := strings.ToLower(strings.TrimSpace(tr.Match))
	now := map[string]bool{}
	for _, it := range w.Items {
		title := strings.TrimSpace(it.Title)
		if title == "" || match != "" && !strings.Contains(strings.ToLower(title+" "+it.Subtitle), match) {
			continue
		}
		now[title] = true
	}
	s.rules.mu.Lock()
	st := s.ruleState(key)
	prev, first := st.items, !st.itemsSeen
	// A list that is suddenly empty may be a hiccup of the service: it counts
	// only when it is still empty in the next round (the last download
	// finishing empties the queue, too).
	if len(now) == 0 && len(prev) > 0 && !st.emptyOnce {
		st.emptyOnce = true
		s.rules.mu.Unlock()
		return false, "", true
	}
	st.emptyOnce = false
	st.items, st.itemsSeen = now, true
	s.rules.mu.Unlock()
	if first {
		return false, "", true // what was there before the workflow ran does not count
	}
	var hits []string
	if tr.Gone {
		for k := range prev {
			if !now[k] {
				hits = append(hits, k)
			}
		}
	} else {
		for k := range now {
			if !prev[k] {
				hits = append(hits, k)
			}
		}
	}
	if len(hits) == 0 {
		return false, "", true
	}
	sort.Strings(hits)
	entry := strings.Join(hits, ", ")
	s.rules.mu.Lock()
	s.ruleState(wfKey).entry = entry
	s.rules.mu.Unlock()
	if tr.Gone {
		return true, i18n.T(lang, "Nicht mehr bei %s: %s", t.Name, clip(entry, 200)), true
	}
	return true, i18n.T(lang, "Neu bei %s: %s", t.Name, clip(entry, 200)), true
}

// hostOf is the server of an address, for the log (an address may carry a key).
func hostOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "?"
	}
	return u.Host
}

// callURL calls an address as a workflow step – with the person's reach: an
// external account only reaches the public internet.
func (s *Server) callURL(ctx context.Context, user int64, method, address, body string) (int, error) {
	u, err := s.store.User(user)
	if err != nil {
		return 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var rd io.Reader
	if body != "" && method != http.MethodGet {
		rd = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, address, rd)
	if err != nil {
		return 0, errors.New(i18n.T(s.userLang(user), "ungültige Adresse"))
	}
	if rd != nil {
		ct := "text/plain; charset=utf-8"
		if b := strings.TrimSpace(body); strings.HasPrefix(b, "{") || strings.HasPrefix(b, "[") {
			ct = "application/json"
		}
		req.Header.Set("Content-Type", ct)
	}
	req.Header.Set("User-Agent", "Heimdeck")
	resp, err := integrations.NewClientFor(false, u.External).Do(req)
	if err != nil {
		if integrations.HomeNetworkBlocked(err) {
			return 0, errors.New(i18n.T(s.userLang(user), "Dieses Konto darf nur Adressen im Internet aufrufen"))
		}
		return 0, errors.New(logging.Redact(logging.Public(err.Error())))
	}
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))
	resp.Body.Close()
	if resp.StatusCode >= 400 {
		return resp.StatusCode, fmt.Errorf("Status %d", resp.StatusCode)
	}
	return resp.StatusCode, nil
}

// weekReview asks the AI to look back on the week: how the services did and
// what the workflows did.
func (s *Server) weekReview(ctx context.Context, user int64, lang string) (string, error) {
	u, err := s.store.User(user)
	if err != nil || u.Disabled || s.aiFor(u) == nil {
		return "", errors.New(i18n.T(lang, "Die KI ist für dein Konto nicht eingerichtet"))
	}
	c := s.aiConfig()
	if !c.Dashboard {
		return "", errors.New(i18n.T(lang, "Für den Lagebericht muss ein Admin der KI die Daten des Dashboards erlauben (Einstellungen → KI)."))
	}
	since := time.Now().Add(-7 * 24 * time.Hour)
	tiles, _ := s.aiTiles(s.botRequest(ctx, user, lang))
	buckets, _ := s.store.CheckBuckets(since, 24*time.Hour)
	type day struct{ up, total int }
	byTile := map[int64]map[int64]*day{}
	for _, b := range buckets {
		if b.Kind != "int" {
			continue
		}
		if byTile[b.TileID] == nil {
			byTile[b.TileID] = map[int64]*day{}
		}
		byTile[b.TileID][b.Start] = &day{b.Up, b.Total}
	}
	var avail strings.Builder
	for _, t := range tiles {
		days := byTile[t.ID]
		if len(days) == 0 {
			continue
		}
		up, total := 0, 0
		var bad []string
		starts := make([]int64, 0, len(days))
		for st := range days {
			starts = append(starts, st)
		}
		sort.Slice(starts, func(i, j int) bool { return starts[i] < starts[j] })
		for _, st := range starts {
			d := days[st]
			up, total = up+d.up, total+d.total
			if d.up < d.total {
				bad = append(bad, fmt.Sprintf("%s %.1f %%", time.Unix(st, 0).Format("Mon 02.01."), 100*float64(d.up)/float64(d.total)))
			}
		}
		fmt.Fprintf(&avail, "- %s: %.2f %% reachable", t.Name, 100*float64(up)/float64(max(total, 1)))
		if len(bad) > 0 {
			avail.WriteString("; not always: " + strings.Join(bad, ", "))
		}
		avail.WriteString("\n")
	}
	var runs strings.Builder
	for _, e := range s.ruleLog(user) {
		if e.At < since.Unix() {
			continue
		}
		state := "ok"
		if !e.OK {
			state = "with errors"
		}
		fmt.Fprintf(&runs, "- %s %s (%s): %s\n", time.Unix(e.At, 0).Format("Mon 15:04"), e.Name, state, clip(e.Result, 200))
	}
	if avail.Len() == 0 && runs.Len() == 0 {
		return i18n.T(lang, "Für die letzten sieben Tage liegen noch keine Daten vor."), nil
	}
	system := "You are the assistant inside Heimdeck, a self-hosted home dashboard. Answer in " + aiLanguage(lang) + ". " +
		"Today is " + time.Now().Format("Monday, 2 January 2006") + "." + memoryPrompt(s.memoryOf(user)) +
		"\n\nAvailability of the services over the last 7 days (checked every minute):\n" + avail.String() +
		"\nWorkflow runs in that time:\n" + firstNonEmptyStr(runs.String(), "none\n")
	prompt := "Write a short review of the week from the data above: first anything that needs attention (outages, repeated failures, workflows with errors), then one or two lines on what went well. " +
		"At most about 10 short lines, an emoji at the start of each part, no tables, nothing that is not in the data."
	text, _, err := aiComplete(ctx, c, []any{map[string]string{"role": "system", "content": system}, map[string]string{"role": "user", "content": prompt}}, false, false, nil, lang, false)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(firstNonEmptyStr(text, i18n.T(lang, "Die KI hat keine Antwort gegeben."))), nil
}
