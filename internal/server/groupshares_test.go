package server

import (
	"fmt"
	"testing"
)

// A single group can be shared: the other person sees only that group (as a
// dashboard "Shared groups"), view only, and nothing else of the dashboard.
func TestGroupShares(t *testing.T) {
	st, base := multiUserServer(t)
	luis := newTestClient(t, base)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "de"})
	luis.call("POST", "/api/users", map[string]string{"username": "anna", "password": "anna-pw-123"})
	_, media := luis.call("POST", "/api/groups", map[string]any{"name": "Medien geteilt"})
	_, private := luis.call("POST", "/api/groups", map[string]any{"name": "Privat"})
	_, shown := luis.call("POST", "/api/tiles", map[string]any{"groupId": media["id"], "name": "Jellyfin", "url": "http://jellyfin.local"})
	_, hidden := luis.call("POST", "/api/tiles", map[string]any{"groupId": private["id"], "name": "Bank", "url": "http://bank.local"})
	users, _ := st.Users()
	annaID := users[1].ID

	if code, out := luis.call("PUT", "/api/groups/shares", map[string]any{"groups": []any{media["id"]}, "user": annaID}); code != 200 {
		t.Fatalf("share: %d %v", code, out)
	}
	anna := newTestClient(t, base)
	anna.call("POST", "/api/login", map[string]string{"username": "anna", "password": "anna-pw-123"})
	// Not accepted yet: a request, nothing visible.
	_, dash := anna.call("GET", "/api/dashboard", nil)
	if len(dash["groupRequests"].([]any)) != 1 || fmt.Sprint(dash["groups"]) != fmt.Sprint(onlyOwn(dash)) {
		t.Fatalf("before accepting: %v", dash["groupRequests"])
	}
	if code, _ := anna.call("GET", fmt.Sprintf("/api/tiles/%v/widget", shown["id"]), nil); code != 404 {
		t.Fatalf("tile visible before accepting: %d", code)
	}
	if code, _ := anna.call("POST", fmt.Sprintf("/api/groups/%v/accept", media["id"]), nil); code != 200 {
		t.Fatalf("accept: %d", code)
	}
	_, dash = anna.call("GET", "/api/dashboard", nil)
	var vb float64
	for _, b := range dash["boards"].([]any) {
		b := b.(map[string]any)
		if b["shared"] == true {
			vb = b["id"].(float64)
		}
	}
	found := map[string]float64{}
	for _, g := range dash["groups"].([]any) {
		g := g.(map[string]any)
		found[g["name"].(string)] = g["boardId"].(float64)
	}
	if vb >= 0 || found["Medien geteilt"] != vb || found["Privat"] != 0 {
		t.Fatalf("shared groups dashboard %v, groups %v", vb, found)
	}
	if code, _ := anna.call("GET", fmt.Sprintf("/api/tiles/%v/widget", shown["id"]), nil); code != 200 {
		t.Fatalf("shared tile not visible: %d", code)
	}
	if code, _ := anna.call("GET", fmt.Sprintf("/api/tiles/%v/widget", hidden["id"]), nil); code != 404 {
		t.Fatalf("tile of another group visible: %d", code)
	}
	// View only, and no buttons without permission.
	if code, _ := anna.call("PUT", fmt.Sprintf("/api/tiles/%v", shown["id"]), map[string]any{"groupId": media["id"], "name": "x", "url": "http://x"}); code == 200 {
		t.Fatal("anna edited a shared tile")
	}
	if code, _ := anna.call("POST", fmt.Sprintf("/api/tiles/%v/action", shown["id"]), map[string]any{"action": "x"}); code != 403 {
		t.Fatalf("action without permission: %d", code)
	}
	// Someone else's groups cannot be shared.
	if code, _ := anna.call("PUT", "/api/groups/shares", map[string]any{"groups": []any{private["id"]}, "user": users[0].ID}); code == 200 {
		t.Fatal("anna shared luis' group")
	}
	// Leaving the share takes the group away again.
	anna.call("DELETE", fmt.Sprintf("/api/groups/%v/shares/%d", media["id"], annaID), nil)
	if code, _ := anna.call("GET", fmt.Sprintf("/api/tiles/%v/widget", shown["id"]), nil); code != 404 {
		t.Fatalf("still visible after leaving: %d", code)
	}
}

// onlyOwn lists the groups on the user's own dashboards.
func onlyOwn(dash map[string]any) []any {
	own := map[float64]bool{}
	for _, b := range dash["boards"].([]any) {
		if b.(map[string]any)["shared"] != true {
			own[b.(map[string]any)["id"].(float64)] = true
		}
	}
	out := []any{}
	for _, g := range dash["groups"].([]any) {
		if own[g.(map[string]any)["boardId"].(float64)] {
			out = append(out, g)
		}
	}
	return out
}
