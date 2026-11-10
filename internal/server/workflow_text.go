package server

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/i18n"
	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/store"
)

// Building blocks in a workflow's message: a tile's current value, its list
// or whether it answers, filled in when the message goes out –
//
//	{Jellyfin · Liste}  what is playing (the tile's list)
//	{Proxmox · CPU-Temperatur}  a value, by its label on the tile
//	{NAS · Status}  reachable or not
//	{Grund} {Uhrzeit} {Datum}  why the workflow started, the time, the date
//
// They read the tiles as the workflow sees them: with the rights of the
// person who made it, without hidden values, locked or hidden groups. The
// English words (List, reason, time, date) work as well.

var placeholderRe = regexp.MustCompile(`\{([^{}\n]{1,120})\}`)

const maxListLines = 10

func placeholderWord(part string, words ...string) bool {
	p := strings.ToLower(strings.TrimSpace(part))
	for _, w := range words {
		if p == w {
			return true
		}
	}
	return false
}

// splitPlaceholder splits "Jellyfin · Liste" into the tile and the part.
func splitPlaceholder(p string) (tile, part string, ok bool) {
	i, n := strings.LastIndex(p, "·"), len("·")
	if i < 0 {
		i, n = strings.LastIndex(p, ":"), 1
	}
	if i < 0 {
		return "", "", false
	}
	tile, part = strings.TrimSpace(p[:i]), strings.TrimSpace(p[i+n:])
	return tile, part, tile != "" && part != ""
}

func isGeneral(p string) bool {
	return placeholderWord(p, "grund", "reason", "uhrzeit", "time", "datum", "date", "eintrag", "entry")
}

func isList(part string) bool   { return placeholderWord(part, "liste", "list", "einträge", "entries") }
func isStatus(part string) bool { return placeholderWord(part, "status", "zustand") }

// sameLabel compares a value's label in either language.
func sameLabel(a, b string) bool {
	return strings.EqualFold(a, b) || strings.EqualFold(i18n.Source(i18n.EN, a), i18n.Source(i18n.EN, b))
}

// fill puts the current values into a message of this run.
func (w *wfRun) fill(text string) string {
	if !strings.Contains(text, "{") {
		return text
	}
	var out strings.Builder
	last := 0
	for _, at := range placeholderRe.FindAllStringIndex(text, -1) {
		m := text[at[0]:at[1]]
		out.WriteString(text[last:at[0]])
		last = at[1]
		v, ok := w.placeholder(m[1 : len(m)-1])
		if !ok {
			w.results = append(w.results, "⚠ "+i18n.T(w.lang, "Baustein %s nicht gefunden", m))
			v = m
		}
		// After a list of several lines, the text goes on in a new line.
		if strings.HasPrefix(v, "\n") && last < len(text) && text[last] != '\n' {
			v += "\n"
		}
		out.WriteString(v)
	}
	out.WriteString(text[last:])
	return out.String()
}

func (w *wfRun) placeholder(p string) (string, bool) {
	lang := w.lang
	switch {
	case placeholderWord(p, "grund", "reason"):
		return firstNonEmptyStr(w.why, i18n.T(lang, "Von Hand gestartet")), true
	case placeholderWord(p, "eintrag", "entry"):
		return firstNonEmptyStr(w.entry, "–"), true
	case placeholderWord(p, "uhrzeit", "time"):
		return time.Now().Format("15:04"), true
	case placeholderWord(p, "datum", "date"):
		if lang == i18n.EN {
			return time.Now().Format("2006-01-02"), true
		}
		return time.Now().Format("02.01.2006"), true
	}
	name, part, ok := splitPlaceholder(p)
	if !ok {
		return "", false
	}
	t, ok := w.tile(name)
	if !ok {
		return "", false
	}
	if isStatus(part) {
		w.s.mu.Lock()
		st, checked := w.s.latest[t.ID]
		w.s.mu.Unlock()
		switch {
		case !checked:
			return "?", true
		case st.Online:
			return i18n.T(lang, "erreichbar"), true
		}
		return i18n.T(lang, "nicht erreichbar"), true
	}
	wd := w.widgetOf(t)
	if wd == nil {
		return i18n.T(lang, "(%s antwortet nicht)", t.Name), true
	}
	if isList(part) {
		return listText(lang, wd.Items), true
	}
	for _, st := range wd.Stats {
		if sameLabel(st.Label, part) {
			return st.Value, true
		}
	}
	return "", false
}

