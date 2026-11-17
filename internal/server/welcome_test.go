package server

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/store"
)

// A new account starts with Heimdeck's own background and the welcome; once
// it is finished it does not come back.
func TestWelcomeForNewAccounts(t *testing.T) {
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
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "de"})
	users, _ := st.Users()
	got := app.settingsFor(users[0].ID)
	if got["onboarding"] != "1" || got["background"] != "builtin:bifroest" {
		t.Fatalf("new account: onboarding %q, background %q", got["onboarding"], got["background"])
	}
	if code, body := luis.call("PUT", "/api/settings", map[string]string{"onboarding": "", "background": "builtin:deck"}); code != 200 {
		t.Fatalf("finishing the welcome: %d %s", code, body)
	}
	got = app.settingsFor(users[0].ID)
	if got["onboarding"] != "" || got["background"] != "builtin:deck" {
		t.Fatalf("after the welcome: onboarding %q, background %q", got["onboarding"], got["background"])
	}
}
