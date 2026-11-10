package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heimdeck/heimdeck/internal/i18n"
	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/store"
)

// The workflows' API: list, save, delete, start, dry run – and a draft by the
// AI from a sentence, which the editor shows for checking before anything is
// saved.

// ruleTile is a tile as the workflow editor offers it.
type ruleTile struct {
	ID        int64                 `json:"id"`
	Name      string                `json:"name"`
	Group     string                `json:"group"`
	Monitored bool                  `json:"monitored"`
	Kind      string                `json:"kind"` // the integration
	Stats     []integrations.Stat   `json:"stats"`
	Actions   []integrations.Action `json:"actions"`
	List      string                `json:"list,omitempty"` // the tile has a list (its title), for {Tile · Liste}
}

// ruleTiles are the tiles a workflow may use, with their current values and
// buttons – as a workflow sees them when it runs: no locked or hidden groups,
// no values or buttons the owner hid on the tile.
func (s *Server) ruleTiles(r *http.Request) []ruleTile {
	r = s.botRequest(r.Context(), uid(r), langOf(r))
	tiles, groups := s.aiTiles(r)
	access, _ := s.groupsFor(uid(r))
	lang := langOf(r)
	out := make([]ruleTile, len(tiles))
	var wg sync.WaitGroup
	for i, t := range tiles {
		out[i] = ruleTile{ID: t.ID, Name: t.Name, Group: groups[t.GroupID].board + " · " + groups[t.GroupID].name, Monitored: t.Monitor, Kind: t.Integration, Stats: []integrations.Stat{}, Actions: []integrations.Action{}}
		if !t.ShowWidget {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			w, pending, err := s.widget(t, lang, 4*time.Second)
			if pending || err != nil || w == nil {
				return
			}
			w = hideParts(w, t.Hide, lang)
			out[i].Stats = append(out[i].Stats, w.Stats...)
			if w.ItemsTitle != "" || len(w.Items) > 0 {
				out[i].List = firstNonEmptyStr(w.ItemsTitle, i18n.T(lang, "Liste"))
			}
			integ, _ := integrations.Get(t.Integration)
			if _, ok := integ.(integrations.Actioner); ok && access[t.GroupID].Act && s.dockerAllowed(t) {
				out[i].Actions = append(out[i].Actions, w.Actions...)
				// Buttons of single entries, e.g. a switch "Garden lights".
				for j, it := range w.Items {
					if j == 25 {
						break
					}
					for _, a := range it.Actions {
						a.Label += " – " + clip(it.Title, 60)
						out[i].Actions = append(out[i].Actions, a)
					}
				}
			}
		}()
	}
	wg.Wait()
	return out
}

func (s *Server) handleRuleTiles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, s.ruleTiles(r))
}

func (s *Server) handleRules(w http.ResponseWriter, r *http.Request) {
	user := uid(r)
	rules := s.rulesOf(user)
	if rules == nil {
		rules = []rule{}
	}
	log := s.ruleLog(user)
	if log == nil {
		log = []ruleLogEntry{}
	}
	values := map[string]string{}
	running := map[string]bool{}
	s.rules.mu.Lock()
	for _, ru := range rules {
		key := stateKey(user, ru.ID)
		if st := s.rules.state[key]; st != nil && st.value != "" {
			values[ru.ID] = st.value
		}
		_, running[ru.ID] = s.rules.running[key]
	}
	s.rules.mu.Unlock()
	settings := s.settingsFor(user)
	telegram := s.notifySecrets(user).TelegramToken != "" && strings.TrimSpace(settings["telegramChat"]) != ""
	writeJSON(w, map[string]any{"rules": rules, "log": log, "values": values, "running": running, "telegram": telegram})
}

// ---- checking a workflow ----

// ruleCheck holds what a check needs: the tiles the user may use.
type ruleCheck struct {
	s     *Server
	r     *http.Request
	user  int64
	lang  string
	self  string // the workflow's own ID
	tiles map[int64]ruleTile
	steps int
}

