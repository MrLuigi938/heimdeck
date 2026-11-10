package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heimdeck/heimdeck/internal/camera"
	"github.com/heimdeck/heimdeck/internal/i18n"
	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/logging"
	"github.com/heimdeck/heimdeck/internal/store"
)

// Workflows (automations): "when one of these happens – and these hold –,
// do these steps". Heimdeck checks them every half minute and runs them with
// the rights of the person who made them; the AI may draft one, but never
// runs one. A trigger fires when it becomes true, and again only after it
// was false in between and the workflow's waiting time has passed. A button
// that asks before it runs on the dashboard asks here too – with a Telegram
// button – unless the workflow says to run it without asking; the workflow
// waits for the answer.
//
// Before workflows a rule had one trigger and a list of actions; those are
// read as a workflow with one trigger and the actions as its steps.

const (
	rulesKey    = "_rules"
	ruleLogKey  = "_ruleLog"
	maxRules    = 50
	maxTriggers = 5
	maxConds    = 5
	maxSteps    = 20
	maxRuleLog  = 40
	maxWait     = 24 * 60 // minutes
	ruleTick    = 30 * time.Second
	ruleAskTTL  = time.Hour
)

type rule struct {
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	On       bool          `json:"on"`
	Triggers []ruleTrigger `json:"triggers"`
	Only     ruleOnly      `json:"only"`
	Conds    []ruleCond    `json:"conds"`
	Steps    []ruleAction  `json:"steps"`
	NoAsk    bool          `json:"noAsk"`    // run buttons that normally ask without asking
	Cooldown int           `json:"cooldown"` // minutes before the workflow fires again
	// Before workflows (read, then moved into Triggers and Steps):
	Trigger *ruleTrigger `json:"trigger,omitempty"`
	Actions []ruleAction `json:"actions,omitempty"`
}

type ruleTrigger struct {
	Kind  string `json:"kind"`               // time | down | up | value | camera | webhook | manual
	Name  string `json:"tileName,omitempty"` // the tile's name, for the list
	Time  string `json:"time,omitempty"`     // HH:MM
	Days  []int  `json:"days,omitempty"`     // 0 = Sunday … 6 = Saturday; none = every day
	Tile  int64  `json:"tile,omitempty"`
	For   int    `json:"for,omitempty"`   // minutes a service has to be down
	Stat  string `json:"stat,omitempty"`  // the tile's value, by its label
	Op    string `json:"op,omitempty"`    // > or <
	Value string `json:"value,omitempty"` // e.g. "90 %" or "100 GB"
	Event string `json:"event,omitempty"` // camera: the kind of event ("Person", "Klingel" …); empty = any
	Match string `json:"match,omitempty"` // webhook: only messages containing this text
}

// ruleOnly limits a workflow to a time window (which may pass midnight) and days.
type ruleOnly struct {
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	Days []int  `json:"days,omitempty"`
}

// ruleCond is a condition on another tile: a value, or whether it answers.
type ruleCond struct {
	Kind  string `json:"kind"` // value | online | offline
	Tile  int64  `json:"tile"`
	Name  string `json:"tileName,omitempty"`
	Stat  string `json:"stat,omitempty"`
	Op    string `json:"op,omitempty"`
	Value string `json:"value,omitempty"`
}

// ruleAction is one step of a workflow.
type ruleAction struct {
	Kind    string            `json:"kind"` // button | notify | scene | wait | if
	Tile    int64             `json:"tile,omitempty"`
	Action  string            `json:"action,omitempty"`
	Params  map[string]string `json:"params,omitempty"`
	Label   string            `json:"label,omitempty"`   // "Pause · SABnzbd", for the list and messages
	Confirm string            `json:"confirm,omitempty"` // the button asks before it runs
	Text    string            `json:"text,omitempty"`
	Report  bool              `json:"report,omitempty"`  // add the situation report
	Camera  int64             `json:"camera,omitempty"`  // send this camera's picture along (Telegram)
	Scene   string            `json:"scene,omitempty"`   // the scene to start
	Minutes int               `json:"minutes,omitempty"` // wait
	If      *ruleCond         `json:"if,omitempty"`
	Then    []ruleAction      `json:"then,omitempty"`
	Else    []ruleAction      `json:"else,omitempty"`
	Button  *int              `json:"button,omitempty"` // from an AI draft: the tile's button by number
}

type ruleLogEntry struct {
	At     int64  `json:"at"`
	Rule   string `json:"rule"`
	Name   string `json:"name"`
	Why    string `json:"why"`
	Result string `json:"result"`
	OK     bool   `json:"ok"`
}

