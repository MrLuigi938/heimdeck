package server

import (
	"testing"

	"github.com/heimdeck/heimdeck/internal/store"
)

func TestAppLink(t *testing.T) {
	for _, ok := range []string{"", "intent://#Intent;package=com.ubnt.easyunifi;end", "shortcuts://run-shortcut?name=UniFi", "homeassistant://navigate/lovelace", "https://unifi.ui.com"} {
		if _, err := appLink(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"javascript:alert(1)", " JavaScript:alert(1)", "data:text/html,x", "file:///etc/passwd", "unifi.ui.com", "http://x"} {
		if _, err := appLink(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}

	// Stored with the tile and kept when the tile is edited.
	s := testServer(t)
	g, _ := s.store.CreateGroup("A", 0)
	tile, err := s.buildTile(tileInput{Tile: store.Tile{GroupID: g.ID, Name: "UniFi", URL: "https://192.168.1.1", Integration: "link",
		AppAndroid: " intent://#Intent;package=com.ubnt.easyunifi;end ", AppIOS: "shortcuts://run-shortcut?name=UniFi"}}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.store.SaveTile(&tile); err != nil {
		t.Fatal(err)
	}
	got, _ := s.store.Tile(tile.ID)
	if got.AppAndroid != "intent://#Intent;package=com.ubnt.easyunifi;end" || got.AppIOS != "shortcuts://run-shortcut?name=UniFi" {
		t.Fatalf("stored %q %q", got.AppAndroid, got.AppIOS)
	}
}
