package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/store"
)

type testClient struct {
	t    *testing.T
	base string
	c    *http.Client
}

func (tc *testClient) call(method, path string, body any) (int, map[string]any) {
	tc.t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(method, tc.base+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := tc.c.Do(req)
	if err != nil {
		tc.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	json.Unmarshal(raw, &out)
	if out == nil || len(out) == 0 {
		out = map[string]any{"_raw": string(raw)}
	}
	return resp.StatusCode, out
}

func newTestClient(t *testing.T, base string) *testClient {
	jar, _ := cookiejar.New(nil)
	return &testClient{t: t, base: base, c: &http.Client{Jar: jar}}
}

func multiUserServer(t *testing.T) (*store.Store, string) {
	dir := t.TempDir()
	box, err := secret.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(dir, "test.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := httptest.NewServer(New(st, auth.New(st, false), nil, nil).Handler())
	t.Cleanup(srv.Close)
	return st, srv.URL
}

// A second user sees nothing of the first one's dashboards until one is
// shared and accepted, and then can look but not change anything.
func TestSharedDashboards(t *testing.T) {
	st, base := multiUserServer(t)
	admin := newTestClient(t, base)
	if code, _ := admin.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "de"}); code != 200 {
		t.Fatalf("setup: %d", code)
	}
	if code, out := admin.call("POST", "/api/users", map[string]string{"username": "anna", "password": "anna-pw-123"}); code != 200 || out["role"] != "user" {
		t.Fatalf("create user: %d %v", code, out)
	}
	anna := newTestClient(t, base)
	if code, _ := anna.call("POST", "/api/login", map[string]string{"username": "anna", "password": "anna-pw-123"}); code != 200 {
		t.Fatalf("login anna: %d", code)
	}

	adminBoards, _ := st.BoardsOf(1)
	board := adminBoards[0].ID
	_, g := admin.call("POST", "/api/groups", map[string]any{"name": "Privat", "boardId": board})
	gid := int64(g["id"].(float64))
	_, tile := admin.call("POST", "/api/tiles", map[string]any{"groupId": gid, "name": "NAS", "url": "http://nas.local", "integration": "link"})
	tid := int64(tile["id"].(float64))
	tpath := fmt.Sprintf("/api/tiles/%d", tid)

	// Anna's own dashboard was created with her account.
	_, dash := anna.call("GET", "/api/dashboard", nil)
	if bs := dash["boards"].([]any); len(bs) != 1 || dash["role"] != "user" {
		t.Fatalf("anna's boards: %v", dash["boards"])
	}
	if strings.Contains(fmt.Sprint(dash["groups"]), "NAS") {
		t.Fatal("anna sees the admin's tiles")
	}
	// Nothing of the admin's is reachable.
	for _, c := range []struct{ method, path string }{
		{"GET", tpath + "/widget"}, {"PUT", tpath}, {"DELETE", tpath}, {"POST", tpath + "/action"},
		{"DELETE", fmt.Sprintf("/api/groups/%d", gid)}, {"PUT", fmt.Sprintf("/api/boards/%d", board)},
		{"DELETE", fmt.Sprintf("/api/boards/%d", board)},
	} {
		if code, _ := anna.call(c.method, c.path, map[string]any{"name": "x", "groupId": gid, "action": "x"}); code != 404 {
			t.Errorf("%s %s before sharing: %d", c.method, c.path, code)
		}
	}
	// Borrowing the keys of the admin's tile by sending its ID.
	if code, out := anna.call("POST", "/api/tiles/test", map[string]any{"id": tid, "name": "x", "integration": "link", "url": "http://x"}); code != 200 || out["ok"] != false {
		t.Errorf("test with foreign tile id: %d %v", code, out)
	}
	if code, _ := anna.call("PUT", "/api/layout", []map[string]any{{"groupId": gid, "tiles": []int64{tid}}}); code != 404 {
		t.Errorf("layout of a foreign group: %d", code)
	}
	if code, _ := anna.call("GET", "/api/users", nil); code != 403 {
		t.Errorf("user list as user: %d", code)
	}
	if code, _ := anna.call("GET", "/api/discover", nil); code != 403 {
		t.Errorf("discover as user: %d", code)
	}
	if code, out := anna.call("POST", "/api/tiles", map[string]any{"groupId": gid, "name": "x", "integration": "link"}); code != 404 {
		t.Errorf("tile into a foreign group: %d %v", code, out)
	}
	annaBoards, _ := st.BoardsOf(2)
	_, ag := anna.call("POST", "/api/groups", map[string]any{"name": "Meins", "boardId": annaBoards[0].ID})
	if code, out := anna.call("POST", "/api/tiles", map[string]any{"groupId": ag["id"], "name": "Docker", "integration": "docker"}); code != 400 {
		t.Errorf("docker tile as user: %d %v", code, out)
	}

	// Shared, but not accepted yet: still invisible.
	share := fmt.Sprintf("/api/boards/%d/shares/2", board)
	if code, _ := anna.call("PUT", fmt.Sprintf("/api/boards/%d/shares/1", annaBoards[0].ID+100), map[string]bool{}); code != 404 {
		t.Errorf("sharing a board that does not exist: %d", code)
	}
	if code, _ := admin.call("PUT", share, map[string]bool{"actions": false}); code != 200 {
		t.Fatalf("share: %d", code)
	}
	_, dash = anna.call("GET", "/api/dashboard", nil)
	if len(dash["shareRequests"].([]any)) != 1 || strings.Contains(fmt.Sprint(dash["groups"]), "NAS") {
		t.Fatalf("pending share: %v / %v", dash["shareRequests"], dash["groups"])
	}
	if code, _ := anna.call("GET", tpath+"/widget", nil); code != 404 {
		t.Errorf("widget before accepting: %d", code)
	}

	// Accepted: visible, read-only, no buttons.
	if code, _ := anna.call("POST", fmt.Sprintf("/api/boards/%d/accept", board), nil); code != 200 {
		t.Fatalf("accept: %d", code)
	}
	_, dash = anna.call("GET", "/api/dashboard", nil)
	if !strings.Contains(fmt.Sprint(dash["groups"]), "NAS") || !strings.Contains(fmt.Sprint(dash["boards"]), "owner:luis") {
		t.Fatalf("accepted share not shown: %v", dash["boards"])
	}
	if code, _ := anna.call("GET", tpath+"/widget", nil); code != 200 {
		t.Errorf("widget after accepting: %d", code)
	}
	for _, c := range []struct{ method, path string }{
		{"PUT", tpath}, {"DELETE", tpath}, {"DELETE", fmt.Sprintf("/api/groups/%d", gid)},
		{"PUT", fmt.Sprintf("/api/groups/%d", gid)}, {"PUT", fmt.Sprintf("/api/boards/%d", board)},
		{"DELETE", fmt.Sprintf("/api/boards/%d", board)}, {"POST", tpath + "/action"},
	} {
		if code, _ := anna.call(c.method, c.path, map[string]any{"name": "x", "groupId": gid, "action": "x"}); code != 403 {
			t.Errorf("%s %s as viewer: %d", c.method, c.path, code)
		}
	}
	if code, _ := anna.call("PUT", share, map[string]bool{"actions": true}); code != 403 {
		t.Errorf("viewer changed the share: %d", code)
	}
	// With buttons allowed the action goes through to the service (a link has none).
	admin.call("PUT", share, map[string]bool{"actions": true})
	if code, _ := anna.call("POST", tpath+"/action", map[string]any{"action": "x"}); code != 400 {
		t.Errorf("action with permission: %d", code)
	}

	// A protected group: the viewer needs the group's password, not her own.
	admin.call("PUT", fmt.Sprintf("/api/groups/%d", gid), map[string]string{"visibility": "protected", "password": "group-pw"})
	if code, _ := anna.call("POST", fmt.Sprintf("/api/groups/%d/unlock", gid), map[string]string{"password": "anna-pw-123"}); code != 403 {
		t.Errorf("viewer unlocked with her account password: %d", code)
	}
	if code, _ := anna.call("POST", fmt.Sprintf("/api/groups/%d/unlock", gid), map[string]string{"password": "group-pw"}); code != 200 {
		t.Errorf("viewer with the group password: %d", code)
	}

	// Personal settings stay personal; instance settings are for admins.
	anna.call("PUT", "/api/settings", map[string]string{"theme": "nord", "debug": "true"})
	_, dash = admin.call("GET", "/api/dashboard", nil)
	if dash["settings"].(map[string]any)["theme"] == "nord" {
		t.Error("anna's theme changed the admin's")
	}
	if s, _ := st.Settings(); s["debug"] == "true" {
		t.Error("user turned on the debug mode")
	}

	// Anna leaves the share; the admin removes her account.
	if code, _ := anna.call("DELETE", share, nil); code != 200 {
		t.Errorf("leave share: %d", code)
	}
	if code, _ := anna.call("GET", tpath+"/widget", nil); code != 404 {
		t.Errorf("widget after leaving: %d", code)
	}
	if code, _ := admin.call("DELETE", "/api/users/1", nil); code != 400 {
		t.Errorf("admin deleted themself: %d", code)
	}
	if code, _ := admin.call("DELETE", "/api/users/2", nil); code != 200 {
		t.Fatalf("delete user: %d", code)
	}
	if code, _ := anna.call("GET", "/api/dashboard", nil); code != 401 {
		t.Errorf("deleted user still logged in: %d", code)
	}
	if b, _ := st.BoardsOf(2); len(b) != 0 {
		t.Errorf("boards of the deleted user remain: %v", b)
	}
}

// An instance from before 1.4: its only user becomes the admin and owns the
// dashboards, and the personal settings move to that account.
func TestUpgradeToSeveralUsers(t *testing.T) {
	dir := t.TempDir()
	box, _ := secret.Open(dir)
	path := filepath.Join(dir, "test.db")
	st, err := store.Open(path, box)
	if err != nil {
		t.Fatal(err)
	}
	st.CreateUser("luis", "x", store.RoleUser) // as if created by an old version
	b := store.Board{Name: "Arbeit"}
	st.SaveBoard(&b)
	st.SaveSettings(map[string]string{"theme": "nord", "language": "en", "debug": "true"})
	st.Close()

	st, err = store.Open(path, box)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	New(st, auth.New(st, false), nil, nil)
	if u, _ := st.User(1); u.Role != store.RoleAdmin {
		t.Fatalf("role %q", u.Role)
	}
	if boards, _ := st.BoardsOf(1); len(boards) != 2 {
		t.Fatalf("boards of the admin: %v", boards)
	}
	own, _ := st.UserSettings(1)
	global, _ := st.Settings()
	if own["theme"] != "nord" || own["language"] != "en" || global["theme"] != "" || global["debug"] != "true" || global["language"] != "en" {
		t.Fatalf("own %v global %v", own, global)
	}
}

func TestInvites(t *testing.T) {
	_, base := multiUserServer(t)
	admin := newTestClient(t, base)
	admin.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123"})
	_, inv := admin.call("POST", "/api/invites", nil)
	token, _ := inv["token"].(string)
	if len(token) < 40 {
		t.Fatalf("invite: %v", inv)
	}
	guest := newTestClient(t, base)
	if _, out := guest.call("GET", "/api/invite/nope", nil); out["valid"] != false {
		t.Errorf("unknown token valid: %v", out)
	}
	if _, out := guest.call("GET", "/api/invite/"+token, nil); out["valid"] != true {
		t.Errorf("token not valid: %v", out)
	}
	if code, _ := guest.call("POST", "/api/invite/"+token, map[string]string{"username": "luis", "password": "something-123"}); code != 409 {
		t.Errorf("taken name: %d", code)
	}
	if code, _ := guest.call("POST", "/api/invite/"+token, map[string]string{"username": "ben", "password": "ben-pw-123", "language": "en"}); code != 200 {
		t.Fatalf("join: %d", code)
	}
	if code, out := guest.call("GET", "/api/dashboard", nil); code != 200 || out["username"] != "ben" || out["role"] != "user" {
		t.Fatalf("after joining: %d %v", code, out)
	}
	other := newTestClient(t, base)
	if code, _ := other.call("POST", "/api/invite/"+token, map[string]string{"username": "eve", "password": "eve-pw-1234"}); code != 403 {
		t.Errorf("invite used twice: %d", code)
	}
	// Disabled accounts are logged out and cannot log in.
	admin.call("PUT", "/api/users/2", map[string]bool{"disabled": true})
	if code, _ := guest.call("GET", "/api/dashboard", nil); code != 401 {
		t.Errorf("disabled user still logged in: %d", code)
	}
	if code, _ := guest.call("POST", "/api/login", map[string]string{"username": "ben", "password": "ben-pw-123"}); code != 403 {
		t.Errorf("disabled user logged in again: %d", code)
	}
	if code, _ := admin.call("PUT", "/api/users/1", map[string]string{"role": "user"}); code != 400 {
		t.Errorf("last admin demoted: %d", code)
	}
}
