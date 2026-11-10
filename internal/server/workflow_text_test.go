package server

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/store"
)

// Building blocks in a workflow's message are filled with the tile's current
// values when it runs – what is playing, a temperature – and checked on saving.
func TestWorkflowBuildingBlocks(t *testing.T) {
	media := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"temp": 61.5, "secret": "x", "now": [{"name": "Dune", "who": "Luis · TV"}, {"name": "Andor", "who": "Anna · Phone"}]}`)
	}))
	defer media.Close()

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
	_, g := luis.call("POST", "/api/groups", map[string]any{"name": "Server"})
	if code, res := luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "Media", "url": media.URL, "integration": "customapi", "showWidget": true,
		"hide": []string{"Secret"}, "config": map[string]string{"stats": "Temperature = temp | zahl:1 °C\nSecret = secret", "list": "now", "item": "title = name\nsubtitle = who"}}); code != 200 {
		t.Fatalf("tile: %d %v", code, res)
	}

	// The editor offers the values and the list.
	var tiles []ruleTile
	getJSON(luis, "/api/rules/tiles", &tiles)
	if len(tiles) != 1 || tiles[0].List == "" || len(tiles[0].Stats) != 1 {
		t.Fatalf("tiles: %+v", tiles)
	}

	wf := func(text string) map[string]any {
		return map[string]any{"name": "Stream", "on": true, "triggers": []any{map[string]any{"kind": "manual"}},
			"steps": []any{map[string]any{"kind": "notify", "text": text}}}
	}
	// Checked on saving: a tile or value that does not exist, a hidden one, nonsense.
	for text, want := range map[string]string{
		"{Plex · Liste}":     "keine Kachel",
		"{Media · Humidity}": "hat keinen Wert",
		"{Media · Secret}":   "hat keinen Wert",
		"{what is this}":     "kenne ich nicht",
	} {
		if code, res := luis.call("POST", "/api/rules", wf(text)); code != 400 || !strings.Contains(fmt.Sprint(res["error"]), want) {
			t.Fatalf("%s: %d %v", text, code, res)
		}
	}

	// Filled in on the run (here: the dry run).
	_, res := luis.call("POST", "/api/rules/dryrun", wf("Playing: {Media · List} – temperature {media · temperature} at {time} ({reason})"))
	out := fmt.Sprint(res["lines"])
	if !strings.Contains(out, "• Dune (Luis · TV)") || !strings.Contains(out, "• Andor (Anna · Phone)") || !strings.Contains(out, "temperature 61.5 °C") || strings.Contains(out, "{") {
		t.Fatalf("dry run: %v", res)
	}

	// A block whose tile went away stays visible and is noted, the rest still goes out.
	w := &wfRun{s: app, ctx: t.Context(), user: 1, lang: "en", ok: true}
	if got := w.fill("A {Gone · List} B"); got != "A {Gone · List} B" || len(w.results) != 1 {
		t.Fatalf("missing tile: %q %v", got, w.results)
	}
	if listText("en", []integrations.Item{{Title: "Dune"}}) != "Dune" || listText("en", nil) != "nothing" {
		t.Fatal("listText")
	}
}
