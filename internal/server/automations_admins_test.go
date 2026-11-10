package server

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/store"
)

// "Automations for admins only": other accounts can neither see nor use them,
// their workflows stay stored but do not run – until it is switched off again.
func TestAutomationsForAdminsOnly(t *testing.T) {
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
	luis.call("POST", "/api/users", map[string]string{"username": "anna", "password": "anna-pw-123"})
	anna := newTestClient(t, srv.URL)
	anna.call("POST", "/api/login", map[string]string{"username": "anna", "password": "anna-pw-123"})
	users, _ := st.Users()
	annaID := users[1].ID

	wf := map[string]any{"name": "Scene", "on": true, "triggers": []any{map[string]any{"kind": "manual"}}, "steps": []any{map[string]any{"kind": "notify", "text": "hi"}}}
	if code, _ := anna.call("POST", "/api/rules", wf); code != 200 {
		t.Fatal("anna could not make a workflow while everyone may")
	}
	// Only an admin may switch it; anna's attempt is ignored.
	anna.call("PUT", "/api/settings", map[string]string{"automationsAdmins": "true"})
	if !app.mayAutomate(annaID) {
		t.Fatal("a user switched automations to admins only")
	}
	luis.call("PUT", "/api/settings", map[string]string{"automationsAdmins": "true"})

	for _, path := range []string{"/api/rules", "/api/rules/tiles", "/api/rules/templates"} {
		if code, _ := anna.call("GET", path, nil); code != 403 {
			t.Fatalf("%s for a user: %d", path, code)
		}
	}
	if code, _ := anna.call("POST", "/api/rules", wf); code != 403 {
		t.Fatal("a user saved a workflow")
	}
	if code, _ := luis.call("GET", "/api/rules", nil); code != 200 {
		t.Fatal("the admin lost the automations")
	}
	// Stored, but it does not run.
	r := app.rulesOf(annaID)
	if len(r) != 1 {
		t.Fatal("anna's workflow was deleted")
	}
	if e := app.runRule(t.Context(), annaID, r[0], "", runByHand); e.OK || !strings.Contains(e.Result, "nur für Admins") {
		t.Fatalf("anna's workflow ran: %+v", e)
	}
	if app.rulesAsk(annaID) {
		t.Fatal("the bot listens for anna's scenes")
	}

	// Switched off again: anna's workflows are back.
	luis.call("PUT", "/api/settings", map[string]string{"automationsAdmins": "false"})
	if code, _ := anna.call("GET", "/api/rules", nil); code != 200 {
		t.Fatal("anna's automations did not come back")
	}
	if e := app.runRule(t.Context(), annaID, r[0], "", runByHand); !e.OK {
		t.Fatalf("anna's workflow after switching back: %+v", e)
	}
}