// ruleEngine is the state between checks.
type ruleEngine struct {
	mu      sync.Mutex
	state   map[string]*ruleState // by user:workflow and user:workflow:trigger
	pending map[string]*ruleAsk
	running map[string]context.CancelFunc // user:workflow while it runs (it may wait); stops it
	last    time.Time                     // the last check round
	since   time.Time                     // the round before: time triggers due since then fire
	seq     int
}

type ruleState struct {
	armed    bool // fires when the trigger becomes true
	since    time.Time
	wasDown  bool
	lastFire time.Time
	minute   string // the last minute a time trigger fired
	value    string // the value seen last, for the list
	event    int64  // the newest camera event seen
	started  bool   // the camera's events were looked at once
}

// ruleAsk waits for a tap on ✅ in Telegram.
type ruleAsk struct {
	user   int64
	at     time.Time
	answer chan bool
}

var (
	rulesMu   sync.Mutex
	hhmmRe    = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
	numUnitRe = regexp.MustCompile(`(-?[0-9][0-9.,]*)\s*([A-Za-zµ%°]*)`)
)

// normalize moves a rule from before workflows into the workflow fields.
func (r *rule) normalize() {
	if r.Trigger != nil {
		if len(r.Triggers) == 0 {
			r.Triggers = []ruleTrigger{*r.Trigger}
		}
		r.Trigger = nil
	}
	if len(r.Actions) > 0 {
		if len(r.Steps) == 0 {
			r.Steps = r.Actions
		}
		r.Actions = nil
	}
	if r.Triggers == nil {
		r.Triggers = []ruleTrigger{}
	}
	if r.Conds == nil {
		r.Conds = []ruleCond{}
	}
	if r.Steps == nil {
		r.Steps = []ruleAction{}
	}
}

// manual says whether the workflow can be started by hand (a scene).
func (r rule) manual() bool {
	return slices.ContainsFunc(r.Triggers, func(t ruleTrigger) bool { return t.Kind == "manual" })
}

// walkSteps calls fn for every step, also those inside "if".
func walkSteps(steps []ruleAction, fn func(a ruleAction)) {
	for _, a := range steps {
		fn(a)
		walkSteps(a.Then, fn)
		walkSteps(a.Else, fn)
	}
}

func (s *Server) rulesOf(user int64) []rule {
	settings, _ := s.store.UserSettings(user)
	var rules []rule
	json.Unmarshal([]byte(settings[rulesKey]), &rules)
	for i := range rules {
		rules[i].normalize()
	}
	return rules
}

func (s *Server) saveRules(user int64, rules []rule) error {
	b, _ := json.Marshal(rules)
	return s.store.SaveUserSettings(user, map[string]string{rulesKey: string(b)})
}

func (s *Server) ruleLog(user int64) []ruleLogEntry {
	settings, _ := s.store.UserSettings(user)
	var log []ruleLogEntry
	json.Unmarshal([]byte(settings[ruleLogKey]), &log)
	return log
}

func (s *Server) addRuleLog(user int64, e ruleLogEntry) {
	rulesMu.Lock()
	defer rulesMu.Unlock()
	log := append([]ruleLogEntry{e}, s.ruleLog(user)...)
	if len(log) > maxRuleLog {
		log = log[:maxRuleLog]
	}
	b, _ := json.Marshal(log)
	logErr("Automationen protokollieren", s.store.SaveUserSettings(user, map[string]string{ruleLogKey: string(b)}))
}

// ---- checking ----

// Automations checks the workflows of all users every half minute.
func (s *Server) Automations(ctx context.Context) {
	defer logging.Recover("Automationen")
	tick := time.NewTicker(ruleTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-tick.C:
			s.checkRules(ctx, now)
		}
	}
}

func (s *Server) checkRules(ctx context.Context, now time.Time) {
	defer logging.Recover("Automationen")
	s.rules.mu.Lock()
	s.rules.since, s.rules.last = s.rules.last, now
	s.rules.mu.Unlock()
	users, _ := s.store.Users()
	for _, u := range users {
		if u.Disabled {
			continue
		}
		for _, r := range s.rulesOf(u.ID) {
			if !r.On {
				continue
			}
			if why, fire := s.ruleDue(u.ID, r, now); fire {
				slog.Info("Automation ausgelöst", "regel", r.Name, "benutzer", u.Username, "grund", why)
				go s.runRule(ctx, u.ID, r, why, runAuto)
			}
		}
	}
}

func dayOK(days []int, now time.Time) bool {
	return len(days) == 0 || slices.Contains(days, int(now.Weekday()))
}

func minutesOf(hhmm string) int {
	return int(hhmm[0]-'0')*600 + int(hhmm[1]-'0')*60 + int(hhmm[3]-'0')*10 + int(hhmm[4]-'0')
}

