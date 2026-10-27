package server

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/store"
)

// Another admin cannot demote, disable, delete or reset the password of the
// main admin; the main admin can still manage the other admins.
func TestMainAdminProtected(t *testing.T) {
	_, base := multiUserServer(t)
	luis := newTestClient(t, base)
	if code, _ := luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "de"}); code != 200 {
		t.Fatalf("setup: %d", code)
	}
	code, out := luis.call("POST", "/api/users", map[string]string{"username": "max", "password": "max-pw-1234", "role": "admin"})
	if code != 200 {
		t.Fatalf("create admin: %d %v", code, out)
	}
	maxID := int64(out["id"].(float64))
	_, list := luis.call("GET", "/api/users", nil)
	mainID := int64(list["main"].(float64))
	if mainID == 0 || mainID == maxID {
		t.Fatalf("main admin %d", mainID)
	}

	max := newTestClient(t, base)
	if code, _ := max.call("POST", "/api/login", map[string]string{"username": "max", "password": "max-pw-1234"}); code != 200 {
		t.Fatalf("login max: %d", code)
	}
	path := fmt.Sprintf("/api/users/%d", mainID)
	for _, body := range []map[string]any{{"role": "user"}, {"disabled": true}, {"password": "taken-over-1"}} {
		if code, _ := max.call("PUT", path, body); code != 403 {
			t.Errorf("max changed the main admin with %v: %d", body, code)
		}
	}
	if code, _ := max.call("DELETE", path, nil); code != 403 {
		t.Errorf("max deleted the main admin: %d", code)
	}
	if code, _ := luis.call("POST", "/api/login", map[string]string{"username": "luis", "password": "admin-pw-123"}); code != 200 {
		t.Fatalf("main admin can no longer log in: %d", code)
	}

	// The main admin manages the other admins as before.
	if code, out := luis.call("PUT", fmt.Sprintf("/api/users/%d", maxID), map[string]any{"role": "user"}); code != 200 || out["role"] != "user" {
		t.Fatalf("demote max: %d %v", code, out)
	}
}

// People a dashboard is shared with see its tiles, but not their settings
// (a webhook tile's key would let them send messages to it).
func TestSharedTilesWithoutSettings(t *testing.T) {
	st, base := multiUserServer(t)
	luis := newTestClient(t, base)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "de"})
	luis.call("POST", "/api/users", map[string]string{"username": "anna", "password": "anna-pw-123"})
	_, g := luis.call("POST", "/api/groups", map[string]any{"name": "Meldungen"})
	_, tile := luis.call("POST", "/api/tiles", map[string]any{"groupId": g["id"], "name": "Hook", "integration": "webhook", "config": map[string]string{"hookKey": "geheimer-schluessel-123456"}})
	if tile["id"] == nil {
		t.Fatalf("tile: %v", tile)
	}
	users, _ := st.Users()
	board := int64(g["boardId"].(float64))
	luis.call("PUT", fmt.Sprintf("/api/boards/%d/shares/%d", board, users[1].ID), map[string]bool{"actions": false})
	anna := newTestClient(t, base)
	anna.call("POST", "/api/login", map[string]string{"username": "anna", "password": "anna-pw-123"})
	anna.call("POST", fmt.Sprintf("/api/boards/%d/accept", board), nil)

	_, dash := anna.call("GET", "/api/dashboard", nil)
	found := false
	for _, gr := range dash["groups"].([]any) {
		for _, tl := range gr.(map[string]any)["tiles"].([]any) {
			tl := tl.(map[string]any)
			if tl["name"] == "Hook" {
				found = true
				if len(tl["config"].(map[string]any)) != 0 || tl["shared"] != true {
					t.Fatalf("shared tile shows its settings: %v", tl)
				}
			}
		}
	}
	if !found {
		t.Fatal("shared tile missing")
	}
	// The owner still gets them for editing.
	_, own := luis.call("GET", "/api/dashboard", nil)
	if !bytes.Contains([]byte(fmt.Sprint(own["groups"])), []byte("geheimer-schluessel-123456")) {
		t.Fatal("owner lost the tile settings")
	}
}

func TestSafeLinks(t *testing.T) {
	w := safeWidget(&integrations.Widget{Items: []integrations.Item{
		{URL: "javascript:alert(1)", Image: "javascript:x"},
		{URL: " JavaScript:alert(1)"},
		{URL: "data:text/html,x", Image: "data:image/png;base64,AA"},
		{URL: "//evil.example", Image: "/api/tiles/1/image?p=x"},
		{URL: "https://jellyfin.local/web", Image: "http://img.local/a.jpg"},
	}})
	got := []string{}
	for _, it := range w.Items {
		got = append(got, it.URL+"|"+it.Image)
	}
	want := []string{"|", "|", "|data:image/png;base64,AA", "|/api/tiles/1/image?p=x", "https://jellyfin.local/web|http://img.local/a.jpg"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %q", got)
	}
}

// A backup cannot bring in a search address that runs script.
func TestImportChecksSettings(t *testing.T) {
	_, base := multiUserServer(t)
	luis := newTestClient(t, base)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "de"})
	if code, out := luis.call("POST", "/api/import", map[string]any{"version": 1, "settings": map[string]string{"searchEngine": "custom", "searchUrl": "javascript:alert(1)//{q}", "title": "Zuhause"}}); code != 200 {
		t.Fatalf("import: %d %v", code, out)
	}
	_, dash := luis.call("GET", "/api/dashboard", nil)
	s := dash["settings"].(map[string]any)
	if s["searchUrl"] == "javascript:alert(1)//{q}" || s["title"] != "Zuhause" {
		t.Fatalf("settings after import: %v", s)
	}
}

// The Docker tile stops working when its owner is no longer an admin.
func TestDockerTileNeedsAdminOwner(t *testing.T) {
	s := testServer(t)
	id, err := s.store.CreateUser("max", "x", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	b := store.Board{OwnerID: id, Name: "System"}
	s.store.SaveBoard(&b)
	g, _ := s.store.CreateGroup("Docker", b.ID)
	tile := store.Tile{GroupID: g.ID, Name: "Docker", Integration: "docker", Monitor: true}
	if err := s.store.SaveTile(&tile); err != nil {
		t.Fatal(err)
	}
	if !s.dockerAllowed(tile) {
		t.Fatal("admin's Docker tile refused")
	}
	u, _ := s.store.User(id)
	u.Role = store.RoleUser
	s.store.UpdateUser(u)
	if s.dockerAllowed(tile) {
		t.Fatal("Docker tile still works after the owner was demoted")
	}
}
