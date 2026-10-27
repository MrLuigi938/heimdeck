package server

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/oidc"
	"github.com/heimdeck/heimdeck/internal/store"
)

// Login with an identity provider (OpenID Connect): Authentik, Authelia,
// Keycloak, Pocket ID, Zitadel … The admin sets it up under user management.
// Accounts are linked by the provider's user ID ("sub"), never by name: an
// existing account is linked by its owner (log in, then "Link with SSO"), or
// a new one is made on the first login if the admin allows that.

const ssoKey = "_sso" // global setting, sealed like other secrets

type ssoConfig struct {
	Enabled      bool   `json:"enabled"`
	Issuer       string `json:"issuer"`
	ClientID     string `json:"clientId"`
	ClientSecret string `json:"clientSecret,omitempty"`
	Label        string `json:"label"`
	Scopes       string `json:"scopes"`
	PublicURL    string `json:"publicUrl"`    // Heimdeck's address, if the automatic one does not fit (proxy)
	AutoCreate   bool   `json:"autoCreate"`   // make accounts on the first login
	AllowedGroup string `json:"allowedGroup"` // only members may log in ("" = everyone the provider lets through)
	AdminGroup   string `json:"adminGroup"`   // members are admins, the others users ("" = roles stay as set here)
	PasswordOff  bool   `json:"passwordOff"`  // only the main admin may still log in with a password
	Insecure     bool   `json:"insecure"`     // self-signed certificate of the provider
}

func (s *Server) ssoConfig() ssoConfig {
	var c ssoConfig
	settings, _ := s.store.Settings()
	if plain, err := s.store.OpenSecret(settings[ssoKey]); err == nil && plain != nil {
		json.Unmarshal(plain, &c)
	}
	return c
}

func (c ssoConfig) ready() bool { return c.Enabled && c.Issuer != "" && c.ClientID != "" }

func (c ssoConfig) label() string {
	if c.Label != "" {
		return c.Label
	}
	return "SSO"
}

func (c ssoConfig) client() *http.Client { return integrations.NewClient(c.Insecure) }

func (c ssoConfig) scopes() []string {
	sc := strings.Fields(strings.ReplaceAll(c.Scopes, ",", " "))
	if len(sc) == 0 {
		sc = []string{"openid", "profile", "email", "groups"}
	}
	if !slices.Contains(sc, "openid") {
		sc = append([]string{"openid"}, sc...)
	}
	return sc
}

// callbackURL is the address the provider sends the browser back to; it has
// to be entered there exactly like this.
func (s *Server) callbackURL(r *http.Request, c ssoConfig) string {
	if u := strings.TrimRight(strings.TrimSpace(c.PublicURL), "/"); u != "" {
		return u + "/api/oidc/callback"
	}
	scheme := "http"
	if r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https") {
		scheme = "https"
	}
	return scheme + "://" + r.Host + "/api/oidc/callback"
}

// ---- settings (admin) ----

func (s *Server) handleGetSSO(w http.ResponseWriter, r *http.Request) {
	c := s.ssoConfig()
	secretSet := c.ClientSecret != ""
	c.ClientSecret = ""
	if c.Scopes == "" {
		c.Scopes = strings.Join(c.scopes(), " ")
	}
	writeJSON(w, map[string]any{"config": c, "secretSet": secretSet, "callback": s.callbackURL(r, c)})
}

func (s *Server) handleSaveSSO(w http.ResponseWriter, r *http.Request) {
	var c ssoConfig
	if err := readJSON(r, &c); err != nil {
		writeErr(w, 400, err)
		return
	}
	old := s.ssoConfig()
	if c.ClientSecret == "" {
		c.ClientSecret = old.ClientSecret
	}
	c.Issuer = strings.TrimRight(strings.TrimSpace(c.Issuer), "/")
	c.ClientID, c.Label = strings.TrimSpace(c.ClientID), strings.TrimSpace(c.Label)
	c.AllowedGroup, c.AdminGroup = strings.TrimSpace(c.AllowedGroup), strings.TrimSpace(c.AdminGroup)
	c.PublicURL = strings.TrimSpace(c.PublicURL)
	for _, u := range []string{c.Issuer, c.PublicURL} {
		if p, err := url.Parse(u); u != "" && (err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "") {
			writeErr(w, 400, errors.New("SSO: bitte vollständige Adressen mit http:// oder https:// angeben"))
			return
		}
	}
	if c.Enabled {
		if c.Issuer == "" || c.ClientID == "" || c.ClientSecret == "" {
			writeErr(w, 400, errors.New("SSO: Issuer-Adresse, Client-ID und Client-Secret werden gebraucht"))
			return
		}
		ctx, cancel := timeout(r, 10*time.Second)
		defer cancel()
		if _, err := oidc.Discover(ctx, c.client(), c.Issuer); err != nil {
			writeErr(w, 400, errors.New(tr(r, "SSO: %s", tr(r, err.Error()))))
			return
		}
	}
	plain, _ := json.Marshal(c)
	sealed, err := s.store.SealSecret(plain)
	if err == nil {
		err = s.store.SaveSettings(map[string]string{ssoKey: sealed})
	}
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	slog.Info("SSO-Einstellungen gespeichert", "aktiv", c.Enabled, "anbieter", c.Issuer, "neue Konten", c.AutoCreate, "passwort aus", c.PasswordOff, "von", s.username(r))
	writeJSON(w, map[string]bool{"ok": true})
}

