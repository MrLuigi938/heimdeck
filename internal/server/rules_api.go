package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heimdeck/heimdeck/internal/integrations"
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
}

// ruleTiles are the tiles a workflow may use, with their current values and buttons.
func (s *Server) ruleTiles(r *http.Request) []ruleTile {
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
			out[i].Stats = append(out[i].Stats, w.Stats...)
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
		running[ru.ID] = s.rules.running[key]
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
	case "down", "up", "value", "camera", "webhook":
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
		*a = ruleAction{Kind: "notify", Text: text, Report: a.Report, Camera: a.Camera}
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
	rulesMu.Lock()
	rules := s.rulesOf(user)
	if id != "" {
		i := slices.IndexFunc(rules, func(x rule) bool { return x.ID == id })
		if i < 0 {
			rulesMu.Unlock()
			writeErr(w, 404, errNotFound)
			return
		}
		rules[i] = ru
	} else {
		if len(rules) >= maxRules {
			rulesMu.Unlock()
			writeErr(w, 400, errors.New("Höchstens 50 Regeln"))
			return
		}
		ru.ID = strconv.FormatInt(time.Now().UnixNano(), 36)
		rules = append(rules, ru)
	}
	err := s.saveRules(user, rules)
	rulesMu.Unlock()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	// A changed workflow starts afresh.
	s.rules.mu.Lock()
	prefix := stateKey(user, ru.ID)
	for k := range s.rules.state {
		if k == prefix || strings.HasPrefix(k, prefix+":") {
			delete(s.rules.state, k)
		}
	}
	s.rules.mu.Unlock()
	s.kickBots() // a workflow that asks, or a scene, needs the bot listening
	slog.Info("Automation gespeichert", "regel", ru.Name, "auslöser", len(ru.Triggers), "schritte", len(ru.Steps), "benutzer", s.username(r))
	writeJSON(w, ru)
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
	for _, cd := range ru.Conds {
		holds, desc := s.condHolds(user, lang, cd)
		mark := "✖"
		if holds {
			mark = "✔"
		}
		lines = append(lines, mark+" "+desc)
	}
	ctx, cancel := timeout(r, 30*time.Second)
	defer cancel()
	e := s.runRule(ctx, user, ru, "", runDry)
	if e.Result != "" {
		lines = append(lines, strings.Split(e.Result, "\n")...)
	}
	writeJSON(w, map[string]any{"lines": lines, "conds": s.condsHold(user, lang, ru.Conds)})
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
	lang := langOf(r)
	tiles := s.ruleTiles(r)
	var cat strings.Builder
	for _, t := range tiles {
		if len(t.Stats) == 0 && len(t.Actions) == 0 && !t.Monitored && t.Kind != "camera" && t.Kind != "webhook" {
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
	var sceneNames []string
	for _, sc := range s.scenes(u.ID, "") {
		sceneNames = append(sceneNames, fmt.Sprintf("%q", sc.Name))
	}
	system := "You turn a description into a Heimdeck workflow (an automation of a home dashboard). Answer with ONE JSON object only, no text around it.\n" +
		"Shape: {\"name\":\"short name\",\"triggers\":[…],\"only\":{\"from\":\"HH:MM\",\"to\":\"HH:MM\",\"days\":[…]},\"conds\":[…],\"steps\":[…],\"cooldown\":minutes}\n" +
		"Triggers (any one starts the workflow): {\"kind\":\"time\",\"time\":\"HH:MM\",\"days\":[1,2,3,4,5]} (days 0=Sunday … 6=Saturday, empty = every day) · " +
		"{\"kind\":\"down\",\"tile\":ID,\"for\":minutes} (a monitored service stops answering) · {\"kind\":\"up\",\"tile\":ID} · " +
		"{\"kind\":\"value\",\"tile\":ID,\"stat\":\"exact value label\",\"op\":\">\" or \"<\",\"value\":\"90 %\"} · " +
		"{\"kind\":\"camera\",\"tile\":ID,\"event\":\"\" or \"Person\", \"Klingel\" (doorbell), \"Bewegung\" (motion), \"Fahrzeug\", \"Tier\"} · " +
		"{\"kind\":\"webhook\",\"tile\":ID,\"match\":\"text in the message or empty\"} · {\"kind\":\"manual\"} (started by hand: a scene).\n" +
		"\"only\" (optional) limits the automatic triggers to a time window and days. Conditions (all must hold): {\"kind\":\"value\",\"tile\":ID,\"stat\":…,\"op\":…,\"value\":…} · {\"kind\":\"online\",\"tile\":ID} · {\"kind\":\"offline\",\"tile\":ID}.\n" +
		"Steps, in order: {\"kind\":\"notify\",\"text\":\"message\",\"report\":false,\"camera\":ID of a camera tile or 0} · {\"kind\":\"button\",\"tile\":ID,\"button\":number from the tile's buttons} · " +
		"{\"kind\":\"scene\",\"scene\":\"name\"} · {\"kind\":\"wait\",\"minutes\":N} · {\"kind\":\"if\",\"if\":condition,\"then\":[steps],\"else\":[steps]} (no if inside an if).\n" +
		"Use only the tiles, value labels, buttons and scenes listed below, with their exact IDs, labels and numbers. If something asked for does not exist, leave it out and add a notify step that says what is missing. " +
		"Write names and messages in " + aiLanguage(lang) + ".\n\nTiles:\n" + cat.String()
	if len(sceneNames) > 0 {
		system += "\nScenes: " + strings.Join(sceneNames, ", ") + "\n"
	}
	user := clip(in.Text, 2000)
	if in.Current != nil {
		cur, _ := json.Marshal(in.Current)
		user = "Change this workflow as described, keep the rest:\n" + string(cur) + "\n\nChange: " + user
	}
	ctx, cancel := timeout(r, 3*time.Minute)
	defer cancel()
	text, _, err := aiComplete(ctx, s.aiConfig(), []any{map[string]string{"role": "system", "content": system}, map[string]string{"role": "user", "content": user}}, false, false, nil, lang, false)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	draft, err := parseDraft(text)
	if err != nil {
		slog.Debug("KI-Entwurf nicht lesbar", "antwort", clip(text, 500))
		writeErr(w, 502, errors.New("Die KI hat keinen lesbaren Workflow geliefert – bitte anders beschreiben oder ein größeres Modell nehmen"))
		return
	}
	if in.Current != nil {
		draft.ID, draft.On = in.Current.ID, in.Current.On
	} else {
		draft.On = true
	}
	// Checked like a saved one; a problem is shown in the editor to fix by hand.
	checked := draft
	problem := ""
	if err := s.checkRule(r, &checked); err != nil {
		problem = tr(r, err.Error())
		draft.normalize()
	} else {
		draft = checked
	}
	slog.Info("KI-Entwurf für einen Workflow", "benutzer", u.Username, "problem", problem)
	writeJSON(w, map[string]any{"rule": draft, "problem": problem})
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
