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

	// The package name or the Play Store address is enough for Android.
	for in, want := range map[string]string{
		" dev.jdtech.jellyfin ": "intent://#Intent;package=dev.jdtech.jellyfin;end",
		"https://play.google.com/store/apps/details?id=com.ubnt.easyunifi&hl=de": "intent://#Intent;package=com.ubnt.easyunifi;end",
		"homeassistant://navigate": "homeassistant://navigate",
		"jellyfin":                 "jellyfin",
	} {
		if got := androidApp(in); got != want {
			t.Errorf("androidApp(%q) = %q, want %q", in, got, want)
		}
	}

	// Stored with the tile and kept when the tile is edited.
	s := testServer(t)
	g, _ := s.store.CreateGroup("A", 0)
	tile, err := s.buildTile(tileInput{Tile: store.Tile{GroupID: g.ID, Name: "UniFi", URL: "https://192.168.1.1", Integration: "link",
		AppAndroid: " com.ubnt.easyunifi ", AppIOS: "shortcuts://run-shortcut?name=UniFi"}}, 0)
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
