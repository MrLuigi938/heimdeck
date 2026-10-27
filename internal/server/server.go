// Package server wires the HTTP API and the embedded web interface together.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/docker"
	"github.com/heimdeck/heimdeck/internal/i18n"
	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/logging"
	"github.com/heimdeck/heimdeck/internal/store"
)

type Server struct {
	store   *store.Store
	auth    *auth.Auth
	docker  *docker.Client
	updates *docker.Updates
	web     fs.FS

	mu           sync.Mutex
	widgets      map[string]cached[*integrations.Widget] // key: tile id + language
	fetching     map[string]chan struct{}                // widget fetches in progress
	widgetGen    map[string]int                          // bumped by forget, drops outdated fetches
	favicons     map[string]favicon                      // key: tile address
	faviconFetch map[string]chan struct{}                // favicon lookups in progress
	latest       map[int64]TileStatus
	outages      map[string]*outage // key: tile id + "int"/"ext"
	recheck      chan int64
	// unlocked holds, per login session, the protected groups opened with their password.
	unlocked map[string]map[int64]time.Time
	version  string       // set at build time, "dev" otherwise
	update   *releaseInfo // newer release found by CheckUpdates, nil if none
	// updateChecked is when GitHub last answered; started when the server came up.
	updateChecked time.Time
	started       time.Time
}

type cached[T any] struct {
	at  time.Time
	val T
	err error
}

const (
	widgetTTL    = 10 * time.Second
	widgetErrTTL = 30 * time.Second // unreachable services are asked less often
	unlockTTL    = 12 * time.Hour
)