func validDays(days []int) bool {
	for _, d := range days {
		if d < 0 || d > 6 {
			return false
		}
	}
	return true
}

// checkRule tidies up a workflow from the editor (or the AI) and checks it
// against what the user may use; buttons are taken from the tile, never from
// the browser.
func (s *Server) checkRule(r *http.Request, ru *rule) error {
	ru.normalize()
	ru.Name = strings.TrimSpace(ru.Name)
	if ru.Name == "" || len([]rune(ru.Name)) > 80 {
		return errors.New("Bitte der Regel einen Namen geben (bis 80 Zeichen)")
	}
	c := &ruleCheck{s: s, r: r, user: uid(r), lang: langOf(r), self: ru.ID, tiles: map[int64]ruleTile{}}
	for _, t := range s.ruleTiles(r) {
		c.tiles[t.ID] = t
	}
	if len(ru.Triggers) == 0 || len(ru.Triggers) > maxTriggers {
		return errors.New("Bitte 1 bis 5 Auslöser angeben")
	}
	if ru.All && ru.manual() {
		return errors.New("„Von Hand starten“ lässt sich nicht mit „und“ verbinden – bitte „oder“ wählen")
	}
	ru.All = ru.All && len(ru.Triggers) > 1
	ru.Layout = tidyLayout(ru.Layout)
	for i := range ru.Triggers {
		if err := c.trigger(&ru.Triggers[i]); err != nil {
			return err
		}
	}
	if !validDays(ru.Only.Days) {
		return errors.New("ungültiger Wochentag")
	}
	if (ru.Only.From == "") != (ru.Only.To == "") || ru.Only.From != "" && (!hhmmRe.MatchString(ru.Only.From) || !hhmmRe.MatchString(ru.Only.To)) {
		return errors.New("Für das Zeitfenster bitte Beginn und Ende angeben, z. B. 22:00 bis 06:00")
	}
	if len(ru.Conds) > maxConds {
		return errors.New("Höchstens 5 Bedingungen")
	}
	for i := range ru.Conds {
		if err := c.cond(&ru.Conds[i]); err != nil {
			return err
		}
	}
	if len(ru.Steps) == 0 {
		return errors.New("Bitte mindestens einen Schritt angeben")
	}
	if err := c.stepList(ru.Steps, 0); err != nil {
		return err
	}
	if ru.watches() && !onlyReports(ru.Steps) {
		return errors.New("Ein Workflow mit KI-Wächter darf nur Nachrichten schicken – keine Knöpfe und Szenen")
	}
	switch {
	case ru.Cooldown == 0:
		ru.Cooldown = 60
	case ru.Cooldown < 5:
		ru.Cooldown = 5
	case ru.Cooldown > 7*24*60:
		ru.Cooldown = 7 * 24 * 60
	}
	return nil
}

