// Package server wires the HTTP API and the embedded web interface together.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
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
	"github.com/heimdeck/heimdeck/internal/store"
)

type Server struct {
	store  *store.Store
	auth   *auth.Auth
	docker *docker.Client
	web    fs.FS

	mu      sync.Mutex
	widgets map[string]cached[*integrations.Widget] // key: tile id + language
	latest  map[int64]TileStatus
	recheck chan int64
	// unlocked holds, per login session, the protected groups opened with their password.
	unlocked map[string]map[int64]time.Time
}

type cached[T any] struct {
	at  time.Time
	val T
	err error
}

const (
	widgetTTL = 10 * time.Second
	unlockTTL = 12 * time.Hour
)

func New(s *store.Store, a *auth.Auth, d *docker.Client, web fs.FS) *Server {
	return &Server{store: s, auth: a, docker: d, web: web,
		widgets: map[string]cached[*integrations.Widget]{}, latest: map[int64]TileStatus{},
		recheck: make(chan int64, 16), unlocked: map[string]map[int64]time.Time{}}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", s.handleState)
	mux.HandleFunc("POST /api/setup", s.handleSetup)
	mux.HandleFunc("POST /api/login", s.handleLogin)
	mux.HandleFunc("POST /api/logout", s.handleLogout)

	api := http.NewServeMux()
	api.HandleFunc("GET /api/dashboard", s.handleDashboard)
	api.HandleFunc("PUT /api/settings", s.handleSaveSettings)
	api.HandleFunc("PUT /api/password", s.handlePassword)
	api.HandleFunc("POST /api/groups", s.handleCreateGroup)
	api.HandleFunc("PUT /api/groups/{id}", s.handleUpdateGroup)
	api.HandleFunc("POST /api/groups/{id}/unlock", s.handleUnlockGroup)
	api.HandleFunc("POST /api/groups/{id}/lock", s.handleLockGroup)
	api.HandleFunc("DELETE /api/groups/{id}", s.handleDeleteGroup)
	api.HandleFunc("POST /api/tiles", s.handleSaveTile)
	api.HandleFunc("PUT /api/tiles/{id}", s.handleSaveTile)
	api.HandleFunc("DELETE /api/tiles/{id}", s.handleDeleteTile)
	api.HandleFunc("POST /api/tiles/test", s.handleTestTile)
	api.HandleFunc("GET /api/tiles/{id}/widget", s.handleWidget)
	api.HandleFunc("POST /api/tiles/{id}/action", s.handleAction)
	api.HandleFunc("PUT /api/layout", s.handleLayout)
	api.HandleFunc("GET /api/status", s.handleStatus)
	api.HandleFunc("GET /api/uptime", s.handleUptime)
	api.HandleFunc("GET /api/search", s.handleSearch)
	api.HandleFunc("GET /api/discover", s.handleDiscover)
	api.HandleFunc("GET /api/export", s.handleExport)
	api.HandleFunc("POST /api/import", s.handleImport)
	mux.Handle("/api/", s.auth.Require(api))

	mux.Handle("/", s.spa())
	return securityHeaders(withLang(requireJSON(mux)))
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
	return &integrations.Instance{BaseURL: base, PublicURL: t.URL, Values: vals, Client: integrations.NewClient(t.Insecure), Lang: lang}
}

func (s *Server) forget(id int64) {
	s.mu.Lock()
	for _, lang := range []string{i18n.DE, i18n.EN} {
		delete(s.widgets, widgetKey(id, lang))
	}
	delete(s.latest, id)
	s.mu.Unlock()
}

func widgetKey(id int64, lang string) string { return fmt.Sprintf("%d:%s", id, lang) }

func logErr(what string, err error) {
	if err != nil {
		log.Printf("%s: %v", what, err)
	}
}

func timeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