func New(s *store.Store, a *auth.Auth, d *docker.Client, web fs.FS) *Server {
	srv := &Server{store: s, auth: a, docker: d, updates: docker.NewUpdates(), web: web,
		widgets: map[string]cached[*integrations.Widget]{}, fetching: map[string]chan struct{}{}, widgetGen: map[string]int{}, favicons: map[string]favicon{}, faviconFetch: map[string]chan struct{}{}, latest: map[int64]TileStatus{}, outages: map[string]*outage{},
		recheck: make(chan int64, 16), unlocked: map[string]map[int64]time.Time{}, started: time.Now()}
	integrations.Register(dockerTile{srv})
	srv.migrateSettings()
	return srv
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/setup", s.handleSetup)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)
	// Invitation links: the token in the address replaces the login.
	mux.HandleFunc("GET /api/invite/{token}", s.handleInviteInfo)
	mux.HandleFunc("POST /api/invite/{token}", s.handleJoin)
	mux.HandleFunc("GET /api/oidc/login", s.handleSSOLogin)
	mux.HandleFunc("GET /api/oidc/callback", s.handleSSOCallback)
	// Messages for webhook tiles; the key in the address replaces the login.
	mux.HandleFunc("GET /hook/{key}", s.handleHook)
	mux.HandleFunc("POST /hook/{key}", s.handleHook)
	mux.HandleFunc("PUT /hook/{key}", s.handleHook)

	api := http.NewServeMux()
	api.HandleFunc("GET /api/dashboard", s.handleDashboard)
	api.HandleFunc("PUT /api/settings", s.handleSaveSettings)
	api.HandleFunc("PUT /api/password", s.handlePassword)
	api.HandleFunc("PUT /api/notify", s.handleSaveNotify)
	api.HandleFunc("POST /api/notify/test", s.handleTestNotify)
	api.HandleFunc("POST /api/notify/telegram/chats", s.handleTelegramChats)
	api.HandleFunc("POST /api/boards", s.handleCreateBoard)
	api.HandleFunc("PUT /api/boards/{id}", s.handleUpdateBoard)
	api.HandleFunc("DELETE /api/boards/{id}", s.handleDeleteBoard)
	api.HandleFunc("POST /api/groups", s.handleCreateGroup)
	api.HandleFunc("PUT /api/groups/{id}", s.handleUpdateGroup)
	api.HandleFunc("POST /api/groups/{id}/unlock", s.handleUnlockGroup)
	api.HandleFunc("POST /api/groups/{id}/lock", s.handleLockGroup)
	api.HandleFunc("DELETE /api/groups/{id}", s.handleDeleteGroup)
	api.HandleFunc("POST /api/tiles", s.handleSaveTile)
	api.HandleFunc("PUT /api/tiles/{id}", s.handleSaveTile)
	api.HandleFunc("DELETE /api/tiles/{id}", s.handleDeleteTile)
	api.HandleFunc("POST /api/tiles/test", s.handleTestTile)
	api.HandleFunc("POST /api/tiles/parts", s.handleTileParts)
	api.HandleFunc("POST /api/tiles/choices", s.handleChoices)
	api.HandleFunc("GET /api/tiles/{id}/widget", s.handleWidget)
	api.HandleFunc("POST /api/tiles/{id}/action", s.handleAction)
	api.HandleFunc("GET /api/tiles/{id}/image", s.handleImage)
	api.HandleFunc("GET /api/tiles/{id}/favicon", s.handleFavicon)
	api.HandleFunc("PUT /api/layout", s.handleLayout)
	api.HandleFunc("GET /api/status", s.handleStatus)
	api.HandleFunc("GET /api/uptime", s.handleUptime)
	api.HandleFunc("GET /api/search", s.handleSearch)
	api.HandleFunc("GET /api/discover", s.adminOnly(s.handleDiscover))
	api.HandleFunc("GET /api/export", s.handleExport)
	api.HandleFunc("POST /api/import", s.handleImport)
	api.HandleFunc("GET /api/debug/goroutines", s.adminOnly(s.handleGoroutines))
	// Accounts (admin) and sharing dashboards (everyone).
	api.HandleFunc("GET /api/users", s.adminOnly(s.handleUsers))
	api.HandleFunc("POST /api/users", s.adminOnly(s.handleCreateUser))
	api.HandleFunc("PUT /api/users/{id}", s.adminOnly(s.handleUpdateUser))
	api.HandleFunc("DELETE /api/users/{id}", s.adminOnly(s.handleDeleteUser))
	api.HandleFunc("POST /api/invites", s.adminOnly(s.handleCreateInvite))
	api.HandleFunc("DELETE /api/invites/{id}", s.adminOnly(s.handleDeleteInvite))
	api.HandleFunc("GET /api/sso", s.adminOnly(s.handleGetSSO))
	api.HandleFunc("PUT /api/sso", s.adminOnly(s.handleSaveSSO))
	api.HandleFunc("DELETE /api/account/sso", s.handleUnlinkSSO)
	api.HandleFunc("GET /api/people", s.handlePeople)
	api.HandleFunc("GET /api/shares", s.handleShares)
	api.HandleFunc("PUT /api/boards/{id}/shares/{user}", s.handleShareBoard)
	api.HandleFunc("DELETE /api/boards/{id}/shares/{user}", s.handleUnshareBoard)
	api.HandleFunc("POST /api/boards/{id}/accept", s.handleAcceptShare)
	mux.Handle("/api/", s.auth.Require(api))

	mux.Handle("/", s.spa())
	return logRequests(securityHeaders(withLang(requireJSON(mux))))
}

// spa serves the built frontend and falls back to index.html for client routes.
func (s *Server) spa() http.Handler {
	files := http.FileServerFS(s.web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(s.web, p); err == nil {
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				files.ServeHTTP(w, r)
				return
			}
		}
		w.Header().Set("Cache-Control", "no-cache")
		http.ServeFileFS(w, r, s.web, "index.html")
	})
}

// requireJSON blocks cross-site form posts: browsers cannot send a JSON
// content type to another origin without a CORS preflight, which we never allow.
func requireJSON(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && strings.HasPrefix(r.URL.Path, "/api/") {
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				writeErr(w, http.StatusUnsupportedMediaType, errors.New("Content-Type muss application/json sein"))
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// langWriter carries the request's language so error messages written
// anywhere below can be translated.
type langWriter struct {
	http.ResponseWriter
	lang string
}

type langKey struct{}

// withLang picks the language from the X-Lang header the web interface
// sends, falling back to the browser's Accept-Language.
func withLang(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lang := r.Header.Get("X-Lang")
		if lang == "" {
			lang = r.Header.Get("Accept-Language")
		}
		lang = i18n.Normalize(lang)
		ctx := context.WithValue(r.Context(), langKey{}, lang)
		next.ServeHTTP(&langWriter{ResponseWriter: w, lang: lang}, r.WithContext(ctx))
	})
}

