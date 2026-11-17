package server

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/store"
)

// The free page layout is kept per account; something that is not a layout is refused.
func TestPageLayoutSetting(t *testing.T) {
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

	layout := `{"all":{"mode":"free","desktop":{"search":{"x":0,"y":2,"w":3,"h":20}}},"boards":{"2":{"mode":"auto"}}}`
	if code, body := luis.call("PUT", "/api/settings", map[string]string{"pageLayout": layout}); code != 200 {
		t.Fatalf("saving a layout: %d %s", code, body)
	}
	if got := app.settingsFor(users[0].ID)["pageLayout"]; got != layout {
		t.Fatalf("stored %q", got)
	}
	if code, body := luis.call("PUT", "/api/settings", map[string]string{"pageWidth": "full", "pageWidthBoards": `{"2":"normal","3":"wide"}`}); code != 200 {
		t.Fatalf("saving the page width: %d %s", code, body)
	}
	for _, bad := range []map[string]string{{"pageWidth": "huge"}, {"pageWidthBoards": `{"2":"huge"}`}, {"pageWidthBoards": "{not json"}} {
		if code, _ := luis.call("PUT", "/api/settings", bad); code != 400 {
			t.Errorf("%v: got %d, want 400", bad, code)
		}
	}
	for _, bad := range []string{"{not json", `{"all":"free"}`} {
		if code, _ := luis.call("PUT", "/api/settings", map[string]string{"pageLayout": bad}); code != 400 {
			t.Errorf("%q: got %d, want 400", bad, code)
		}
	}
	if got := app.settingsFor(users[0].ID)["pageLayout"]; got != layout {
		t.Fatalf("a refused layout replaced the stored one: %q", got)
	}
}
