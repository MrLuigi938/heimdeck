package store

import (
	"path/filepath"
	"testing"

	"github.com/heimdeck/heimdeck/internal/secret"
)

// Only the integration's tiles that still have the old symbol get the new one.
func TestReplaceTileIcon(t *testing.T) {
	dir := t.TempDir()
	box, _ := secret.Open(dir)
	st, err := Open(filepath.Join(dir, "t.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if _, err := st.db.Exec(`INSERT INTO groups (name) VALUES ('g')`); err != nil {
		t.Fatal(err)
	}
	for _, row := range [][2]string{{"hivornet", "🐝"}, {"hivornet", "own.png"}, {"other", "🐝"}} {
		if _, err := st.db.Exec(`INSERT INTO tiles (group_id, name, integration, icon) VALUES (1, 'x', ?, ?)`, row[0], row[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.ReplaceTileIcon("hivornet", "🐝", "new"); err != nil {
		t.Fatal(err)
	}
	rows, _ := st.db.Query(`SELECT integration, icon FROM tiles ORDER BY id`)
	defer rows.Close()
	var got []string
	for rows.Next() {
		var i, ic string
		rows.Scan(&i, &ic)
		got = append(got, i+":"+ic)
	}
	want := "hivornet:new hivornet:own.png other:🐝"
	if s := got[0] + " " + got[1] + " " + got[2]; s != want {
		t.Fatalf("got %q, want %q", s, want)
	}
}
