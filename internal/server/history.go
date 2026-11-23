package server

import (
	"context"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/i18n"
	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/store"
)

// History: values of any tile Heimdeck records itself – every five minutes –
// so a number becomes a chart even when the service keeps no history. The
// values to record are marked in the tile's list of live data parts
// ("~rec:<German label>"); they are stored with the data source values
// ("stat:<label>") and kept 90 days.
const (
	recPrefix      = "~rec:"
	recSeries      = "stat:"
	recordInterval = 5 * time.Minute
)

// historyLoop records the marked values every five minutes.
func (s *Server) historyLoop(ctx context.Context) {
	tick := time.NewTicker(recordInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.recordHistory(ctx)
		}
	}
}

// recordedKeys lists the values a tile records (their German labels).
func recordedKeys(t store.Tile) []string {
	var out []string
	for _, h := range t.Hide {
		if k, ok := strings.CutPrefix(h, recPrefix); ok && k != "" {
			out = append(out, k)
		}
	}
	return out
}

// recordHistory reads the marked values of every tile and stores them.
func (s *Server) recordHistory(ctx context.Context) {
	tiles, err := s.store.Tiles()
	if err != nil {
		return
	}
	now := time.Now().UnixMilli()
	for _, t := range tiles {
		keys := recordedKeys(t)
		if len(keys) == 0 || ctx.Err() != nil {
			continue
		}
		// German labels: the keys are the German source texts.
		w, pending, err := s.widget(t, i18n.DE, 20*time.Second)
		if err != nil || pending || w == nil {
			continue
		}
		var pts []store.Point
		for _, st := range w.Stats {
			if !slices.Contains(keys, st.Label) {
				continue
			}
			if q, ok := parseQuantity(st.Value, i18n.DE); ok {
				pts = append(pts, store.Point{Name: recSeries + st.Label, T: now, Value: q.v})
			}
		}
		if len(pts) > 0 {
			logErr("Verlauf speichern", s.store.AddPoints(t.ID, pts))
		}
	}
	slog.Debug("Verlauf aufgezeichnet")
}

// withHistory adds a chart of the last 24 hours for each recorded value. The
// widget is shared with the cache: a copy gets the charts.
func (s *Server) withHistory(w *integrations.Widget, t store.Tile, lang string) *integrations.Widget {
	keys := recordedKeys(t)
	if w == nil || len(keys) == 0 {
		return w
	}
	pts, err := s.store.Points(t.ID, time.Now().Add(-24*time.Hour))
	if err != nil {
		return w
	}
	out := *w
	out.Charts = slices.Clone(w.Charts)
	for _, k := range keys {
		list := pts[recSeries+k]
		if len(list) < 2 {
			continue
		}
		label := i18n.T(lang, k)
		c := integrations.Chart{ID: "rec:" + k, Title: label + " · " + i18n.T(lang, "24 Std."), Kind: "area",
			Series: []integrations.ChartSeries{{Name: label}}}
		// Bytes and units as the value shows them now.
		for _, st := range w.Stats {
			if st.Label == label || st.Label == k {
				if q, ok := parseQuantity(st.Value, lang); ok && q.bytes {
					c.Bytes = true
				} else if m := numUnitRe.FindStringSubmatch(st.Value); m != nil {
					c.Unit = strings.TrimSpace(m[2])
				}
			}
		}
		for _, p := range list {
			c.Times = append(c.Times, p.T)
			c.Series[0].Values = append(c.Series[0].Values, p.Value)
		}
		out.Charts = append(out.Charts, c)
	}
	return &out
}
