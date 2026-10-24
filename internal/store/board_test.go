package store

import "testing"

func TestBoardTheme(t *testing.T) {
	st, _ := testStore(t)
	b := Board{Name: "Arbeit", Icon: "💼", Theme: "nord"}
	if err := st.SaveBoard(&b); err != nil {
		t.Fatal(err)
	}
	got, err := st.Board(b.ID)
	if err != nil || got.Theme != "nord" {
		t.Fatalf("theme after create: %+v %v", got, err)
	}
	got.Theme = ""
	if err := st.SaveBoard(&got); err != nil {
		t.Fatal(err)
	}
	boards, _ := st.Boards()
	for _, x := range boards {
		if x.ID == b.ID && x.Theme != "" {
			t.Fatalf("theme not cleared: %+v", x)
		}
	}
}