// listText: one entry in the line, several as a list below it.
func listText(lang string, items []integrations.Item) string {
	line := func(it integrations.Item) string {
		s := it.Title
		if it.Subtitle != "" {
			s += " (" + it.Subtitle + ")"
		}
		if it.Badge != "" {
			s += " – " + it.Badge
		}
		return s
	}
	switch len(items) {
	case 0:
		return i18n.T(lang, "nichts")
	case 1:
		return line(items[0])
	}
	var out []string
	for i, it := range items {
		if i == maxListLines {
			out = append(out, i18n.T(lang, "… und %d weitere", len(items)-maxListLines))
			break
		}
		out = append(out, "• "+line(it))
	}
	return "\n" + strings.Join(out, "\n")
}

// tile finds one of the tiles the workflow may read, by its name.
func (w *wfRun) tile(name string) (store.Tile, bool) {
	if w.tiles == nil {
		w.tiles, _ = w.s.aiTiles(w.s.botRequest(w.ctx, w.user, w.lang))
	}
	for _, t := range w.tiles {
		if strings.EqualFold(strings.TrimSpace(t.Name), name) {
			return t, true
		}
	}
	return store.Tile{}, false
}

// widgetOf reads a tile once per run, without what its owner hid.
func (w *wfRun) widgetOf(t store.Tile) *integrations.Widget {
	if wd, ok := w.widgets[t.ID]; ok {
		return wd
	}
	if w.widgets == nil {
		w.widgets = map[int64]*integrations.Widget{}
	}
	var out *integrations.Widget
	if t.ShowWidget {
		wd, pending, err := w.s.widget(t, w.lang, 8*time.Second)
		if !pending && err == nil && wd != nil {
			out = hideParts(wd, t.Hide, w.lang)
		}
	}
	w.widgets[t.ID] = out
	return out
}

// checkPlaceholders says which building block of a message cannot work.
func (c *ruleCheck) checkPlaceholders(text string) error {
	for _, m := range placeholderRe.FindAllStringSubmatch(text, -1) {
		p := m[1]
		if isGeneral(p) {
			continue
		}
		name, part, ok := splitPlaceholder(p)
		if !ok {
			return fmt.Errorf("%s", i18n.T(c.lang, "Den Baustein %s kenne ich nicht – so geht es: {Kachel · Wert}, {Kachel · Liste}, {Kachel · Status}, {Grund}, {Uhrzeit}", m[0]))
		}
		var t *ruleTile
		for id := range c.tiles {
			if strings.EqualFold(strings.TrimSpace(c.tiles[id].Name), name) {
				tt := c.tiles[id]
				t = &tt
				break
			}
		}
		if t == nil {
			return fmt.Errorf("%s", i18n.T(c.lang, "Baustein %s: keine Kachel „%s“ – bitte den Namen wie auf dem Dashboard", m[0], name))
		}
		if isList(part) || isStatus(part) || len(t.Stats) == 0 {
			continue // a tile without values right now (service down) is not held against it
		}
		found := false
		for _, st := range t.Stats {
			found = found || sameLabel(st.Label, part)
		}
		if !found {
			var labels []string
			for _, st := range t.Stats {
				labels = append(labels, st.Label)
			}
			return fmt.Errorf("%s", i18n.T(c.lang, "Baustein %s: %s hat keinen Wert „%s“ – vorhanden: %s", m[0], t.Name, part, strings.Join(labels, ", ")))
		}
	}
	return nil
}
