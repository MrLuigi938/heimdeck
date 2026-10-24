package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/store"
)

// A protected group whose password is forgotten can be unlocked with the
// account password, and then deleted.
func TestProtectedGroupUnlockWithAccountPassword(t *testing.T) {
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
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	call := func(method, path string, body any) int {
		b, _ := json.Marshal(body)
		req, _ := http.NewRequest(method, srv.URL+path, bytes.NewReader(b))
		req.Header.Set("Content-Type", "application/json")
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := call("POST", "/api/setup", map[string]string{"username": "luis", "password": "account-pw-123"}); code != 200 {
		t.Fatalf("setup: %d", code)
	}
	g, err := st.CreateGroup("Zeug", 0)
	if err != nil {
		t.Fatal(err)
	}
	id := "/api/groups/" + strconv.FormatInt(g.ID, 10)
	if code := call("PUT", id, map[string]string{"name": "Zeug", "visibility": "protected", "password": "group-pw"}); code != 200 {
		t.Fatalf("protect: %d", code)
	}
	call("POST", id+"/lock", map[string]string{})
	if code := call("DELETE", id, nil); code != 403 {
		t.Fatalf("locked group deleted: %d", code)
	}
	if code := call("POST", id+"/unlock", map[string]string{"password": "wrong"}); code != 403 {
		t.Fatalf("wrong password accepted: %d", code)
	}
	if code := call("POST", id+"/unlock", map[string]string{"password": "account-pw-123"}); code != 200 {
		t.Fatalf("account password refused: %d", code)
	}
	if code := call("DELETE", id, nil); code != 200 {
		t.Fatalf("delete after unlock: %d", code)
	}
}
