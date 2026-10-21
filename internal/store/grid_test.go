package store

import (
	"path/filepath"
	"testing"

	"github.com/heimdeck/heimdeck/internal/secret"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	box, err := secret.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "test.db")
	st, err := Open(path, box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st, dir
}

func TestGridPositions(t *testing.T) {
	st, _ := testStore(t)
	a, _ := st.CreateGroup("A", 0)
	b, _ := st.CreateGroup("B", 0)
	t1 := Tile{GroupID: a.ID, Name: "one", Integration: "link"}
	t2 := Tile{GroupID: a.ID, Name: "two", Integration: "link", Grid: &GridPos{X: 11, Y: 0, W: 3, H: 2}} // does not fit
	for _, tl := range []*Tile{&t1, &t2} {
		if err := st.SaveTile(tl); err != nil {
			t.Fatal(err)
		}
	}
	if got, _ := st.Tile(t2.ID); got.Grid != nil {
		t.Fatalf("invalid position stored: %+v", got.Grid)
	}

	// A freely arranged group saves positions with the order.
	err := st.SaveLayout(Layout{
		{GroupID: a.ID, Tiles: []int64{t1.ID, t2.ID}, Grid: map[int64]GridPos{t1.ID: {X: 4, Y: 2, W: 3, H: 2}, t2.ID: {X: 0, Y: 0, W: 13, H: 1}}},
		{GroupID: b.ID, Tiles: []int64{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := st.Tile(t1.ID)
	if got.Grid == nil || *got.Grid != (GridPos{X: 4, Y: 2, W: 3, H: 2}) {
		t.Fatalf("position not saved: %+v", got.Grid)
	}
	if got, _ := st.Tile(t2.ID); got.Grid != nil {
		t.Fatalf("position wider than the grid stored: %+v", got.Grid)
	}

	// Editing the tile keeps its position, moving it to another group drops it.
	got.Name = "renamed"
	if err := st.SaveTile(&got); err != nil {
		t.Fatal(err)
	}
	if again, _ := st.Tile(t1.ID); again.Grid == nil {
		t.Fatal("editing a tile lost its position")
	}
	got.GroupID = b.ID
	if err := st.SaveTile(&got); err != nil {
		t.Fatal(err)
	}
	if again, _ := st.Tile(t1.ID); again.Grid != nil {
		t.Fatalf("tile moved to another group kept its position: %+v", again.Grid)
	}

	// Same through the layout: moving back into A without a position.
	st.SaveLayout(Layout{{GroupID: b.ID, Tiles: []int64{t1.ID}, Grid: map[int64]GridPos{t1.ID: {X: 0, Y: 0, W: 2, H: 2}}}})
	st.SaveLayout(Layout{{GroupID: a.ID, Tiles: []int64{t1.ID, t2.ID}}})
	if again, _ := st.Tile(t1.ID); again.Grid != nil || again.GroupID != a.ID {
		t.Fatalf("layout move kept the position: %+v", again.Grid)
	}
}

func TestColumnsArrangementMigrated(t *testing.T) {
	st, dir := testStore(t)
	g, _ := st.CreateGroup("A", 0)
	g.Arrange = "columns"
	if err := st.UpdateGroup(g); err != nil {
		t.Fatal(err)
	}
	st.Close()
	box, _ := secret.Open(dir)
	st2, err := Open(filepath.Join(dir, "test.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	defer st2.Close()
	if got, _ := st2.Group(g.ID); got.Arrange != "" {
		t.Fatalf("arrange = %q, want rows", got.Arrange)
	}
}