// ok says whether the workflow may run now.
func (o ruleOnly) ok(now time.Time) bool {
	if !dayOK(o.Days, now) {
		return false
	}
	if o.From == "" || o.To == "" {
		return true
	}
	m, from, to := now.Hour()*60+now.Minute(), minutesOf(o.From), minutesOf(o.To)
	if from <= to {
		return m >= from && m < to
	}
	return m >= from || m < to // over midnight
}

func (r rule) cooldown() time.Duration {
	if r.Cooldown <= 0 {
		return time.Hour
	}
	return time.Duration(r.Cooldown) * time.Minute
}

func stateKey(user int64, parts ...string) string {
	return strconv.FormatInt(user, 10) + ":" + strings.Join(parts, ":")
}

// ruleState: call with s.rules.mu held.
func (s *Server) ruleState(key string) *ruleState {
	st := s.rules.state[key]
	if st == nil {
		st = &ruleState{armed: true}
		s.rules.state[key] = st
	}
	return st
}

// ruleDue looks at the workflow's triggers and says whether it fires now, and why.
func (s *Server) ruleDue(user int64, r rule, now time.Time) (string, bool) {
	lang := s.userLang(user)
	wfKey := stateKey(user, r.ID)
	for i, tr := range r.Triggers {
		key := stateKey(user, r.ID, strconv.Itoa(i))
		cond, why, ok := s.triggerHolds(user, key, wfKey, tr, now, lang)
		if !ok {
			continue
		}
		var at time.Time
		if tr.Kind == "time" {
			at, _ = s.timeDue(tr, now) // before taking the lock, timeDue takes it too
		}
		s.rules.mu.Lock()
		st, wf := s.ruleState(key), s.ruleState(wfKey)
		if tr.Kind == "time" {
			// Once a day at that time, whatever else says.
			minute := at.Format("2006-01-02 15:04")
			done := st.minute == minute
			st.minute = minute
			s.rules.mu.Unlock()
			if done || !r.Only.ok(now) || !s.condsHold(user, lang, r.Conds) {
				continue
			}
			s.rules.mu.Lock()
			wf.lastFire = now
			s.rules.mu.Unlock()
			return why, true
		}
		if !cond {
			st.armed = true
			s.rules.mu.Unlock()
			continue
		}
		wait := !wf.lastFire.IsZero() && now.Sub(wf.lastFire) < r.cooldown()
		armed := st.armed
		s.rules.mu.Unlock()
		// While the conditions do not hold, the trigger stays armed: the
		// workflow runs as soon as they do (still above 90 % – and now nobody streams).
		if !armed || wait || !r.Only.ok(now) || !s.condsHold(user, lang, r.Conds) {
			continue
		}
		s.rules.mu.Lock()
		st.armed, wf.lastFire = false, now
		s.rules.mu.Unlock()
		return why, true
	}
	return "", false
}

// triggerHolds says whether one trigger is true now (ok: it could be looked at).
func (s *Server) triggerHolds(user int64, key, wfKey string, tr ruleTrigger, now time.Time, lang string) (bool, string, bool) {
	switch tr.Kind {
	case "time":
		if _, due := s.timeDue(tr, now); !due {
			return false, "", false
		}
		return true, i18n.T(lang, "Es ist %s", tr.Time), true
	case "down", "up":
		t, err := s.store.Tile(tr.Tile)
		if err != nil || !s.ruleTileOK(user, t.ID) {
			return false, "", false
		}
		s.mu.Lock()
		ts, checked := s.latest[t.ID]
		s.mu.Unlock()
		if !checked {
			return false, "", false
		}
		s.rules.mu.Lock()
		defer s.rules.mu.Unlock()
		st := s.ruleState(key)
		if ts.Online {
			st.since = time.Time{}
		} else if st.since.IsZero() {
			st.since = now
		}
		down := !ts.Online && now.Sub(st.since) >= time.Duration(max(tr.For, 1))*time.Minute
		if tr.Kind == "down" {
			return down, i18n.T(lang, "%s antwortet seit %d Min. nicht", t.Name, int(now.Sub(st.since).Minutes())), true
		}
		if down {
			st.wasDown = true
		}
		up := ts.Online && st.wasDown
		if up {
			st.wasDown = false
		}
		return up, i18n.T(lang, "%s ist wieder erreichbar", t.Name), true
	case "value":
		holds, why, value, ok := s.valueHolds(user, tr.Tile, tr.Stat, tr.Op, tr.Value, lang)
		if ok {
			s.rules.mu.Lock()
			s.ruleState(wfKey).value = value
			s.rules.mu.Unlock()
		}
		return holds, why, ok
	case "camera":
		t, err := s.store.Tile(tr.Tile)
		if err != nil || !s.ruleTileOK(user, t.ID) {
			return false, "", false
		}
		events := camera.Events(t.ID)
		s.rules.mu.Lock()
		st := s.ruleState(key)
		last := st.event
		for _, e := range events {
			st.event = max(st.event, e.ID) // by number: several events may share a second
		}
		first := !st.started
		st.started = true
		s.rules.mu.Unlock()
		if first {
			return false, "", true // what happened before the workflow ran does not count
		}
		for _, e := range events {
			if e.ID > last && (tr.Event == "" || e.Kind == tr.Event) {
				return true, t.Name + ": " + i18n.T(lang, e.Kind), true
			}
		}
		return false, "", true
	}
	return false, "", false // manual: by hand; webhook: on arrival (hookRules)
}