func (c *ruleCheck) trigger(tr *ruleTrigger) error {
	switch tr.Kind {
	case "time":
		if !hhmmRe.MatchString(tr.Time) {
			return errors.New("Bitte eine Uhrzeit wie 07:00 angeben")
		}
		if !validDays(tr.Days) {
			return errors.New("ungültiger Wochentag")
		}
		*tr = ruleTrigger{Kind: "time", Time: tr.Time, Days: tr.Days}
	case "manual":
		*tr = ruleTrigger{Kind: "manual"}
	case "ai":
		u, _ := c.s.store.User(c.user)
		if c.s.aiFor(u) == nil || !c.s.aiConfig().Dashboard {
			return errors.New("Für den KI-Wächter muss ein Admin der KI die Daten des Dashboards erlauben (Einstellungen → KI).")
		}
		prompt := strings.TrimSpace(tr.Prompt)
		if prompt == "" || len([]rune(prompt)) > watchMaxText {
			return errors.New("Bitte dem KI-Wächter sagen, wann er sich melden soll (bis 500 Zeichen)")
		}
		every := tr.Every
		if !slices.Contains(watchEvery, every) {
			every = 60
		}
		*tr = ruleTrigger{Kind: "ai", Prompt: prompt, Every: every}
	case "down", "up", "value", "item", "camera", "webhook":
		t, ok := c.tiles[tr.Tile]
		if !ok {
			return errors.New("Bitte eine Kachel wählen")
		}
		clean := ruleTrigger{Kind: tr.Kind, Tile: tr.Tile, Name: t.Name}
		switch tr.Kind {
		case "down", "up":
			if !t.Monitored {
				return errors.New("Diese Kachel wird nicht überwacht – in ihren Einstellungen die Online-Prüfung einschalten")
			}
			if tr.Kind == "down" {
				clean.For = tr.For
				if clean.For < 1 || clean.For > 1440 {
					clean.For = 2
				}
			}
		case "value":
			if err := c.value(t, tr.Stat, &tr.Op, tr.Value); err != nil {
				return err
			}
			clean.Stat, clean.Op, clean.Value = tr.Stat, tr.Op, tr.Value
			clean.For = min(max(tr.For, 0), 1440) // 0: as soon as it is past the limit
		case "item":
			if t.List == "" {
				return errors.New("Diese Kachel hat keine Liste – bitte eine Kachel mit Einträgen wählen (z. B. Downloads, Anfragen)")
			}
			clean.Gone = tr.Gone
			clean.Match = strings.TrimSpace(tr.Match)
			if len([]rune(clean.Match)) > 100 {
				return errors.New("Der Suchtext ist zu lang (bis 100 Zeichen)")
			}
		case "camera":
			if t.Kind != "camera" {
				return errors.New("Bitte eine Kamera wählen")
			}
			clean.Event = tr.Event
		case "webhook":
			if t.Kind != "webhook" {
				return errors.New("Bitte eine Webhook-Kachel wählen")
			}
			clean.Match = strings.TrimSpace(tr.Match)
			if len([]rune(clean.Match)) > 100 {
				return errors.New("Der Suchtext ist zu lang (bis 100 Zeichen)")
			}
		}
		*tr = clean
	default:
		return errors.New("Bitte einen Auslöser wählen")
	}
	return nil
}

// value checks a comparison with one of the tile's values.
func (c *ruleCheck) value(t ruleTile, stat string, op *string, limit string) error {
	if !slices.ContainsFunc(t.Stats, func(st integrations.Stat) bool { return st.Label == stat }) {
		return errors.New("Bitte einen Wert der Kachel wählen")
	}
	if *op != "<" && *op != "=" {
		*op = ">"
	}
	if _, ok := parseQuantity(limit, c.lang); !ok {
		return errors.New("Bitte eine Grenze als Zahl angeben, z. B. 90 % oder 100 GB")
	}
	return nil
}

func (c *ruleCheck) cond(cd *ruleCond) error {
	t, ok := c.tiles[cd.Tile]
	if !ok {
		return errors.New("Bitte eine Kachel wählen")
	}
	switch cd.Kind {
	case "online", "offline":
		if !t.Monitored {
			return errors.New("Diese Kachel wird nicht überwacht – in ihren Einstellungen die Online-Prüfung einschalten")
		}
		*cd = ruleCond{Kind: cd.Kind, Tile: cd.Tile, Name: t.Name}
	case "value":
		if err := c.value(t, cd.Stat, &cd.Op, cd.Value); err != nil {
			return err
		}
		*cd = ruleCond{Kind: "value", Tile: cd.Tile, Name: t.Name, Stat: cd.Stat, Op: cd.Op, Value: cd.Value}
	default:
		return errors.New("Bitte eine Bedingung wählen")
	}
	return nil
}

func (c *ruleCheck) stepList(list []ruleAction, depth int) error {
	for i := range list {
		c.steps++
		if c.steps > maxSteps {
			return errors.New("Höchstens 20 Schritte")
		}
		if err := c.step(&list[i], depth); err != nil {
			return err
		}
	}
	return nil
}

