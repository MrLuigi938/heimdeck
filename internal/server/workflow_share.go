package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/heimdeck/heimdeck/internal/i18n"
)

// Workflows to take along: as a file to share, in Heimdeck's backup, and as
// templates. Tiles have other numbers on another Heimdeck (and after a
// restore), so a portable workflow names its tiles – and a template names
// only the kind of tile (sabnzbd|qbittorrent …). Bringing one in looks the
// names up among the tiles this person may use.

const shareFormat = 1

// sharedWorkflow is the file of one workflow.
type sharedWorkflow struct {
	Heimdeck int  `json:"heimdeckWorkflow"`
	Rule     rule `json:"workflow"`
}

// portable replaces tile and scene numbers by names.
func (s *Server) portable(r rule) rule {
	b, _ := json.Marshal(r)
	var p rule
	json.Unmarshal(b, &p)
	name := func(id int64) string {
		if id == 0 {
			return ""
		}
		if t, err := s.store.Tile(id); err == nil {
			return t.Name
		}
		return ""
	}
	p.ID = ""
	for i := range p.Triggers {
		if p.Triggers[i].Tile != 0 {
			p.Triggers[i].Name = firstNonEmptyStr(name(p.Triggers[i].Tile), p.Triggers[i].Name)
			p.Triggers[i].Tile = 0
		}
	}
	for i := range p.Conds {
		p.Conds[i].Name = firstNonEmptyStr(name(p.Conds[i].Tile), p.Conds[i].Name)
		p.Conds[i].Tile = 0
	}
	var steps func(list []ruleAction)
	steps = func(list []ruleAction) {
		for i := range list {
			a := &list[i]
			if a.Tile != 0 {
				a.TileName, a.Tile = name(a.Tile), 0
			}
			if a.Camera != 0 {
				a.CameraName, a.Camera = name(a.Camera), 0
			}
			if a.Kind == "scene" {
				a.Scene = "" // by its name (Label)
			}
			if a.If != nil {
				a.If.Name = firstNonEmptyStr(name(a.If.Tile), a.If.Name)
				a.If.Tile = 0
			}
			steps(a.Then)
			steps(a.Else)
		}
	}
	steps(p.Steps)
	return p
}

// resolve finds the tiles and scenes of a portable workflow among those the
// person may use (by name, else by kind). What is not found stays empty; the
// editor (or the check) then asks for it.
func (s *Server) resolve(r *http.Request, p rule) rule {
	p.normalize()
	tiles := s.ruleTiles(r)
	lang := langOf(r)
	find := func(name, kind string) (ruleTile, bool) {
		if name != "" {
			for _, t := range tiles {
				if strings.EqualFold(strings.TrimSpace(t.Name), strings.TrimSpace(name)) {
					return t, true
				}
			}
		}
		for _, k := range strings.Split(kind, "|") {
			for _, t := range tiles {
				if k != "" && t.Kind == k {
					return t, true
				}
			}
		}
		return ruleTile{}, false
	}
	first := ""
	for i := range p.Triggers {
		tr := &p.Triggers[i]
		if tr.Tile != 0 || tr.Kind == "time" || tr.Kind == "manual" || tr.Kind == "ai" {
			continue
		}
		if t, ok := find(tr.Name, tr.TileKind); ok {
			tr.Tile, tr.Name = t.ID, t.Name
			first = firstNonEmptyStr(first, t.Name)
		}
		tr.TileKind = ""
	}
	for i := range p.Conds {
		c := &p.Conds[i]
		if t, ok := find(c.Name, c.TileKind); ok && c.Tile == 0 {
			c.Tile, c.Name = t.ID, t.Name
		}
		c.TileKind = ""
	}
	ai := s.aiConfig().Dashboard
	var steps func(list []ruleAction)
	steps = func(list []ruleAction) {
		for i := range list {
			a := &list[i]
			// A template's text may name the first trigger's tile.
			a.Text = strings.ReplaceAll(a.Text, "%TILE%", firstNonEmptyStr(first, "…"))
			if !ai {
				a.Report, a.Review = false, false
			}
			switch a.Kind {
			case "button":
				if t, ok := find(a.TileName, a.TileKind); ok && a.Tile == 0 {
					a.Tile = t.ID
					// The button by its label (the first part of "Pausieren · SABnzbd").
					want, _, _ := strings.Cut(a.Label, " · ")
					for j, act := range t.Actions {
						if act.ID == a.Action && sameParams(act.Params, a.Params) || sameLabel(act.Label, want) {
							a.Action, a.Params = act.ID, act.Params
							b := j
							a.Button = &b
							break
						}
					}
				}
			case "notify":
				if a.CameraName != "" {
					if t, ok := find(a.CameraName, "camera"); ok {
						a.Camera = t.ID
					}
				}
			case "scene":
				if sc, ok := s.sceneOf(uid(r), a.Label); ok {
					a.Scene = sc.ID
				}
			}
			a.TileName, a.TileKind, a.CameraName = "", "", ""
			if a.If != nil {
				if t, ok := find(a.If.Name, a.If.TileKind); ok && a.If.Tile == 0 {
					a.If.Tile, a.If.Name = t.ID, t.Name
				}
				a.If.TileKind = ""
			}
			steps(a.Then)
			steps(a.Else)
		}
	}
	steps(p.Steps)
	if p.Name == "" {
		p.Name = i18n.T(lang, "Workflow")
	}
	return p
}