// ruleTileOK says whether the user's workflows may still look at a tile: it is
// on one of their dashboards or shared with them, outside locked and hidden
// groups. Checked on every run – a share can end, a group can be locked after
// the workflow was saved.
func (s *Server) ruleTileOK(user, tile int64) bool {
	tiles, _ := s.aiTiles(s.botRequest(context.Background(), user, i18n.DE))
	return slices.ContainsFunc(tiles, func(t store.Tile) bool { return t.ID == tile })
}

// timeDue says whether a time trigger's time passed since the last check
// round (not just: whether it is that minute now – a slow round must not let
// it slip by), and which time that was.
func (s *Server) timeDue(tr ruleTrigger, now time.Time) (time.Time, bool) {
	if !hhmmRe.MatchString(tr.Time) {
		return time.Time{}, false
	}
	m := minutesOf(tr.Time)
	at := time.Date(now.Year(), now.Month(), now.Day(), m/60, m%60, 0, 0, now.Location())
	if at.After(now) {
		at = at.AddDate(0, 0, -1) // just after midnight: yesterday's time
	}
	s.rules.mu.Lock()
	from := s.rules.since
	s.rules.mu.Unlock()
	if from.IsZero() || !from.Before(now) || now.Sub(from) > 15*time.Minute {
		from = now.Add(-time.Minute)
	}
	return at, at.After(from) && dayOK(tr.Days, at)
}

// valueHolds compares a tile's value with a limit.
func (s *Server) valueHolds(user, tile int64, stat, op, limit, lang string) (holds bool, why, value string, ok bool) {
	t, err := s.store.Tile(tile)
	if err != nil || !s.ruleTileOK(user, tile) {
		return
	}
	w, pending, err := s.widget(t, lang, 8*time.Second)
	if pending || err != nil || w == nil {
		return
	}
	w = hideParts(w, t.Hide, lang) // a value the owner hid is not there
	for _, st := range w.Stats {
		if st.Label == stat {
			value, ok = st.Value, true
			break
		}
	}
	if !ok {
		return
	}
	if holds, ok = compareValue(value, op, limit, lang); !ok {
		return
	}
	return holds, fmt.Sprintf("%s · %s: %s %s %s", t.Name, stat, value, op, limit), value, true
}

// condHolds says whether a condition holds now, and how it looks.
func (s *Server) condHolds(user int64, lang string, c ruleCond) (bool, string) {
	switch c.Kind {
	case "value":
		holds, why, _, ok := s.valueHolds(user, c.Tile, c.Stat, c.Op, c.Value, lang)
		if !ok {
			return false, i18n.T(lang, "%s: kein Wert", firstNonEmptyStr(c.Name, "?"))
		}
		return holds, why
	case "online", "offline":
		name := firstNonEmptyStr(c.Name, "?")
		if !s.ruleTileOK(user, c.Tile) {
			return false, i18n.T(lang, "%s: nicht mehr erlaubt", name)
		}
		s.mu.Lock()
		ts, checked := s.latest[c.Tile]
		s.mu.Unlock()
		if !checked {
			return false, i18n.T(lang, "%s: noch nicht geprüft", name)
		}
		if ts.Online {
			return c.Kind == "online", i18n.T(lang, "%s ist erreichbar", name)
		}
		return c.Kind == "offline", i18n.T(lang, "%s ist nicht erreichbar", name)
	}
	return false, ""
}

func (s *Server) condsHold(user int64, lang string, conds []ruleCond) bool {
	for _, c := range conds {
		if ok, _ := s.condHolds(user, lang, c); !ok {
			return false
		}
	}
	return true
}

// quantity is a number from a tile's value; bytes are counted in bytes.
type quantity struct {
	v, raw float64
	bytes  bool
}

var byteUnits = map[string]float64{"b": 1, "kb": 1e3, "kib": 1 << 10, "mb": 1e6, "mib": 1 << 20, "gb": 1e9, "gib": 1 << 30, "tb": 1e12, "tib": 1 << 40, "pb": 1e15}