func (c *ruleCheck) step(a *ruleAction, depth int) error {
	switch a.Kind {
	case "notify":
		text := strings.TrimSpace(a.Text)
		if len([]rune(text)) > 1000 {
			return errors.New("Die Nachricht ist zu lang (bis 1000 Zeichen)")
		}
		if a.Report {
			u, _ := c.s.store.User(c.user)
			if c.s.aiFor(u) == nil || !c.s.aiConfig().Dashboard {
				return errors.New("Für den Lagebericht muss ein Admin der KI die Daten des Dashboards erlauben (Einstellungen → KI).")
			}
		}
		if a.Camera != 0 {
			if t, ok := c.tiles[a.Camera]; !ok || t.Kind != "camera" {
				return errors.New("Bitte eine Kamera wählen")
			}
		}
		if text == "" && !a.Report && a.Camera == 0 {
			return errors.New("Bitte einen Text für die Nachricht angeben")
		}
		if err := c.checkPlaceholders(text); err != nil {
			return err
		}
		if a.Review {
			u, _ := c.s.store.User(c.user)
			if c.s.aiFor(u) == nil || !c.s.aiConfig().Dashboard {
				return errors.New("Für den Lagebericht muss ein Admin der KI die Daten des Dashboards erlauben (Einstellungen → KI).")
			}
		}
		var channels []string
		for _, ch := range a.Channels {
			if (ch == "telegram" || ch == "discord" || ch == "ntfy") && !slices.Contains(channels, ch) {
				channels = append(channels, ch)
			}
		}
		*a = ruleAction{Kind: "notify", Text: text, Report: a.Report, Review: a.Review, Camera: a.Camera, Channels: channels}
	case "http":
		raw := strings.TrimSpace(a.URL)
		u, err := url.Parse(raw)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(raw) > 500 {
			return errors.New("Bitte eine Adresse wie https://beispiel.de/hook angeben")
		}
		method := strings.ToUpper(strings.TrimSpace(a.Method))
		if method != "GET" && method != "POST" && method != "PUT" {
			method = "POST"
		}
		body := strings.TrimSpace(a.Text)
		if len([]rune(body)) > 2000 {
			return errors.New("Der Inhalt ist zu lang (bis 2000 Zeichen)")
		}
		if err := c.checkPlaceholders(body); err != nil {
			return err
		}
		if method == "GET" {
			body = ""
		}
		*a = ruleAction{Kind: "http", URL: raw, Method: method, Text: body}
	case "scene":
		scene, ok := c.s.sceneOf(c.user, a.Scene)
		if !ok || c.self != "" && scene.ID == c.self {
			return errors.New("Bitte eine Szene wählen")
		}
		*a = ruleAction{Kind: "scene", Scene: scene.ID, Label: scene.Name}
	case "button":
		t, ok := c.tiles[a.Tile]
		if !ok {
			return errors.New("Bitte eine Kachel wählen")
		}
		var chosen *integrations.Action
		if a.Button != nil && *a.Button >= 0 && *a.Button < len(t.Actions) {
			chosen = &t.Actions[*a.Button] // from an AI draft
		}
		for i, act := range t.Actions {
			if chosen == nil && act.ID == a.Action && fmt.Sprint(act.Params) == fmt.Sprint(a.Params) {
				chosen = &t.Actions[i]
			}
		}
		if chosen == nil {
			return errors.New("Diesen Knopf hat die Kachel gerade nicht – ist der Dienst erreichbar, und darfst du ihre Knöpfe benutzen?")
		}
		*a = ruleAction{Kind: "button", Tile: a.Tile, Action: chosen.ID, Params: chosen.Params, Label: chosen.Label + " · " + t.Name, Confirm: chosen.Confirm}
	case "wait":
		if a.Minutes < 1 || a.Minutes > maxWait {
			return errors.New("Bitte eine Wartezeit von 1 Minute bis 24 Stunden angeben")
		}
		*a = ruleAction{Kind: "wait", Minutes: a.Minutes}
	case "if":
		if depth >= 1 {
			return errors.New("Ein „Wenn“ in einem „Wenn“ geht nicht – bitte eine Ebene")
		}
		if a.If == nil {
			return errors.New("Bitte eine Bedingung wählen")
		}
		cd := *a.If
		if err := c.cond(&cd); err != nil {
			return err
		}
		if len(a.Then)+len(a.Else) == 0 {
			return errors.New("Bitte bei „Wenn“ mindestens einen Schritt angeben")
		}
		if err := c.stepList(a.Then, depth+1); err != nil {
			return err
		}
		if err := c.stepList(a.Else, depth+1); err != nil {
			return err
		}
		*a = ruleAction{Kind: "if", If: &cd, Then: a.Then, Else: a.Else}
	default:
		return errors.New("Bitte eine Aktion wählen")
	}
	return nil
}