func sameParams(a, b map[string]string) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y) || len(a) == 0 && len(b) == 0
}

// handleExportRule hands out one workflow as a file to share.
func (s *Server) handleExportRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, ru := range s.rulesOf(uid(r)) {
		if ru.ID == id {
			p := s.portable(ru)
			name := strings.Map(func(c rune) rune {
				if c < 128 && (c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
					return c
				}
				return '-'
			}, ru.Name)
			w.Header().Set("Content-Disposition", `attachment; filename="workflow-`+strings.Trim(name, "-")+`.json"`)
			writeJSON(w, sharedWorkflow{Heimdeck: shareFormat, Rule: p})
			return
		}
	}
	writeErr(w, 404, errNotFound)
}

// handleImportRule reads a shared workflow (or a template) for the editor:
// it is not saved – the person checks it, picks what was not found, and saves.
func (s *Server) handleImportRule(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 256<<10))
	if err != nil {
		writeErr(w, 400, errors.New("ungültige Anfrage"))
		return
	}
	var f sharedWorkflow
	if json.Unmarshal(body, &f) != nil || f.Heimdeck != shareFormat {
		writeErr(w, 400, errors.New("Das ist keine Workflow-Datei von Heimdeck"))
		return
	}
	ru := s.resolve(r, f.Rule)
	ru.ID, ru.On, ru.NoAsk = "", true, false // "without asking" only by the person's own tick
	checked := ru
	problem := ""
	if err := s.checkRule(r, &checked); err != nil {
		problem = tr(r, err.Error())
	} else {
		ru = checked
	}
	writeJSON(w, map[string]any{"rule": ru, "problem": problem})
}

// ---- in the backup ----

// exportRules: the person's workflows for the backup, portable.
func (s *Server) exportRules(user int64) []rule {
	out := []rule{}
	for _, ru := range s.rulesOf(user) {
		p := s.portable(ru)
		p.ID = ru.ID // kept, so scenes started by others find each other again
		out = append(out, p)
	}
	return out
}

// importRules adds the workflows of a backup: checked like saved ones; one
// whose tiles or buttons are missing comes in switched off, to fix in the editor.
func (s *Server) importRules(r *http.Request, list []rule) int {
	user := uid(r)
	if len(list) == 0 {
		return 0
	}
	// Scenes first, so workflows that start them find them by name.
	order := make([]rule, 0, len(list))
	for _, ru := range list {
		if ru.manual() {
			order = append(order, ru)
		}
	}
	for _, ru := range list {
		if !ru.manual() {
			order = append(order, ru)
		}
	}
	n := 0
	for _, p := range order {
		ru := s.resolve(r, p)
		on := ru.On
		ru.ID = ""
		checked := ru
		if err := s.checkRule(r, &checked); err == nil {
			ru = checked
			ru.On = on
		} else {
			ru.On = false
		}
		if _, err := s.putRule(user, ru); err != nil {
			break
		}
		n++
	}
	return n
}

// ---- templates ----

type ruleTemplate struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Desc string `json:"desc"`
	Rule rule   `json:"workflow"`
}

var downloaders = "sabnzbd|qbittorrent|transmission|deluge|nzbget"