// passwordAllowed: with "only SSO", the main admin keeps the password login
// as the way back in when the provider is down.
func (s *Server) passwordAllowed(username string) bool {
	c := s.ssoConfig()
	if !c.ready() || !c.PasswordOff {
		return true
	}
	id, _, err := s.store.UserByName(username)
	return err == nil && id == s.mainAdmin()
}

// ---- login flow ----

type ssoPending struct {
	req      oidc.Request
	redirect string
	link     int64 // linking this logged-in account instead of logging in
	at       time.Time
}

var pending = struct {
	sync.Mutex
	m map[string]ssoPending
}{m: map[string]ssoPending{}}

const ssoCookie = "heimdeck_sso"

// handleSSOLogin sends the browser to the provider. ?link=1 links the
// logged-in account instead.
func (s *Server) handleSSOLogin(w http.ResponseWriter, r *http.Request) {
	c := s.ssoConfig()
	if !c.ready() {
		ssoFail(w, r, "config")
		return
	}
	var link int64
	if r.URL.Query().Get("link") == "1" {
		if link = s.auth.UserID(r); link == 0 {
			ssoFail(w, r, "state")
			return
		}
	}
	ctx, cancel := timeout(r, 10*time.Second)
	defer cancel()
	p, err := oidc.Discover(ctx, c.client(), c.Issuer)
	if err != nil {
		slog.Warn("SSO: Anbieter nicht erreichbar", "fehler", err)
		ssoFail(w, r, "provider")
		return
	}
	req := oidc.NewRequest()
	cfg := oidc.Config{Issuer: c.Issuer, ClientID: c.ClientID, ClientSecret: c.ClientSecret, Scopes: c.scopes(), RedirectURL: s.callbackURL(r, c)}
	pending.Lock()
	for k, v := range pending.m {
		if time.Since(v.at) > 10*time.Minute {
			delete(pending.m, k)
		}
	}
	if len(pending.m) > 1000 {
		pending.Unlock()
		ssoFail(w, r, "provider")
		return
	}
	pending.m[req.State] = ssoPending{req: req, redirect: cfg.RedirectURL, link: link, at: time.Now()}
	pending.Unlock()
	// The state also goes into a cookie, so only this browser can finish the
	// login (nobody can slip their own login into someone else's browser).
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: req.State, Path: "/api/oidc", MaxAge: 600, HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.auth.SecureCookie})
	http.Redirect(w, r, p.AuthURL(cfg, req), http.StatusFound)
}

// ssoFail sends the browser back to Heimdeck with a short error code that
// the login page explains.
func ssoFail(w http.ResponseWriter, r *http.Request, code string) {
	http.Redirect(w, r, "/?sso_error="+code, http.StatusFound)
}

var usernameClean = regexp.MustCompile(`[^\p{L}\p{N}._@-]+`)

