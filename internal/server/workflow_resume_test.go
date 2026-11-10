package server

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/store"
)

// A run that waits survives a restart: where it stands is kept, and after the
// start it goes on there. One stopped on purpose, or changed meanwhile, does not.
func TestRunsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	box, _ := secret.Open(dir)
	st, err := store.Open(filepath.Join(dir, "test.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	app := New(st, auth.New(st, false), nil, nil)
	srv := httptest.NewServer(app.Handler())
	t.Cleanup(srv.Close)
	luis := newTestClient(t, srv.URL)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "en"})
	users, _ := st.Users()
	user := users[0].ID

	_, saved := luis.call("POST", "/api/rules", map[string]any{"name": "Night", "on": true, "triggers": []any{map[string]any{"kind": "manual"}},
		"steps": []any{map[string]any{"kind": "notify", "text": "first"}, map[string]any{"kind": "wait", "minutes": 30}, map[string]any{"kind": "notify", "text": "after the wait"}}})
	id := saved["id"].(string)
	find := func() rule {
		for _, r := range app.rulesOf(user) {
			if r.ID == id {
				return r
			}
		}
		t.Fatal("workflow gone")
		return rule{}
	}
	waitFor := func(what string, ok func() bool) {
		t.Helper()
		for end := time.Now().Add(5 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			if ok() {
				return
			}
		}
		t.Fatal(what)
	}

	// Heimdeck stops while the run waits: the record stays, nothing is logged.
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan ruleLogEntry, 1)
	go func() { done <- app.runRule(ctx, user, find(), "", runByHand) }()
	waitFor("no record while waiting", func() bool { return app.hasRunRecord(user, id) })
	rec := app.runRecords(user)[id]
	if len(rec.At) != 1 || rec.At[0] != 1 || rec.Wake < time.Now().Add(29*time.Minute).Unix() {
		t.Fatalf("record: %+v", rec)
	}
	stop()
	if e := <-done; !strings.Contains(e.Result, "after the restart") {
		t.Fatalf("stopping: %+v", e)
	}
	if !app.hasRunRecord(user, id) || len(app.ruleLog(user)) != 0 {
		t.Fatal("the record went, or the stop was logged")
	}

	// The start: the wait is over meanwhile, the run goes on and ends.
	rec.Wake = time.Now().Add(-time.Minute).Unix()
	recs := app.runRecords(user)
	recs[id] = rec
	app.saveRunRecords(user, recs)
	app.resumeRuns(context.Background())
	waitFor("the run did not go on", func() bool { return len(app.ruleLog(user)) == 1 })
	e := app.ruleLog(user)[0]
	if !e.OK || !strings.Contains(e.Result, "Message") || !strings.Contains(e.Result, "waited") || app.hasRunRecord(user, id) {
		t.Fatalf("after the restart: %+v", e)
	}

	// Stopped on purpose (the workflow saved again): no record, logged as stopped.
	ctx2, stop2 := context.WithCancel(context.Background())
	defer stop2()
	go func() { done <- app.runRule(ctx2, user, find(), "", runByHand) }()
	waitFor("no record", func() bool { return app.hasRunRecord(user, id) })
	app.stopRuns(stateKey(user, id))
	if e := <-done; e.OK || app.hasRunRecord(user, id) {
		t.Fatalf("stopped on purpose: %+v", e)
	}

	// A record of a workflow changed since is dropped, not continued.
	recs = app.runRecords(user)
	rec.Rev = "older"
	recs[id] = rec
	app.saveRunRecords(user, recs)
	before := len(app.ruleLog(user))
	app.resumeRuns(context.Background())
	time.Sleep(100 * time.Millisecond)
	if app.hasRunRecord(user, id) || len(app.ruleLog(user)) != before {
		t.Fatal("a changed workflow was continued")
	}
}