// parseQuantity reads "1.8 TB", "1,8 TB", "92 %", "11.250" … The language
// decides only when a separator could be either (one separator, three digits).
func parseQuantity(s, lang string) (quantity, bool) {
	m := numUnitRe.FindStringSubmatch(s)
	if m == nil {
		return quantity{}, false
	}
	num := strings.TrimRight(m[1], ".,")
	dots, commas := strings.Count(num, "."), strings.Count(num, ",")
	switch {
	case dots > 0 && commas > 0: // the last one is the decimal point
		if strings.LastIndex(num, ".") > strings.LastIndex(num, ",") {
			num = strings.ReplaceAll(num, ",", "")
		} else {
			num = strings.ReplaceAll(strings.ReplaceAll(num, ".", ""), ",", ".")
		}
	case dots+commas == 1:
		sep := "."
		if commas == 1 {
			sep = ","
		}
		after := len(num) - strings.LastIndex(num, sep) - 1
		thousands := after == 3 && ((sep == "." && lang == i18n.DE) || (sep == "," && lang != i18n.DE))
		if thousands {
			num = strings.ReplaceAll(num, sep, "")
		} else {
			num = strings.ReplaceAll(num, ",", ".")
		}
	case dots > 1:
		num = strings.ReplaceAll(num, ".", "")
	case commas > 1:
		num = strings.ReplaceAll(num, ",", "")
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return quantity{}, false
	}
	q := quantity{v: v, raw: v}
	if f, ok := byteUnits[strings.ToLower(m[2])]; ok {
		q.v, q.bytes = v*f, true
	}
	return q, true
}

// compareValue says whether value op threshold holds. With a byte unit on
// both sides the sizes are compared (900 GB < 1 TB); otherwise the numbers.
func compareValue(value, op, threshold, lang string) (bool, bool) {
	got, ok1 := parseQuantity(value, lang)
	want, ok2 := parseQuantity(threshold, lang)
	if !ok1 || !ok2 {
		return false, false
	}
	a, b := got.raw, want.raw
	if got.bytes && want.bytes {
		a, b = got.v, want.v
	}
	switch op {
	case "<":
		return a < b, true
	case "=":
		return a == b, true
	}
	return a > b, true
}

// ---- running ----

type runMode int

const (
	runAuto   runMode = iota // by a trigger
	runByHand                // started by a person: that is the confirmation of its buttons
	runDry                   // a dry run: says what would happen, does nothing
)

// sceneDepthKey counts scenes started by scenes, against endless loops.
type sceneDepthKey struct{}

// wfRun is one run of a workflow.
type wfRun struct {
	s        *Server
	ctx      context.Context
	user     int64
	lang     string
	r        rule
	why      string
	mode     runMode
	lines    []string // for the notification
	results  []string // for the log (or the dry run)
	pictures []int64
	ok       bool
}

// runRule runs the workflow's steps and notes the run in the log.
func (s *Server) runRule(ctx context.Context, user int64, r rule, why string, mode runMode) ruleLogEntry {
	defer logging.Recover("Automationen")
	lang := s.userLang(user)
	if mode == runByHand {
		why = i18n.T(lang, "Von Hand gestartet")
	}
	key := stateKey(user, r.ID)
	if mode != runDry {
		var stop context.CancelFunc
		ctx, stop = context.WithCancel(ctx)
		defer stop()
		s.rules.mu.Lock()
		_, busy := s.rules.running[key]
		if !busy {
			s.rules.running[key] = stop
		}
		s.rules.mu.Unlock()
		if busy {
			if depth, _ := ctx.Value(sceneDepthKey{}).(int); depth > 0 {
				// Started by a scene it started itself: a loop.
				return ruleLogEntry{At: time.Now().Unix(), Rule: r.ID, Name: r.Name, Why: why, Result: "⚠ " + i18n.T(lang, "Szenen starten sich hier zu tief ineinander – abgebrochen"), OK: false}
			}
			return ruleLogEntry{At: time.Now().Unix(), Rule: r.ID, Name: r.Name, Why: why, Result: i18n.T(lang, "läuft schon – dieser Start wurde ausgelassen"), OK: true}
		}
		defer func() {
			s.rules.mu.Lock()
			delete(s.rules.running, key)
			s.rules.mu.Unlock()
		}()
	}
	run := &wfRun{s: s, ctx: ctx, user: user, lang: lang, r: r, why: why, mode: mode, ok: true}
	run.steps(r.Steps, 0)
	if mode != runDry {
		if len(run.lines) > 0 {
			s.send(user, s.notifyPrefs(user).settings, []notification{{Rule: true, Title: r.Name, Body: strings.Join(append([]string{why}, run.lines...), "\n\n")}})
		}
		for _, cam := range run.pictures {
			if err := s.sendCameraTelegram(ctx, user, lang, cam); err != nil {
				run.fail("📹 " + err.Error())
			} else {
				run.results = append(run.results, "📹 "+i18n.T(lang, "Kamerabild"))
			}
		}
	}
	sep := " · "
	if mode == runDry {
		sep = "\n"
	}
	e := ruleLogEntry{At: time.Now().Unix(), Rule: r.ID, Name: r.Name, Why: why, Result: strings.Join(run.results, sep), OK: run.ok}
	if mode != runDry {
		s.addRuleLog(user, e)
	}
	return e
}

