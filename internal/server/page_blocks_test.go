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

// Info blocks and hidden parts of a free page are kept with the layout;
// unknown kinds, nested options, hiding the settings or too long notes are refused.
func TestPageLayoutBlocks(t *testing.T) {
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

	good := `{"all":{"mode":"free","hidden":["title","tools"],"blocks":{"i1a":{"type":"clock","opt":{"face":"analog","seconds":true}},` +
		`"i2b":{"type":"weather","tile":7,"opt":{"mood":true},"style":{"bg":"glass","scale":1.2}}},"desktop":{"i1a":{"x":0,"y":0,"w":4,"h":4}}}}`
	if code, body := luis.call("PUT", "/api/settings", map[string]string{"pageLayout": good}); code != 200 {
		t.Fatalf("saving blocks: %d %s", code, body)
	}
	for _, bad := range []string{
		`{"all":{"blocks":{"i1":{"type":"script"}}}}`,
		`{"all":{"blocks":{"x1":{"type":"clock"}}}}`,
		`{"all":{"blocks":{"i1":{"type":"clock","opt":{"face":{"a":1}}}}}}`,
		`{"all":{"hidden":["settings"]}}`,
		`{"all":{"blocks":{"i1":{"type":"text","opt":{"text":"` + strings.Repeat("x", 5001) + `"}}}}}`,
	} {
		if code, _ := luis.call("PUT", "/api/settings", map[string]string{"pageLayout": bad}); code != 400 {
			t.Errorf("%.80s: got %d, want 400", bad, code)
		}
	}
}
