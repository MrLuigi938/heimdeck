// Package auth handles the login, session cookies and a simple brute-force guard.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
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
	until time.Time
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
	ip := clientIP(r)
	a.mu.Lock()
	if f := a.failures[ip]; f != nil && time.Now().Before(f.until) {
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
		f := a.failures[ip]
		if f == nil {
			f = &attempt{}
			a.failures[ip] = f
		}
		f.count++
		if f.count >= maxFailures {
			f.count = 0
			f.until = time.Now().Add(lockout)
		}
		a.mu.Unlock()
		return ErrInvalid
	}
	a.mu.Lock()
	delete(a.failures, ip)
	a.mu.Unlock()
	return a.startSession(w, id)
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

// Setup creates the first (and only) user and logs them in.
func (a *Auth) Setup(w http.ResponseWriter, username, password string) error {
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	if err := a.store.CreateUser(username, hash); err != nil {
		return err
	}
	id, _, err := a.store.UserByName(username)
	if err != nil {
		return err
	}
	return a.startSession(w, id)
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