func (w *wfRun) fail(msg string) {
	w.ok = false
	w.results = append(w.results, "⚠ "+msg)
	w.lines = append(w.lines, "⚠ "+msg)
}

func (w *wfRun) steps(list []ruleAction, depth int) {
	s, lang := w.s, w.lang
	dry := w.mode == runDry
	for _, a := range list {
		if w.ctx.Err() != nil {
			w.fail(i18n.T(lang, "abgebrochen"))
			return
		}
		switch a.Kind {
		case "notify":
			text := strings.TrimSpace(a.Text)
			if dry {
				w.results = append(w.results, "✉ "+i18n.T(lang, "würde senden: %s", firstNonEmptyStr(text, i18n.T(lang, "Lagebericht"))))
				continue
			}
			if a.Report {
				rctx, cancel := context.WithTimeout(w.ctx, tgAnswerTime)
				rep, err := s.statusReport(rctx, w.user, lang)
				cancel()
				if err != nil {
					rep = "⚠ " + err.Error()
				}
				text = strings.TrimSpace(text + "\n\n" + rep)
			}
			if text != "" {
				w.lines = append(w.lines, text)
			}
			w.results = append(w.results, "✉ "+i18n.T(lang, "Nachricht"))
			if a.Camera != 0 {
				w.pictures = append(w.pictures, a.Camera)
			}
		case "scene":
			scene, found := s.sceneOf(w.user, a.Scene)
			sceneDepth, _ := w.ctx.Value(sceneDepthKey{}).(int)
			switch {
			case !found:
				w.fail(i18n.T(lang, "Die Szene gibt es nicht mehr"))
			case !scene.On:
				w.fail(i18n.T(lang, "Die Szene ist ausgeschaltet"))
			case sceneDepth >= 3 || scene.ID == w.r.ID:
				w.fail(i18n.T(lang, "Szenen starten sich hier zu tief ineinander – abgebrochen"))
			case dry:
				w.results = append(w.results, "🎬 "+i18n.T(lang, "würde starten: %s", scene.Name))
			default:
				mode := w.mode
				e := s.runRule(context.WithValue(w.ctx, sceneDepthKey{}, sceneDepth+1), w.user, scene, i18n.T(lang, "Gestartet von „%s“", w.r.Name), mode)
				w.results = append(w.results, "🎬 "+scene.Name)
				if !e.OK {
					w.ok = false
				}
			}
		case "button":
			ask := a.Confirm != "" && !w.r.NoAsk && w.mode == runAuto
			if dry {
				line := "▶ " + i18n.T(lang, "würde drücken: %s", a.Label)
				if a.Confirm != "" && !w.r.NoAsk {
					line += " (" + i18n.T(lang, "fragt vorher in Telegram") + ")"
				}
				w.results = append(w.results, line)
				continue
			}
			if ask {
				yes, err := s.askAndWait(w.ctx, w.user, w.r, w.why, a, lang)
				switch {
				case err != nil:
					w.fail(a.Label + ": " + err.Error())
					continue
				case !yes:
					w.results = append(w.results, "✖ "+i18n.T(lang, "%s: abgelehnt oder keine Antwort", a.Label))
					continue
				}
			}
			if err := s.botAction(w.ctx, w.user, lang, proposal{TileID: a.Tile, Action: a.Action, Params: a.Params, Label: a.Label}); err != nil {
				w.fail(a.Label + ": " + err.Error())
				if ask {
					s.tgNotice(w.ctx, w.user, "⚠ "+a.Label+": "+err.Error())
				}
			} else {
				w.results = append(w.results, "✅ "+a.Label)
				if ask {
					s.tgNotice(w.ctx, w.user, "✅ "+a.Label)
				}
			}
		case "wait":
			m := min(max(a.Minutes, 1), maxWait)
			if dry {
				w.results = append(w.results, "⏳ "+i18n.T(lang, "würde %d Min. warten", m))
				continue
			}
			sleepCtx(w.ctx, time.Duration(m)*time.Minute)
			w.results = append(w.results, "⏳ "+i18n.T(lang, "%d Min. gewartet", m))
		case "if":
			if a.If == nil || depth >= 2 {
				continue
			}
			holds, desc := s.condHolds(w.user, lang, *a.If)
			answer := i18n.T(lang, "nein")
			if holds {
				answer = i18n.T(lang, "ja")
			}
			w.results = append(w.results, "❓ "+desc+" → "+answer)
			if holds {
				w.steps(a.Then, depth+1)
			} else {
				w.steps(a.Else, depth+1)
			}
		}
	}
}

