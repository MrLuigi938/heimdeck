package server

import (
	"strings"
	"testing"
	"time"

	"github.com/heimdeck/heimdeck/internal/store"
)

// A value marked for history is recorded and comes back as a chart; the
// mark itself never hides anything.
func TestHistory(t *testing.T) {
	s := testServer(t)
	g, _ := s.store.CreateGroup("A", 0)
	const key = "Hist123_-Hist123_-Hist12"
	tile := store.Tile{GroupID: g.ID, Name: "Hooks", Integration: "webhook", Config: map[string]string{"hookKey": key},
		Hide: []string{"~rec:Meldungen (24 Std.)"}}
	if err := s.store.SaveTile(&tile); err != nil {
		t.Fatal(err)
	}
	// An older value, then one recorded now.
	s.store.AddPoints(tile.ID, []store.Point{{Name: "stat:Meldungen (24 Std.)", T: time.Now().Add(-time.Hour).UnixMilli(), Value: 4}})
	s.recordHistory(t.Context())
	pts, _ := s.store.Points(tile.ID, time.Now().Add(-2*time.Hour))
	if list := pts["stat:Meldungen (24 Std.)"]; len(list) != 2 || list[1].Value != 0 {
		t.Fatalf("recorded %+v", pts)
	}
	tile, _ = s.store.Tile(tile.ID)
	w, err := s.safeFetch(t.Context(), tile, "en")
	if err != nil {
		t.Fatal(err)
	}
	w = s.withHistory(hideParts(w, tile.Hide, "en"), tile, "en")
	if len(w.Stats) != 2 {
		t.Fatalf("the mark hid a value: %+v", w.Stats)
	}
	if len(w.Charts) != 1 || w.Charts[0].ID != "rec:Meldungen (24 Std.)" || len(w.Charts[0].Times) != 2 || !strings.Contains(w.Charts[0].Title, "Messages") {
		t.Fatalf("chart %+v", w.Charts)
	}
	// A tile without marks records nothing.
	if keys := recordedKeys(store.Tile{Hide: []string{"#items", "CPU"}}); len(keys) != 0 {
		t.Fatalf("keys %v", keys)
	}
}