// ---- handlers ----

func (s *Server) handleSaveRule(w http.ResponseWriter, r *http.Request) {
	var ru rule
	if err := readJSON(r, &ru); err != nil {
		writeErr(w, 400, err)
		return
	}
	ru.normalize()
	user := uid(r)
	id := r.PathValue("id")
	ru.ID = id
	checked := false
	// Switching a workflow on or off works even while its service does not answer.
	if id != "" {
		for _, old := range s.rulesOf(user) {
			if old.ID == id && sameRule(old, ru) {
				old.On = ru.On
				ru, checked = old, true
			}
		}
	}
	if !checked {
		if err := s.checkRule(r, &ru); err != nil {
			writeErr(w, 400, err)
			return
		}
	}
	ru, err := s.putRule(user, ru)
	switch {
	case errors.Is(err, errNotFound):
		writeErr(w, 404, err)
		return
	case errors.Is(err, errTooManyRules):
		writeErr(w, 400, err)
		return
	case err != nil:
		writeErr(w, 500, err)
		return
	}
	slog.Info("Automation gespeichert", "regel", ru.Name, "auslöser", len(ru.Triggers), "schritte", len(ru.Steps), "benutzer", s.username(r))
	writeJSON(w, ru)
}

var errTooManyRules = errors.New("Höchstens 50 Regeln")

// putRule stores a checked workflow: a new one without an ID, or in place of
// the one with its ID. A changed workflow starts afresh; a run that is still
// waiting stops.
func (s *Server) putRule(user int64, ru rule) (rule, error) {
	rulesMu.Lock()
	rules := s.rulesOf(user)
	if ru.ID != "" {
		i := slices.IndexFunc(rules, func(x rule) bool { return x.ID == ru.ID })
		if i < 0 {
			rulesMu.Unlock()
			return ru, errNotFound
		}
		rules[i] = ru
	} else {
		if len(rules) >= maxRules {
			rulesMu.Unlock()
			return ru, errTooManyRules
		}
		ru.ID = strconv.FormatInt(time.Now().UnixNano(), 36)
		rules = append(rules, ru)
	}
	err := s.saveRules(user, rules)
	rulesMu.Unlock()
	if err != nil {
		return ru, err
	}
	s.stopRuns(stateKey(user, ru.ID))
	s.rules.mu.Lock()
	prefix := stateKey(user, ru.ID)
	for k := range s.rules.state {
		if k == prefix || strings.HasPrefix(k, prefix+":") {
			delete(s.rules.state, k)
		}
	}
	s.rules.mu.Unlock()
	s.kickBots() // a workflow that asks, or a scene, needs the bot listening
	return ru, nil
}