// stopRuns stops the runs of a workflow (key user:id) or, with a key ending
// in ":", all runs of a user – the workflow was changed or deleted, the
// account locked.
func (s *Server) stopRuns(key string) {
	s.rules.mu.Lock()
	defer s.rules.mu.Unlock()
	for k, stop := range s.rules.running {
		if k == key || strings.HasSuffix(key, ":") && strings.HasPrefix(k, key) {
			stop()
		}
	}
}

// hasWait says whether a run may take a while.
func hasWait(steps []ruleAction) bool {
	wait := false
	walkSteps(steps, func(a ruleAction) { wait = wait || a.Kind == "wait" || a.Kind == "scene" })
	return wait
}

// askAndWait asks in Telegram whether to press a button and waits for the answer.
func (s *Server) askAndWait(ctx context.Context, user int64, r rule, why string, a ruleAction, lang string) (bool, error) {
	chat := strings.TrimSpace(s.settingsFor(user)["telegramChat"])
	token := s.notifySecrets(user).TelegramToken
	if chat == "" || token == "" {
		return false, errors.New(i18n.T(lang, "braucht eine Bestätigung – dafür Telegram einrichten oder bei der Regel „ohne Nachfrage“ wählen"))
	}
	if _, running := s.botChats(token); !running {
		return false, errors.New(i18n.T(lang, "braucht eine Bestätigung über Telegram, aber der Bot läuft gerade nicht"))
	}
	ask := &ruleAsk{user: user, at: time.Now(), answer: make(chan bool, 1)}
	s.rules.mu.Lock()
	s.rules.seq++
	id := strconv.Itoa(s.rules.seq)
	s.rules.pending[id] = ask
	for k, p := range s.rules.pending {
		if time.Since(p.at) > ruleAskTTL {
			delete(s.rules.pending, k)
		}
	}
	s.rules.mu.Unlock()
	defer func() {
		s.rules.mu.Lock()
		delete(s.rules.pending, id)
		s.rules.mu.Unlock()
	}()
	text := "⚙️ " + i18n.T(lang, "Regel „%s“", r.Name) + "\n" + why + "\n\n" + i18n.T(lang, "Soll ich das ausführen?") + "\n• " + a.Label
	body := map[string]any{"chat_id": chat, "text": text, "reply_markup": map[string]any{"inline_keyboard": [][]map[string]string{{
		{"text": "✅ " + i18n.T(lang, "Ausführen"), "callback_data": "r:" + id},
		{"text": i18n.T(lang, "Abbrechen"), "callback_data": "x:" + id},
	}}}}
	if err := tgCall(ctx, token, "sendMessage", body, nil); err != nil {
		return false, errors.New(logging.Redact(err.Error()))
	}
	timer := time.NewTimer(ruleAskTTL)
	defer timer.Stop()
	select {
	case yes := <-ask.answer:
		return yes, nil
	case <-timer.C:
		return false, nil
	case <-ctx.Done():
		return false, ctx.Err()
	}
}

// answerRule hands a tap in Telegram to the workflow waiting for it.
func (s *Server) answerRule(ctx context.Context, user int64, id string, yes bool, lang string) string {
	s.rules.mu.Lock()
	ask := s.rules.pending[id]
	if ask == nil || ask.user != user || time.Since(ask.at) > ruleAskTTL {
		s.rules.mu.Unlock()
		return i18n.T(lang, "Diese Aktion ist abgelaufen – frag bitte nochmal.")
	}
	delete(s.rules.pending, id)
	s.rules.mu.Unlock()
	ask.answer <- yes
	if !yes {
		return i18n.T(lang, "Abgebrochen")
	}
	return "⏳ " + i18n.T(lang, "Wird ausgeführt …")
}

// tgNotice sends a short note to the user's Telegram chat (after a confirmation).
func (s *Server) tgNotice(ctx context.Context, user int64, text string) {
	chat := strings.TrimSpace(s.settingsFor(user)["telegramChat"])
	token := s.notifySecrets(user).TelegramToken
	if chat == "" || token == "" {
		return
	}
	if err := tgCall(ctx, token, "sendMessage", map[string]any{"chat_id": chat, "text": text}, nil); err != nil {
		slog.Warn("Telegram-Hinweis nicht gesendet", "fehler", logging.Redact(err.Error()))
	}
}

// rulesAsk says whether a user has workflows that may ask in Telegram, or
// scenes to start with /szene – then the bot has to listen even without the AI.
func (s *Server) rulesAsk(user int64) bool {
	for _, r := range s.rulesOf(user) {
		if !r.On {
			continue
		}
		if r.manual() {
			return true
		}
		asks := false
		walkSteps(r.Steps, func(a ruleAction) { asks = asks || a.Kind == "button" && a.Confirm != "" })
		if asks && !r.NoAsk {
			return true
		}
	}
	return false
}

