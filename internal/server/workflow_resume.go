package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/heimdeck/heimdeck/internal/i18n"
	"github.com/heimdeck/heimdeck/internal/logging"
)

// Runs that wait – "wait 10 minutes", a question in Telegram, a scene that
// waits – keep a record of where they stand. After a restart (an update, a
// new container) Heimdeck continues them there: a wait that is over goes on
// at once, an open question is asked again. A run stopped on purpose (the
// workflow changed or was deleted, the account locked) loses its record.

const runsKey = "_wfRuns"

// runRecord is where a run stands.
type runRecord struct {
	Rule    string   `json:"rule"`
	Rev     string   `json:"rev"` // the workflow as it was: a changed one is not continued
	Why     string   `json:"why"`
	Entry   string   `json:"entry,omitempty"`
	Mode    int      `json:"mode"`
	At      []int    `json:"at"`   // the step: [i] or [i, branch, j]
	Wake    int64    `json:"wake"` // when to go on (a wait), or when it asked (a question)
	Ask     bool     `json:"ask,omitempty"`
	Results []string `json:"results"`
	OK      bool     `json:"ok"`
	Started int64    `json:"started"`
}

var runsMu sync.Mutex

// ruleRev is a short fingerprint of a workflow's steps and triggers.
func ruleRev(r rule) string {
	b, _ := json.Marshal(struct {
		T []ruleTrigger
		S []ruleAction
	}{r.Triggers, r.Steps})
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:8])
}

func (s *Server) runRecords(user int64) map[string]runRecord {
	settings, _ := s.store.UserSettings(user)
	out := map[string]runRecord{}
	json.Unmarshal([]byte(settings[runsKey]), &out)
	return out
}

func (s *Server) saveRunRecords(user int64, recs map[string]runRecord) {
	b, _ := json.Marshal(recs)
	logErr("Workflow-Stand speichern", s.store.SaveUserSettings(user, map[string]string{runsKey: string(b)}))
}

// persist keeps where the run stands (only for real runs, not scenes inside scenes'
// own records – each run keeps its own).
func (w *wfRun) persist(at []int, wake time.Time, ask bool) {
	if w.mode == runDry {
		return
	}
	rec := runRecord{Rule: w.r.ID, Rev: ruleRev(w.r), Why: w.why, Entry: w.entry, Mode: int(w.mode), At: at, Ask: ask,
		Results: append([]string(nil), w.results...), OK: w.ok, Started: w.started.Unix()}
	if !wake.IsZero() {
		rec.Wake = wake.Unix()
	}
	runsMu.Lock()
	recs := w.s.runRecords(w.user)
	recs[w.r.ID] = rec
	w.s.saveRunRecords(w.user, recs)
	runsMu.Unlock()
	w.persisted = true
}

func (s *Server) hasRunRecord(user int64, id string) bool {
	runsMu.Lock()
	defer runsMu.Unlock()
	_, ok := s.runRecords(user)[id]
	return ok
}

// dropRunRecord forgets where a run stood; id "" forgets all of the user's.
func (s *Server) dropRunRecord(user int64, id string) {
	runsMu.Lock()
	defer runsMu.Unlock()
	recs := s.runRecords(user)
	if len(recs) == 0 {
		return
	}
	if id == "" {
		recs = map[string]runRecord{}
	} else if _, ok := recs[id]; !ok {
		return
	} else {
		delete(recs, id)
	}
	s.saveRunRecords(user, recs)
}

// sendNow sends what the run has to say so far (before it waits).
func (w *wfRun) sendNow() {
	if w.mode == runDry {
		return
	}
	w.flush()
	for _, cam := range w.pictures {
		if err := w.s.sendCameraTelegram(w.ctx, w.user, w.lang, cam); err != nil {
			w.results = append(w.results, "⚠ 📹 "+err.Error())
			w.ok = false
		} else {
			w.results = append(w.results, "📹 "+i18n.T(w.lang, "Kamerabild"))
		}
	}
	w.sends, w.lines, w.pictures = nil, nil, nil
}

// resume goes on where the record says: after a wait (once it is over), at
// a question (asked again), after a scene.
func (w *wfRun) resume(rec *runRecord) {
	if wake := time.Unix(rec.Wake, 0); !rec.Ask && rec.Wake > 0 && time.Now().Before(wake) {
		sleepCtx(w.ctx, time.Until(wake))
		if w.ctx.Err() != nil {
			return
		}
	}
	at := rec.At
	if len(at) == 0 || at[0] < 0 || at[0] >= len(w.r.Steps) {
		return
	}
	next := 1 // after the step that waited
	if rec.Ask {
		next = 0 // the question again – unless it was asked too long ago
		if time.Since(time.Unix(rec.Wake, 0)) > ruleAskTTL {
			next = 1
			w.results = append(w.results, "✖ "+i18n.T(w.lang, "%s: abgelehnt oder keine Antwort", stepLabelAt(w.r, at)))
		}
	}
	top := at[0]
	if len(at) == 3 {
		if_ := w.r.Steps[top]
		branch := if_.Then
		if at[1] == 1 {
			branch = if_.Else
		}
		if j := at[2] + next; j < len(branch) {
			w.steps(branch[j:], 1, stepPlace{top: top, branch: at[1], offset: j})
		}
		if top+1 < len(w.r.Steps) && w.ctx.Err() == nil {
			w.steps(w.r.Steps[top+1:], 0, stepPlace{branch: -1, offset: top + 1})
		}
		return
	}
	if i := top + next; i < len(w.r.Steps) {
		w.steps(w.r.Steps[i:], 0, stepPlace{branch: -1, offset: i})
	}
}

func stepLabelAt(r rule, at []int) string {
	defer func() { recover() }()
	a := r.Steps[at[0]]
	if len(at) == 3 {
		if at[1] == 0 {
			a = a.Then[at[2]]
		} else {
			a = a.Else[at[2]]
		}
	}
	return a.Label
}

// resumeRuns continues, after a start, the runs that were waiting.
func (s *Server) resumeRuns(ctx context.Context) {
	defer logging.Recover("Automationen")
	users, _ := s.store.Users()
	for _, u := range users {
		runsMu.Lock()
		recs := s.runRecords(u.ID)
		runsMu.Unlock()
		if len(recs) == 0 {
			continue
		}
		rules := map[string]rule{}
		for _, r := range s.rulesOf(u.ID) {
			rules[r.ID] = r
		}
		for id, rec := range recs {
			r, ok := rules[id]
			if u.Disabled || !ok || !r.On || rec.Rev != ruleRev(r) || !s.mayAutomate(u.ID) {
				s.dropRunRecord(u.ID, id) // gone, switched off or changed since
				continue
			}
			slog.Info("Workflow wird fortgesetzt", "regel", r.Name, "benutzer", u.Username, "schritt", strings.Trim(strings.Join(strings.Fields(strings.Trim(jsonInts(rec.At), "[]")), ""), " "))
			rec := rec
			go s.runRuleFrom(ctx, u.ID, r, rec.Why, runMode(rec.Mode), &rec)
		}
	}
}

func jsonInts(v []int) string {
	b, _ := json.Marshal(v)
	return string(b)
}
