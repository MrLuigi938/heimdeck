package server

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/heimdeck/heimdeck/internal/i18n"
	"github.com/heimdeck/heimdeck/internal/integrations"
)

// Each tile can leave out parts of its live data (Tile.Hide): single values
// by their German label, or whole sections.
const (
	partItems   = "#items"
	partAlerts  = "#alerts"
	partActions = "#actions"
)

// hideParts returns the widget without the parts the tile leaves out. The
// widget itself is shared with the cache and stays unchanged.
func hideParts(w *integrations.Widget, hide []string, lang string) *integrations.Widget {
	if w == nil || len(hide) == 0 {
		return w
	}
	hidden := func(label string) bool {
		for _, k := range hide {
			if k == label || i18n.T(lang, k) == label {
				return true
			}
		}
		return false
	}
	out := *w
	out.Stats = nil
	for _, s := range w.Stats {
		if !hidden(s.Label) {
			out.Stats = append(out.Stats, s)
		}
	}
	if slices.Contains(hide, partItems) {
		out.Items, out.ItemsTitle = nil, ""
	}
	if slices.Contains(hide, partAlerts) {
		out.Alerts = nil
	}
	if slices.Contains(hide, partActions) {
		out.Actions = nil
	}
	return &out
}

type tilePart struct {
	Key     string `json:"key"`
	Label   string `json:"label"`
	Value   string `json:"value,omitempty"`
	Section bool   `json:"section,omitempty"`
}

// handleTileParts asks the service once and lists what the tile can show,
// for the "Live data" choice in the tile editor. It takes the form like
// /tiles/test, so it also works before the tile is saved.
func (s *Server) handleTileParts(w http.ResponseWriter, r *http.Request) {
	var in tileInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	t, err := s.buildTile(in, uid(r))
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if t.ID != 0 {
		if _, code, err := s.ownTile(r, t.ID); err != nil {
			writeErr(w, code, err)
			return
		}
	} else if _, code, err := s.ownGroup(r, t.GroupID); err != nil {
		// a new tile is fetched with its owner's rights (external or not), so it must be the user's group
		writeErr(w, code, err)
		return
	}
	integ, _ := integrations.Get(t.Integration)
	if !integ.Def().HasWidget {
		writeJSON(w, map[string]any{"parts": []tilePart{}})
		return
	}
	if t.URL == "" && t.APIURL == "" && !integ.Def().NoURL {
		writeErr(w, 400, errors.New("Bitte eine Adresse angeben"))
		return
	}
	ctx, cancel := timeout(r, 15*time.Second)
	defer cancel()
	lang := langOf(r)
	widget, err := s.safeFetch(ctx, t, lang)
	if err != nil {
		writeErr(w, 502, errors.New(tr(r, err.Error())))
		return
	}
	parts := []tilePart{}
	if widget == nil {
		writeJSON(w, map[string]any{"parts": parts})
		return
	}
	for _, st := range widget.Stats {
		parts = append(parts, tilePart{Key: i18n.Source(lang, st.Label), Label: st.Label, Value: st.Value})
	}
	if len(widget.Items) > 0 {
		parts = append(parts, tilePart{Key: partItems, Label: firstNonEmpty(widget.ItemsTitle, tr(r, "Liste")), Section: true})
	}
	if len(widget.Alerts) > 0 {
		parts = append(parts, tilePart{Key: partAlerts, Label: tr(r, "Hinweise und Warnungen"), Section: true})
	}
	if len(widget.Actions) > 0 {
		parts = append(parts, tilePart{Key: partActions, Label: tr(r, "Knöpfe"), Section: true})
	}
	writeJSON(w, map[string]any{"parts": parts})
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