// ---- scenes ----

// sceneOf finds one of the user's scenes (workflows started by hand) by ID or name.
func (s *Server) sceneOf(user int64, idOrName string) (rule, bool) {
	q := strings.TrimSpace(idOrName)
	for _, r := range s.rulesOf(user) {
		if r.manual() && (r.ID == q || strings.EqualFold(r.Name, q)) {
			return r, true
		}
	}
	return rule{}, false
}

// scenes are the user's scenes that are on, matching a name if one is given.
func (s *Server) scenes(user int64, name string) []rule {
	q := strings.ToLower(strings.TrimSpace(name))
	var out, exact []rule
	for _, r := range s.rulesOf(user) {
		if !r.manual() || !r.On {
			continue
		}
		n := strings.ToLower(r.Name)
		if q == "" || strings.Contains(n, q) {
			out = append(out, r)
		}
		if q != "" && n == q {
			exact = append(exact, r)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return out
}

// startScene runs a scene a person started (Telegram, the scene tile): their
// start counts as the confirmation of its buttons. A scene that waits runs on
// in the background.
func (s *Server) startScene(ctx context.Context, user int64, r rule, how string) ruleLogEntry {
	slog.Info("Szene gestartet", "szene", r.Name, "benutzer", s.usernameOf(user), "über", how)
	if hasWait(r.Steps) {
		go s.runRule(context.WithoutCancel(ctx), user, r, "", runByHand)
		lang := s.userLang(user)
		return ruleLogEntry{Rule: r.ID, Name: r.Name, Result: i18n.T(lang, "Gestartet – das Protokoll zeigt, was passiert"), OK: true}
	}
	return s.runRule(ctx, user, r, "", runByHand)
}

// hookRules runs the workflows waiting for a message on this webhook tile.
func (s *Server) hookRules(tileID int64, m integrations.HookMessage) {
	defer logging.Recover("Automationen")
	users, _ := s.store.Users()
	now := time.Now()
	text := strings.ToLower(m.Title + " " + m.Text + " " + m.Value)
	for _, u := range users {
		if u.Disabled {
			continue
		}
		lang := s.userLang(u.ID)
		for _, r := range s.rulesOf(u.ID) {
			if !r.On || !r.Only.ok(now) {
				continue
			}
			i := slices.IndexFunc(r.Triggers, func(tr ruleTrigger) bool {
				return tr.Kind == "webhook" && tr.Tile == tileID && strings.Contains(text, strings.ToLower(tr.Match))
			})
			if i < 0 || !s.ruleTileOK(u.ID, tileID) || !s.condsHold(u.ID, lang, r.Conds) {
				continue
			}
			s.rules.mu.Lock()
			wf := s.ruleState(stateKey(u.ID, r.ID))
			wait := !wf.lastFire.IsZero() && now.Sub(wf.lastFire) < r.cooldown()
			if !wait {
				wf.lastFire = now
			}
			s.rules.mu.Unlock()
			if wait {
				continue
			}
			why := "📨 " + r.Triggers[i].Name + ": " + strings.TrimSpace(firstNonEmptyStr(m.Title, m.Text, m.Value))
			slog.Info("Automation ausgelöst", "regel", r.Name, "benutzer", u.Username, "grund", why)
			go s.runRule(context.Background(), u.ID, r, why, runAuto)
		}
	}
}

// sendCameraTelegram sends a camera's current picture to the user's Telegram chat.
func (s *Server) sendCameraTelegram(ctx context.Context, user int64, lang string, cam int64) error {
	chat := strings.TrimSpace(s.settingsFor(user)["telegramChat"])
	token := s.notifySecrets(user).TelegramToken
	if chat == "" || token == "" {
		return errors.New(i18n.T(lang, "Kamerabilder gehen nur über Telegram – dort einrichten"))
	}
	pic, err := s.cameraPicture(ctx, user, lang, cam)
	if err != nil {
		return err
	}
	caption := "📹 " + pic.name + " · " + pic.taken.In(time.Local).Format("15:04:05")
	method, field, file := "sendPhoto", "photo", "camera.jpg"
	if pic.ct == "video/mp4" {
		method, field, file = "sendAnimation", "animation", "camera.mp4"
	}
	if err := tgUpload(ctx, token, method, map[string]string{"chat_id": chat, "caption": caption}, field, file, pic.ct, pic.data); err != nil {
		slog.Warn("Kamerabild für eine Automation nicht gesendet", "fehler", logging.Redact(err.Error()))
		return errors.New(i18n.T(lang, "Das Kamerabild ließ sich nicht an Telegram schicken."))
	}
	return nil
}
