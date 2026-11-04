// Package auth handles the login, session cookies and a simple brute-force guard.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/heimdeck/heimdeck/internal/store"
)

const (
	CookieName  = "heimdeck_session"
	sessionTTL  = 30 * 24 * time.Hour
	maxFailures = 5
	lockout     = 5 * time.Minute
)

var (
	ErrInvalid = errors.New("Benutzername oder Passwort falsch")
	ErrLocked  = errors.New("Zu viele Fehlversuche – bitte in ein paar Minuten erneut versuchen")
	// ErrDisabled is only returned for the right password, so it does not help guessing.
	ErrDisabled = errors.New("Dieses Konto ist gesperrt")
)

type Auth struct {
	store *store.Store
	// SecureCookie marks the cookie as HTTPS-only (set when served behind TLS).
	SecureCookie bool

	mu       sync.Mutex
	failures map[string]*attempt
}

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("heimdeck"), bcrypt.DefaultCost)

type attempt struct {
	count int
	last  time.Time
	until time.Time
}

// failureKey: failed logins count per account and address. By address alone,
// everyone behind a reverse proxy shares one counter, and anyone could lock
// them all out with a few wrong passwords.
func failureKey(ip, username string) string {
	return ip + "|" + strings.ToLower(username)
}

type ctxKey struct{}

func New(s *store.Store, secure bool) *Auth {
	return &Auth{store: s, SecureCookie: secure, failures: map[string]*attempt{}}
}

func HashPassword(pw string) (string, error) {
	h, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	return string(h), err
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Login checks the credentials and sets the session cookie.
func (a *Auth) Login(w http.ResponseWriter, r *http.Request, username, password string) error {
	key := failureKey(clientIP(r), username)
	a.mu.Lock()
	if f := a.failures[key]; f != nil && time.Now().Before(f.until) {
		a.mu.Unlock()
		return ErrLocked
	}
	a.mu.Unlock()

	id, hash, err := a.store.UserByName(username)
	if err == nil {
		err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	} else {
		// Compare anyway so a wrong username takes as long as a wrong password.
		bcrypt.CompareHashAndPassword(dummyHash, []byte(password))
	}
	if err != nil {
		a.mu.Lock()
		now := time.Now()
		// Old entries go, so guessed names cannot fill the memory.
		if len(a.failures) > 10000 {
			for k, old := range a.failures {
				if now.Sub(old.last) > lockout && now.After(old.until) {
					delete(a.failures, k)
				}
			}
		}
		f := a.failures[key]
		if f == nil || now.Sub(f.last) > lockout {
			f = &attempt{}
			a.failures[key] = f
		}
		f.last = now
		f.count++
		if f.count >= maxFailures {
			f.count = 0
			f.until = time.Now().Add(lockout)
		}
		a.mu.Unlock()
		return ErrInvalid
	}
	a.mu.Lock()
	delete(a.failures, key)
	a.mu.Unlock()
	if u, err := a.store.User(id); err != nil || u.Disabled {
		return ErrDisabled
	}
	return a.startSession(w, id)
}

// LoginUser starts a session for an account whose identity was checked
// elsewhere (the login with an identity provider).
func (a *Auth) LoginUser(w http.ResponseWriter, userID int64) error {
	if u, err := a.store.User(userID); err != nil || u.Disabled {
		return ErrDisabled
	}
	return a.startSession(w, userID)
}

func (a *Auth) startSession(w http.ResponseWriter, userID int64) error {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return err
	}
	token := hex.EncodeToString(buf)
	exp := time.Now().Add(sessionTTL)
	if err := a.store.CreateSession(token, userID, exp); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: CookieName, Value: token, Path: "/", Expires: exp,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: a.SecureCookie,
	})
	return nil
}

// Setup creates the first user, the admin, and logs them in.
func (a *Auth) Setup(w http.ResponseWriter, username, password string) (int64, error) {
	id, err := a.Register(w, username, password, store.RoleAdmin)
	if err == nil {
		err = a.store.AdoptBoards(id)
	}
	return id, err
}

// Register creates an account and, with a ResponseWriter, logs it in.
func (a *Auth) Register(w http.ResponseWriter, username, password, role string) (int64, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return 0, err
	}
	id, err := a.store.CreateUser(username, hash, role)
	if err != nil {
		return 0, err
	}
	if w == nil {
		return id, nil
	}
	return id, a.startSession(w, id)
}

func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(CookieName); err == nil {
		a.store.DeleteSession(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: "", Path: "/", MaxAge: -1})
}

// UserID returns the logged-in user of the request, or 0.
func (a *Auth) UserID(r *http.Request) int64 {
	c, err := r.Cookie(CookieName)
	if err != nil {
		return 0
	}
	id, err := a.store.SessionUser(c.Value)
	if err != nil {
		return 0
	}
	return id
}

// FromContext returns the user that Require let through.
func FromContext(ctx context.Context) int64 {
	id, _ := ctx.Value(ctxKey{}).(int64)
	return id
}

// Require rejects requests without a valid session.
func (a *Auth) Require(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := a.UserID(r)
		if id == 0 {
			http.Error(w, `{"error":"nicht angemeldet"}`, http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, id)))
	})
}
