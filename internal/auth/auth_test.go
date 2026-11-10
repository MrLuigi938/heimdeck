package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/store"
)

func newAuth(t *testing.T, secure bool) (*Auth, *store.Store) {
	t.Helper()
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
	return New(st, secure), st
}

func request(ip string, cookies ...*http.Cookie) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = ip + ":40000"
	for _, c := range cookies {
		r.AddCookie(c)
	}
	return r
}

func sessionCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == CookieName {
			return c
		}
	}
	return nil
}

// Login: the cookie is HttpOnly and SameSite, Secure behind TLS; the session
// names the user; logout ends it.
func TestLoginAndSession(t *testing.T) {
	a, _ := newAuth(t, true)
	id, err := a.Register(nil, "luis", "right-pw-123", store.RoleAdmin)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	if err := a.Login(w, request("10.0.0.1"), "luis", "right-pw-123"); err != nil {
		t.Fatalf("login: %v", err)
	}
	c := sessionCookie(w)
	if c == nil || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || len(c.Value) != 64 {
		t.Fatalf("cookie: %+v", c)
	}
	if got := a.UserID(request("10.0.0.1", c)); got != id {
		t.Fatalf("session user: %d", got)
	}
	if a.UserID(request("10.0.0.1", &http.Cookie{Name: CookieName, Value: "forged"})) != 0 {
		t.Fatal("a made-up session was accepted")
	}

	// Require: without a session 401, with one the user is in the context.
	var seen int64
	h := a.Require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = FromContext(r.Context()) }))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, request("10.0.0.1"))
	if rec.Code != 401 || seen != 0 {
		t.Fatalf("without a session: %d %d", rec.Code, seen)
	}
	h.ServeHTTP(httptest.NewRecorder(), request("10.0.0.1", c))
	if seen != id {
		t.Fatalf("with a session: %d", seen)
	}

	out := httptest.NewRecorder()
	a.Logout(out, request("10.0.0.1", c))
	if a.UserID(request("10.0.0.1", c)) != 0 {
		t.Fatal("the session still works after logout")
	}
	if gone := sessionCookie(out); gone == nil || gone.MaxAge >= 0 {
		t.Fatalf("logout cookie: %+v", gone)
	}

	plain, _ := newAuth(t, false)
	plain.Register(nil, "anna", "anna-pw-123", store.RoleUser)
	w = httptest.NewRecorder()
	plain.Login(w, request("10.0.0.1"), "anna", "anna-pw-123")
	if c := sessionCookie(w); c == nil || c.Secure {
		t.Fatalf("cookie without TLS: %+v", c)
	}
}

// Five wrong passwords lock that account from that address for a while –
// not the account for everyone, and not everyone behind the same proxy.
func TestLoginLockout(t *testing.T) {
	a, _ := newAuth(t, false)
	a.Register(nil, "luis", "right-pw-123", store.RoleAdmin)
	a.Register(nil, "anna", "anna-pw-123", store.RoleUser)
	for i := 0; i < maxFailures; i++ {
		if err := a.Login(httptest.NewRecorder(), request("10.0.0.9"), "luis", "wrong"); !errors.Is(err, ErrInvalid) {
			t.Fatalf("try %d: %v", i, err)
		}
	}
	if err := a.Login(httptest.NewRecorder(), request("10.0.0.9"), "luis", "right-pw-123"); !errors.Is(err, ErrLocked) {
		t.Fatalf("after five wrong passwords: %v", err)
	}
	if err := a.Login(httptest.NewRecorder(), request("10.0.0.9"), "anna", "anna-pw-123"); err != nil {
		t.Fatalf("another account from the same address: %v", err)
	}
	if err := a.Login(httptest.NewRecorder(), request("10.0.0.10"), "luis", "right-pw-123"); err != nil {
		t.Fatalf("the account from another address: %v", err)
	}
	// An unknown name answers like a wrong password.
	if err := a.Login(httptest.NewRecorder(), request("10.0.0.9"), "nobody", "x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("unknown name: %v", err)
	}
}

// A locked account: the right password says so (a wrong one does not), no
// session starts, and its sessions end.
func TestDisabledAccount(t *testing.T) {
	a, st := newAuth(t, false)
	id, _ := a.Register(nil, "anna", "anna-pw-123", store.RoleUser)
	w := httptest.NewRecorder()
	a.Login(w, request("10.0.0.1"), "anna", "anna-pw-123")
	c := sessionCookie(w)
	u, _ := st.User(id)
	u.Disabled = true
	if err := st.UpdateUser(u); err != nil {
		t.Fatal(err)
	}
	if a.UserID(request("10.0.0.1", c)) != 0 {
		t.Fatal("a locked account's session still works")
	}
	if err := a.Login(httptest.NewRecorder(), request("10.0.0.1"), "anna", "wrong"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong password of a locked account: %v", err)
	}
	w = httptest.NewRecorder()
	if err := a.Login(w, request("10.0.0.1"), "anna", "anna-pw-123"); !errors.Is(err, ErrDisabled) || sessionCookie(w) != nil {
		t.Fatalf("right password of a locked account: %v", err)
	}
	if err := a.LoginUser(httptest.NewRecorder(), id); !errors.Is(err, ErrDisabled) {
		t.Fatalf("provider login of a locked account: %v", err)
	}
}