// tidyLayout keeps at most 100 sensible places of blocks on the canvas.
func tidyLayout(l map[string]rulePos) map[string]rulePos {
	out := map[string]rulePos{}
	for k, p := range l {
		if len(out) == 100 || len(k) > 40 || p.X < -1e5 || p.X > 1e5 || p.Y < -1e5 || p.Y > 1e5 {
			continue
		}
		out[k] = p
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sameRule: the two differ at most in being switched on.
func sameRule(a, b rule) bool {
	a.On, b.On, b.ID = false, false, a.ID
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return string(x) == string(y)
}

func (s *Server) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	user, id := uid(r), r.PathValue("id")
	rulesMu.Lock()
	rules := s.rulesOf(user)
	i := slices.IndexFunc(rules, func(x rule) bool { return x.ID == id })
	if i < 0 {
		rulesMu.Unlock()
		writeErr(w, 404, errNotFound)
		return
	}
	err := s.saveRules(user, slices.Delete(rules, i, i+1))
	rulesMu.Unlock()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	s.stopRuns(stateKey(user, id))
	s.kickBots()
	writeJSON(w, map[string]bool{"ok": true})
}

// handleRunRule starts a workflow now ("Start"), without its triggers. One
// that waits runs on in the background.
func (s *Server) handleRunRule(w http.ResponseWriter, r *http.Request) {
	user, id := uid(r), r.PathValue("id")
	for _, ru := range s.rulesOf(user) {
		if ru.ID == id {
			writeJSON(w, s.startScene(r.Context(), user, ru, "Einstellungen"))
			return
		}
	}
	writeErr(w, 404, errNotFound)
}

// handleDryRun says what the workflow would do now – stored or still in the editor.
func (s *Server) handleDryRun(w http.ResponseWriter, r *http.Request) {
	var ru rule
	if err := readJSON(r, &ru); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.checkRule(r, &ru); err != nil {
		writeErr(w, 400, err)
		return
	}
	user, lang := uid(r), langOf(r)
	var lines []string
	conds := []bool{}
	for _, cd := range ru.Conds {
		holds, desc := s.condHolds(user, lang, cd)
		mark := "✖"
		if holds {
			mark = "✔"
		}
		conds = append(conds, holds)
		lines = append(lines, mark+" "+desc)
	}
	// Which triggers hold right now (values and outages; moments have no "now").
	triggers := make([]*bool, len(ru.Triggers))
	for i, tr := range ru.Triggers {
		var v bool
		switch tr.Kind {
		case "value":
			h, _, _, ok := s.valueHolds(user, tr.Tile, tr.Stat, tr.Op, tr.Value, lang)
			if !ok {
				continue
			}
			v = h
		case "down", "up":
			s.mu.Lock()
			st, checked := s.latest[tr.Tile]
			s.mu.Unlock()
			if !checked {
				continue
			}
			v = st.Online == (tr.Kind == "up")
		default:
			continue
		}
		triggers[i] = &v
	}
	ctx, cancel := timeout(r, watchAITimeout)
	defer cancel()
	if r.URL.Query().Get("quiet") != "1" {
		// The editor's preview while typing leaves out the AI watcher (it may cost money).
		lines = append(lines, s.watchDry(ctx, user, s.userLang(user), ru)...) // in the language of the run below
	}
	e := s.runRule(ctx, user, ru, "", runDry)
	if e.Result != "" {
		lines = append(lines, strings.Split(e.Result, "\n")...)
	}
	branches := e.branches
	if branches == nil {
		branches = []bool{}
	}
	writeJSON(w, map[string]any{"lines": lines, "conds": s.condsHold(user, lang, ru.Conds), "condList": conds, "triggers": triggers, "branches": branches})
}

// ---- the AI drafts a workflow ----

// handleDraftRule turns a sentence into a workflow for the editor. Nothing is
// saved: the person checks the draft and saves it themselves.
func (s *Server) handleDraftRule(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.User(uid(r))
	if err != nil || s.aiFor(u) == nil {
		writeErr(w, 403, errors.New("Die KI ist nicht eingerichtet"))
		return
	}
	var in struct {
		Text    string `json:"text"`
		Current *rule  `json:"current"`
	}
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Text) == "" {
		writeErr(w, 400, errors.New("Beschreib kurz, was passieren soll"))
		return
	}
	ctx, cancel := timeout(r, 3*time.Minute)
	defer cancel()
	draft, problem, err := s.draftRule(r.WithContext(ctx), u, in.Text, in.Current)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	if problem != "" {
		problem = tr(r, problem)
	}
	writeJSON(w, map[string]any{"rule": draft, "problem": problem})
}