func langOf(r *http.Request) string {
	if l, ok := r.Context().Value(langKey{}).(string); ok {
		return l
	}
	return i18n.DE
}

func tr(r *http.Request, s string, args ...any) string { return i18n.T(langOf(r), s, args...) }

func translate(w http.ResponseWriter, msg string) string {
	if lw, ok := w.(*langWriter); ok {
		return i18n.T(lw.lang, msg)
	}
	return msg
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "same-origin")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		next.ServeHTTP(w, r)
	})
}

// ---- helpers ----

func writeJSON(w http.ResponseWriter, v any) {
	if m, ok := v.(map[string]any); ok {
		if e, ok := m["error"].(string); ok {
			m["error"] = translate(w, e)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": translate(w, err.Error())})
}

func readJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 2<<20))
	if err := dec.Decode(v); err != nil {
		return errors.New("ungültige Anfrage")
	}
	return nil
}

func pathID(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		return 0, errors.New("ungültige ID")
	}
	return id, nil
}

func requestHost(r *http.Request) string {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if strings.Contains(host, ":") { // IPv6
		host = "[" + host + "]"
	}
	return host
}

func (s *Server) instance(t store.Tile, lang string) *integrations.Instance {
	vals := map[string]string{}
	for k, v := range t.Config {
		vals[k] = v
	}
	for k, v := range t.Secrets {
		vals[k] = v
	}
	base := t.APIURL
	if base == "" {
		base = t.URL
	}
	owner := s.tileOwner(t)
	ext := s.external(owner)
	in := &integrations.Instance{BaseURL: base, PublicURL: t.URL, Values: vals, Client: integrations.NewClientFor(t.Insecure, ext), Lang: lang, TileID: t.ID, PublicOnly: ext}
	in.Others = func(kind string) []*integrations.Instance { return s.othersOf(owner, kind, lang) }
	return in
}

// othersOf returns a user's tiles of one integration, outside protected
// groups (e.g. the Sonarr and Radarr tiles for their calendar tile).
func (s *Server) othersOf(owner int64, kind, lang string) []*integrations.Instance {
	if owner == 0 {
		return nil
	}
	tiles, err := s.store.Tiles()
	if err != nil {
		return nil
	}
	owners, _ := s.store.TileOwners()
	var out []*integrations.Instance
	for _, t := range tiles {
		if t.Integration != kind || owners[t.ID] != owner {
			continue
		}
		if g, err := s.store.Group(t.GroupID); err != nil || g.Visibility == store.Protected {
			continue
		}
		out = append(out, s.instance(t, lang))
	}
	return out
}

// instanceFor is instance for a tile that is being edited in a request: an
// external account stays out of the home network even with a tile that is
// not saved yet.
func (s *Server) instanceFor(r *http.Request, t store.Tile) *integrations.Instance {
	in := s.instance(t, langOf(r))
	if s.external(uid(r)) {
		in.Client, in.PublicOnly = integrations.NewClientFor(t.Insecure, true), true
	}
	return in
}

// tileOwner returns the owner of a tile, or of the group a new tile goes into.
func (s *Server) tileOwner(t store.Tile) int64 {
	if t.ID != 0 {
		if b, err := s.store.TileBoard(t.ID); err == nil {
			return b.OwnerID
		}
	}
	if g, err := s.store.Group(t.GroupID); err == nil {
		if b, err := s.store.Board(g.BoardID); err == nil {
			return b.OwnerID
		}
	}
	return 0
}

// external reports whether a user is marked as external: their tiles do not
// reach into the home network.
func (s *Server) external(user int64) bool {
	u, err := s.store.User(user)
	return err == nil && u.External
}

func (s *Server) forget(id int64) {
	s.mu.Lock()
	for _, lang := range []string{i18n.DE, i18n.EN} {
		delete(s.widgets, widgetKey(id, lang))
		s.widgetGen[widgetKey(id, lang)]++
	}
	delete(s.latest, id)
	s.mu.Unlock()
}

func widgetKey(id int64, lang string) string { return fmt.Sprintf("%d:%s", id, lang) }

func logErr(what string, err error) {
	if err != nil {
		slog.Error(what, "fehler", logging.Redact(err.Error()))
	}
}

func timeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