// templates are ready workflows to start from; their tiles are found by kind.
func templates() []ruleTemplate {
	return []ruleTemplate{
		{ID: "download-done", Name: "Download fertig", Desc: "Nachricht, sobald ein Download die Warteschlange verlässt",
			Rule: rule{Name: "Download fertig", Cooldown: 5,
				Triggers: []ruleTrigger{{Kind: "item", Gone: true, TileKind: downloaders}},
				Steps:    []ruleAction{{Kind: "notify", Text: "✅ Fertig: {Eintrag}"}}}},
		{ID: "new-request", Name: "Neue Anfrage", Desc: "Nachricht, wenn jemand einen Film oder eine Serie anfragt",
			Rule: rule{Name: "Neue Anfrage", Cooldown: 5,
				Triggers: []ruleTrigger{{Kind: "item", TileKind: "jellyseerr|overseerr|ombi"}},
				Steps:    []ruleAction{{Kind: "notify", Text: "🎬 Neue Anfrage: {Eintrag}"}}}},
		{ID: "service-down", Name: "Dienst ausgefallen", Desc: "Nachricht mit Lagebericht, wenn ein Dienst 5 Minuten nicht antwortet",
			Rule: rule{Name: "Dienst ausgefallen", Cooldown: 60,
				Triggers: []ruleTrigger{{Kind: "down", For: 5}},
				Steps:    []ruleAction{{Kind: "notify", Text: "🔴 {Grund}", Report: true}}}},
		{ID: "disk-full", Name: "Speicher fast voll", Desc: "Warnung, wenn ein Wert 10 Minuten über der Grenze liegt",
			Rule: rule{Name: "Speicher fast voll", Cooldown: 360,
				Triggers: []ruleTrigger{{Kind: "value", Op: ">", Value: "90 %", For: 10}},
				Steps:    []ruleAction{{Kind: "notify", Text: "💾 {Grund}"}}}},
		{ID: "streams", Name: "Wer streamt gerade?", Desc: "Nachricht mit dem, was läuft, sobald gestreamt wird",
			Rule: rule{Name: "Wer streamt gerade?", Cooldown: 60,
				Triggers: []ruleTrigger{{Kind: "value", TileKind: "jellyfin|emby|plex", Stat: "Streams", Op: ">", Value: "0"}},
				Steps:    []ruleAction{{Kind: "notify", Text: "📺 {%TILE% · Liste}"}}}},
		{ID: "morning", Name: "Morgenbericht", Desc: "An Werktagen um 7 Uhr der Lagebericht der KI",
			Rule: rule{Name: "Morgenbericht",
				Triggers: []ruleTrigger{{Kind: "time", Time: "07:00", Days: []int{1, 2, 3, 4, 5}}},
				Steps:    []ruleAction{{Kind: "notify", Text: "☀️ Guten Morgen!", Report: true}}}},
		{ID: "week", Name: "Wochenrückblick", Desc: "Sonntags um 18 Uhr: wie die Woche lief",
			Rule: rule{Name: "Wochenrückblick",
				Triggers: []ruleTrigger{{Kind: "time", Time: "18:00", Days: []int{0}}},
				Steps:    []ruleAction{{Kind: "notify", Text: "📅", Review: true}}}},
		{ID: "movie-night", Name: "Filmabend (Szene)", Desc: "Von Hand oder per /szene: Downloads pausieren und Bescheid geben",
			Rule: rule{Name: "Filmabend",
				Triggers: []ruleTrigger{{Kind: "manual"}},
				Steps: []ruleAction{{Kind: "button", TileKind: downloaders, Label: "Pausieren"},
					{Kind: "notify", Text: "🍿 Filmabend – Downloads sind pausiert"}}}},
	}
}

// handleTemplates lists the templates, already fitted to this person's tiles.
func (s *Server) handleTemplates(w http.ResponseWriter, r *http.Request) {
	lang := langOf(r)
	out := []ruleTemplate{}
	for _, tpl := range templates() {
		ru := tpl.Rule
		ru.Name = i18n.T(lang, ru.Name)
		var tr func(list []ruleAction)
		tr = func(list []ruleAction) {
			for i := range list {
				list[i].Text = i18n.T(lang, list[i].Text)
				if list[i].Label != "" {
					list[i].Label = i18n.T(lang, list[i].Label)
				}
			}
		}
		tr(ru.Steps)
		ru = s.resolve(r, ru)
		ru.On = true
		out = append(out, ruleTemplate{ID: tpl.ID, Name: i18n.T(lang, tpl.Name), Desc: i18n.T(lang, tpl.Desc), Rule: ru})
	}
	writeJSON(w, out)
}
