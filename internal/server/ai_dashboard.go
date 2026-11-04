package server

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/heimdeck/heimdeck/internal/i18n"
	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/logging"
	"github.com/heimdeck/heimdeck/internal/store"
)

// The dashboard as context for the AI chat (📋): what the user's tiles show
// right now – values, lists, warnings and whether a service is reachable –
// as a compact text. It takes the same way as the tiles themselves: only
// tiles the user may see (no locked or hidden groups), with the values they
// chose to hide left out, and never a tile's settings or keys.

const (
	dashMaxTiles   = 80
	dashTileChars  = 700
	dashTotalChars = 16000
	dashItems      = 5
	dashWait       = 4 * time.Second
)

// aiGroup is a group as the AI gets to know it.
type aiGroup struct {
	name, board string
	pos         int
}

// aiTiles are the tiles the AI may look at for this user: the ones they can
// see, without locked or hidden groups, in dashboard order.
func (s *Server) aiTiles(r *http.Request) ([]store.Tile, map[int64]aiGroup) {
	tiles, err := s.accessibleTiles(r)
	if err != nil {
		return nil, nil
	}
	groups := map[int64]aiGroup{}
	var shown []store.Tile
	for _, t := range tiles {
		g, ok := groups[t.GroupID]
		if !ok {
			sg, err := s.store.Group(t.GroupID)
			if err != nil || sg.Visibility == store.Hidden {
				groups[t.GroupID] = aiGroup{}
				continue
			}
			g = aiGroup{name: sg.Name, pos: sg.Position}
			if b, err := s.store.Board(sg.BoardID); err == nil {
				g.board = b.Name
			}
			groups[t.GroupID] = g
		}
		if g.name == "" {
			continue
		}
		shown = append(shown, t)
	}
	sort.SliceStable(shown, func(i, j int) bool {
		a, b := groups[shown[i].GroupID], groups[shown[j].GroupID]
		if a.board != b.board {
			return a.board < b.board
		}
		if a.pos != b.pos {
			return a.pos < b.pos
		}
		if shown[i].GroupID != shown[j].GroupID {
			return shown[i].GroupID < shown[j].GroupID // keep each group together
		}
		return shown[i].Position < shown[j].Position
	})
	return shown, groups
}

// dashboardContext returns the text and how many tiles it covers.
func (s *Server) dashboardContext(r *http.Request) (string, int) {
	shown, groups := s.aiTiles(r)
	if len(shown) == 0 {
		return "", 0
	}
	if len(shown) > dashMaxTiles {
		shown = shown[:dashMaxTiles]
	}

	// The live data of all tiles at once; a slow service is left out.
	lang := langOf(r)
	lines := make([]string, len(shown))
	var wg sync.WaitGroup
	for i, t := range shown {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lines[i] = s.tileSummary(t, lang)
		}()
	}
	wg.Wait()

	var b strings.Builder
	lastGroup := int64(-1)
	n := 0
	for i, t := range shown {
		if b.Len()+len(lines[i]) > dashTotalChars {
			b.WriteString("(more tiles left out)\n")
			break
		}
		if t.GroupID != lastGroup {
			g := groups[t.GroupID]
			fmt.Fprintf(&b, "\n## %s · %s\n", g.board, g.name)
			lastGroup = t.GroupID
		}
		b.WriteString(lines[i])
		n++
	}
	return strings.TrimSpace(b.String()), n
}

// tileSummary is one tile as a few lines of text.
func (s *Server) tileSummary(t store.Tile, lang string) string {
	return s.tileDetail(t, lang, dashItems, dashTileChars)
}

// tileDetail is one tile as text, with up to items list entries and chars characters.
func (s *Server) tileDetail(t store.Tile, lang string, items, chars int) string {
	var b strings.Builder
	b.WriteString("- " + clip(t.Name, 60))
	if integ, ok := integrations.Get(t.Integration); ok && t.Integration != "" && t.Integration != "link" {
		b.WriteString(" (" + i18n.T(lang, integ.Def().Name) + ")")
	}
	s.mu.Lock()
	st, checked := s.latest[t.ID]
	s.mu.Unlock()
	if checked {
		if st.Online {
			b.WriteString(" – online")
		} else {
			b.WriteString(" – OFFLINE (not reachable)")
		}
	}
	b.WriteString("\n")
	integ, ok := integrations.Get(t.Integration)
	if !ok || !t.ShowWidget || !integ.Def().HasWidget || !configured(integ, t) {
		return b.String()
	}
	w, pending, err := s.widget(t, lang, dashWait)
	switch {
	case pending:
		b.WriteString("  (live data still loading)\n")
	case err != nil:
		b.WriteString("  error: " + clip(i18n.T(lang, logging.Public(err.Error())), 160) + "\n")
	case w != nil:
		w = hideParts(w, t.Hide, lang)
		var parts []string
		for _, st := range w.Stats {
			v := st.Label + " " + st.Value
			if st.Tone == "bad" || st.Tone == "warn" {
				v += " (!)"
			}
			parts = append(parts, v)
		}
		if len(parts) > 0 {
			b.WriteString("  " + strings.Join(parts, "; ") + "\n")
		}
		for _, a := range w.Alerts {
			b.WriteString("  warning: " + clip(a, 200) + "\n")
		}
		if len(w.Items) > 0 {
			title := w.ItemsTitle
			if title == "" {
				title = "list"
			}
			b.WriteString("  " + title + ":\n")
			for i, it := range w.Items {
				if i == items {
					fmt.Fprintf(&b, "    … %d more\n", len(w.Items)-items)
					break
				}
				line := "    · " + clip(it.Title, 100)
				if it.Subtitle != "" {
					line += " — " + clip(it.Subtitle, 120)
				}
				if it.Badge != "" {
					line += " [" + clip(it.Badge, 40) + "]"
				}
				if it.Progress != nil {
					line += fmt.Sprintf(" (%.0f %%)", *it.Progress)
				}
				b.WriteString(line + "\n")
			}
		}
		if w.Camera != nil {
			for i, e := range w.Camera.Events {
				if i == 3 {
					break
				}
				b.WriteString("  camera event: " + e.Kind + " at " + e.At + "\n")
			}
		}
	}
	out := b.String()
	if r := []rune(out); len(r) > chars {
		out = string(r[:chars]) + "…\n" // keeps the lines, unlike clip
	}
	return out
}