// draftRule has the AI turn a sentence into a workflow – a new one, or a
// change of current. Nothing is saved. problem says what the check found
// (German, to translate); the draft is then only tidied up, not checked.
func (s *Server) draftRule(r *http.Request, u store.User, text string, current *rule) (rule, string, error) {
	lang := langOf(r)
	tiles := s.ruleTiles(r)
	var cat strings.Builder
	for _, t := range tiles {
		if len(t.Stats) == 0 && len(t.Actions) == 0 && t.List == "" && !t.Monitored && t.Kind != "camera" && t.Kind != "webhook" {
			continue
		}
		fmt.Fprintf(&cat, "- tile %d %q (%s)", t.ID, t.Name, t.Kind)
		if t.Monitored {
			cat.WriteString(", monitored")
		}
		if len(t.Stats) > 0 {
			var vals []string
			for _, st := range t.Stats {
				vals = append(vals, fmt.Sprintf("%q = %s", st.Label, st.Value))
			}
			cat.WriteString("; values: " + strings.Join(vals, ", "))
		}
		if t.List != "" {
			fmt.Fprintf(&cat, "; list: %q", t.List)
		}
		if len(t.Actions) > 0 {
			var bs []string
			for i, a := range t.Actions {
				if i == 30 {
					break
				}
				bs = append(bs, fmt.Sprintf("%d %q", i, a.Label))
			}
			cat.WriteString("; buttons: " + strings.Join(bs, ", "))
		}
		cat.WriteString("\n")
	}
	watchHint := ""
	if s.aiConfig().Dashboard {
		watchHint = " · {\"kind\":\"ai\",\"prompt\":\"when the AI watcher should report, in the person's words\",\"every\":15, 30, 60, 180, 360 or 1440} " +
			"(the AI looks at the dashboard every so many minutes; a workflow with it may only have notify, wait and if steps)"
	}
	var sceneNames []string
	for _, sc := range s.scenes(u.ID, "") {
		sceneNames = append(sceneNames, fmt.Sprintf("%q", sc.Name))
	}
	system := "You turn a description into a Heimdeck workflow (an automation of a home dashboard). Answer with ONE JSON object only, no text around it.\n" +
		"Shape: {\"name\":\"short name\",\"triggers\":[…],\"all\":false,\"only\":{\"from\":\"HH:MM\",\"to\":\"HH:MM\",\"days\":[…]},\"conds\":[…],\"steps\":[…],\"cooldown\":minutes}\n" +
		"Triggers (any one starts the workflow): {\"kind\":\"time\",\"time\":\"HH:MM\",\"days\":[1,2,3,4,5]} (days 0=Sunday … 6=Saturday, empty = every day) · " +
		"{\"kind\":\"down\",\"tile\":ID,\"for\":minutes} (a monitored service stops answering) · {\"kind\":\"up\",\"tile\":ID} · " +
		"{\"kind\":\"value\",\"tile\":ID,\"stat\":\"exact value label\",\"op\":\">\" or \"<\",\"value\":\"90 %\"} · " +
		"{\"kind\":\"camera\",\"tile\":ID,\"event\":\"\" or \"Person\", \"Klingel\" (doorbell), \"Bewegung\" (motion), \"Fahrzeug\", \"Tier\"} · " +
		"{\"kind\":\"webhook\",\"tile\":ID,\"match\":\"text in the message or empty\"} · {\"kind\":\"item\",\"tile\":ID of a tile with a list,\"gone\":false,\"match\":\"\"} (a new entry appears in the tile's list, e.g. a new request; with \"gone\":true an entry leaves it, e.g. a download finished) · {\"kind\":\"manual\"} (started by hand: a scene)" + watchHint + ". A value trigger may have \"for\":minutes – the value has to stay past the limit that long.\n" +
		"Triggers are joined by \"or\" (any one starts it); with \"all\":true by \"and\": it starts when one happens while all the others hold (e.g. at 20:00 and Jellyfin streams > 0). \"only\" (optional) limits the automatic triggers to a time window and days. Conditions (all must hold): {\"kind\":\"value\",\"tile\":ID,\"stat\":…,\"op\":…,\"value\":…} · {\"kind\":\"online\",\"tile\":ID} · {\"kind\":\"offline\",\"tile\":ID}.\n" +
		"Steps, in order: {\"kind\":\"notify\",\"text\":\"message\",\"report\":false,\"camera\":ID of a camera tile or 0} · {\"kind\":\"button\",\"tile\":ID,\"button\":number from the tile's buttons} · " +
		"{\"kind\":\"http\",\"method\":\"POST\",\"url\":\"https://…\",\"text\":\"body\"} (calls an address, e.g. a Home Assistant webhook) · {\"kind\":\"scene\",\"scene\":\"name\"} · {\"kind\":\"wait\",\"minutes\":N} · {\"kind\":\"if\",\"if\":condition,\"then\":[steps],\"else\":[steps]} (no if inside an if).\n" +
		"A notify text may contain building blocks that are filled in when it is sent: {tile name · value label} (a value listed below), {tile name · Liste} (the tile's list, e.g. what is playing; only for tiles with a list), {tile name · Status} (reachable or not), {Grund} (why it started), {Eintrag} (the new or finished entry, or the webhook message), {Uhrzeit}, {Datum}. A notify step may have \"channels\":[\"telegram\"] (or discord, ntfy) to go only there, and \"review\":true for the AI's review of the week. Use them whenever the message should say what is going on, e.g. \"Jellyfin: {Jellyfin · Liste} – CPU {Proxmox · CPU}\".\n" +
		"Use only the tiles, value labels, buttons and scenes listed below, with their exact IDs, labels and numbers. If something asked for does not exist, leave it out and add a notify step that says what is missing. " +
		"Write names and messages in " + aiLanguage(lang) + ".\n\nTiles:\n" + cat.String()
	if len(sceneNames) > 0 {
		system += "\nScenes: " + strings.Join(sceneNames, ", ") + "\n"
	}
	ask := clip(text, 2000)
	if current != nil {
		cur, _ := json.Marshal(current)
		ask = "Change this workflow as described, keep the rest:\n" + string(cur) + "\n\nChange: " + ask
	}
	answer, _, err := aiComplete(r.Context(), s.aiConfig(), []any{map[string]string{"role": "system", "content": system}, map[string]string{"role": "user", "content": ask}}, false, false, nil, lang, false)
	if err != nil {
		return rule{}, "", err
	}
	draft, err := parseDraft(answer)
	if err != nil {
		slog.Debug("KI-Entwurf nicht lesbar", "antwort", clip(answer, 500))
		return rule{}, "", errors.New(i18n.T(lang, "Die KI hat keinen lesbaren Workflow geliefert – bitte anders beschreiben oder ein größeres Modell nehmen"))
	}
	if current != nil {
		draft.ID, draft.On, draft.NoAsk = current.ID, current.On, current.NoAsk
	} else {
		draft.On, draft.NoAsk = true, false // "without asking" only ever by the person's own tick
	}
	// Checked like a saved one; a problem is shown to fix by hand.
	checked := draft
	problem := ""
	if err := s.checkRule(r, &checked); err != nil {
		problem = err.Error()
		draft.normalize()
	} else {
		draft = checked
	}
	slog.Info("KI-Entwurf für einen Workflow", "benutzer", u.Username, "problem", problem)
	return draft, problem, nil
}

// parseDraft reads the JSON object out of a model's answer (which may come
// wrapped in a code block or with a sentence around it).
func parseDraft(text string) (rule, error) {
	a, b := strings.Index(text, "{"), strings.LastIndex(text, "}")
	if a < 0 || b <= a {
		return rule{}, errors.New("no JSON")
	}
	var r rule
	if err := json.Unmarshal([]byte(text[a:b+1]), &r); err != nil {
		return rule{}, err
	}
	r.normalize()
	return r, nil
}