func (s *Server) handleSSOCallback(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: ssoCookie, Value: "", Path: "/api/oidc", MaxAge: -1})
	q := r.URL.Query()
	state := q.Get("state")
	cookie, _ := r.Cookie(ssoCookie)
	pending.Lock()
	p, ok := pending.m[state]
	delete(pending.m, state)
	pending.Unlock()
	if !ok || cookie == nil || subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(state)) != 1 || time.Since(p.at) > 10*time.Minute {
		ssoFail(w, r, "state")
		return
	}
	if e := q.Get("error"); e != "" {
		slog.Info("SSO: Anmeldung beim Anbieter abgebrochen", "fehler", e, "beschreibung", q.Get("error_description"))
		ssoFail(w, r, "denied")
		return
	}
	c := s.ssoConfig()
	if !c.ready() {
		ssoFail(w, r, "config")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	prov, err := oidc.Discover(ctx, c.client(), c.Issuer)
	if err != nil {
		ssoFail(w, r, "provider")
		return
	}
	cfg := oidc.Config{Issuer: c.Issuer, ClientID: c.ClientID, ClientSecret: c.ClientSecret, Scopes: c.scopes(), RedirectURL: p.redirect}
	claims, err := prov.Exchange(ctx, cfg, p.req, q.Get("code"))
	if err != nil {
		slog.Warn("SSO: Anmeldung fehlgeschlagen", "fehler", err, "von", r.RemoteAddr)
		ssoFail(w, r, "provider")
		return
	}
	subject := c.Issuer + "|" + claims.Subject
	who := firstNonEmpty(claims.PreferredUsername, claims.Email, claims.Name, claims.Subject)

	if p.link != 0 {
		if s.auth.UserID(r) != p.link {
			ssoFail(w, r, "state")
			return
		}
		if err := s.store.SetSubject(p.link, subject); err != nil {
			ssoFail(w, r, "linked_other")
			return
		}
		slog.Info("SSO verknüpft", "benutzer", s.usernameOf(p.link), "anmeldung", who)
		http.Redirect(w, r, "/?sso=linked", http.StatusFound)
		return
	}

	if c.AllowedGroup != "" && !slices.Contains(claims.Groups, c.AllowedGroup) {
		slog.Warn("SSO: nicht in der erlaubten Gruppe", "anmeldung", who, "gruppe", c.AllowedGroup)
		ssoFail(w, r, "group")
		return
	}
	u, err := s.store.UserBySubject(subject)
	if errors.Is(err, store.ErrNotFound) {
		if !c.AutoCreate {
			slog.Info("SSO: kein verknüpftes Konto", "anmeldung", who)
			ssoFail(w, r, "nouser")
			return
		}
		if u, err = s.ssoAccount(claims, subject, c); err != nil {
			slog.Error("SSO: Konto anlegen", "anmeldung", who, "fehler", err)
			ssoFail(w, r, "provider")
			return
		}
	} else if err != nil {
		ssoFail(w, r, "provider")
		return
	}
	// The admin group decides the role on every login – except for the main admin.
	if c.AdminGroup != "" && u.ID != s.mainAdmin() {
		role := store.RoleUser
		if slices.Contains(claims.Groups, c.AdminGroup) {
			role = store.RoleAdmin
		}
		if role != u.Role {
			u.Role = role
			if role == store.RoleAdmin {
				u.External = false
			}
			logErr("SSO-Rolle", s.store.UpdateUser(u))
			slog.Info("SSO: Rolle aus der Gruppe übernommen", "benutzer", u.Username, "rolle", role)
		}
	}
	if err := s.auth.LoginUser(w, u.ID); err != nil {
		ssoFail(w, r, "disabled")
		return
	}
	slog.Info("Angemeldet über SSO", "benutzer", u.Username, "von", r.RemoteAddr)
	http.Redirect(w, r, "/", http.StatusFound)
}

// ssoAccount makes a new account for a first login. Its name comes from the
// provider; if it is taken, a number is added – an existing account of the
// same name is never taken over.
func (s *Server) ssoAccount(claims oidc.Claims, subject string, c ssoConfig) (store.User, error) {
	base := firstNonEmpty(claims.PreferredUsername, strings.Split(claims.Email, "@")[0], claims.Name, "user")
	base = strings.Trim(usernameClean.ReplaceAllString(base, "-"), "-")
	if base == "" {
		base = "user"
	}
	if len(base) > 60 {
		base = base[:60]
	}
	role := store.RoleUser
	if c.AdminGroup != "" && slices.Contains(claims.Groups, c.AdminGroup) {
		role = store.RoleAdmin
	}
	// A password nobody knows: the account logs in through the provider
	// (an admin can still set one).
	buf := make([]byte, 32)
	rand.Read(buf)
	name := base
	for i := 2; ; i++ {
		id, err := s.auth.Register(nil, name, hex.EncodeToString(buf), role)
		if errors.Is(err, store.ErrNameTaken) && i < 100 {
			name = base + "-" + strconv.Itoa(i)
			continue
		}
		if err != nil {
			return store.User{}, err
		}
		if err := s.store.SetSubject(id, subject); err != nil {
			return store.User{}, err
		}
		s.newAccount(id, s.settingsFor(0)["language"])
		slog.Info("Konto über SSO angelegt", "benutzer", name, "rolle", role)
		return s.store.User(id)
	}
}

func (s *Server) usernameOf(id int64) string {
	name, _ := s.store.Username(id)
	return name
}

// handleUnlinkSSO removes the link of the own account.
func (s *Server) handleUnlinkSSO(w http.ResponseWriter, r *http.Request) {
	me := uid(r)
	if !s.passwordAllowed(s.usernameOf(me)) {
		writeErr(w, 400, errors.New("Ohne SSO könntest du dich nicht mehr anmelden – die Anmeldung mit Passwort ist abgeschaltet"))
		return
	}
	if err := s.store.SetSubject(me, ""); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// ssoInfo tells the account settings whether the login can be linked.
func (s *Server) ssoInfo(u store.User) map[string]any {
	c := s.ssoConfig()
	if !c.ready() {
		return nil
	}
	return map[string]any{"label": c.label(), "linked": u.SSO, "passwordOff": c.PasswordOff && u.ID != s.mainAdmin()}
}
