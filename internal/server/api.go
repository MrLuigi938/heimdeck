package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/i18n"
	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/store"
)

// ---- auth ----

func (s *Server) handleState(w http.ResponseWriter, r *http.Request) {
	hasUser, err := s.store.HasUser()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	settings, _ := s.store.Settings()
	out := map[string]any{"setup": !hasUser, "authenticated": false, "language": settings["language"]}
	if id := s.auth.UserID(r); id != 0 {
		name, _ := s.store.Username(id)
		out["authenticated"], out["username"] = true, name
	}
	writeJSON(w, out)
}

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Language string `json:"language"`
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if err := readJSON(r, &c); err != nil {
		writeErr(w, 400, err)
		return
	}
	if hasUser, _ := s.store.HasUser(); hasUser {
		writeErr(w, 409, errors.New("Einrichtung wurde bereits abgeschlossen"))
		return
	}
	c.Username = strings.TrimSpace(c.Username)
	if c.Username == "" || len(c.Password) < 8 {
		writeErr(w, 400, errors.New("Benutzername angeben und Passwort mit mindestens 8 Zeichen wählen"))
		return
	}
	if err := s.auth.Setup(w, c.Username, c.Password); err != nil {
		writeErr(w, 500, err)
		return
	}
	lang := i18n.Normalize(c.Language)
	logErr("Sprache speichern", s.store.SaveSettings(map[string]string{"language": lang}))
	s.seed(lang)
	writeJSON(w, map[string]bool{"ok": true})
}

// seed creates starter groups so a fresh dashboard is not empty.
func (s *Server) seed(lang string) {
	for _, name := range []string{"Medien", "Downloads", "System"} {
		s.store.CreateGroup(i18n.T(lang, name), 0)
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if err := readJSON(r, &c); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.auth.Login(w, r, strings.TrimSpace(c.Username), c.Password); err != nil {
		code := 401
		if errors.Is(err, auth.ErrLocked) {
			code = 429
		} else if !errors.Is(err, auth.ErrInvalid) {
			code = 500
		}
		writeErr(w, code, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	s.auth.Logout(w, r)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	name, err := s.store.Username(s.auth.UserID(r))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if len(body.New) < 8 {
		writeErr(w, 400, errors.New("Das neue Passwort braucht mindestens 8 Zeichen"))
		return
	}
	if err := s.auth.Login(httptestDiscard{}, r, name, body.Current); err != nil {
		writeErr(w, 403, errors.New("Aktuelles Passwort ist falsch"))
		return
	}
	hash, _ := auth.HashPassword(body.New)
	if err := s.store.SetPassword(name, hash); err != nil {
		writeErr(w, 500, err)
		return
	}
	// All sessions were cleared; log the current browser back in.
	s.auth.Login(w, r, name, body.New)
	writeJSON(w, map[string]bool{"ok": true})
}

// httptestDiscard swallows the cookie set by a verification-only login.
type httptestDiscard struct{}

func (httptestDiscard) Header() http.Header         { return http.Header{} }
func (httptestDiscard) Write(b []byte) (int, error) { return len(b), nil }
func (httptestDiscard) WriteHeader(int)             {}

// ---- dashboard ----

type tileDTO struct {
	store.Tile
	SecretsSet []string `json:"secretsSet"`
}

func toDTO(t store.Tile) tileDTO {
	d := tileDTO{Tile: t, SecretsSet: []string{}}
	for k, v := range t.Secrets {
		if v != "" {
			d.SecretsSet = append(d.SecretsSet, k)
		}
	}
	return d
}

type groupDTO struct {
	store.Group
	Locked bool      `json:"locked"`
	Tiles  []tileDTO `json:"tiles"`
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	settings, err := s.store.Settings()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	groups, err := s.store.Groups()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	tiles, err := s.store.Tiles()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	locked, err := s.lockedGroups(r)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	byGroup := map[int64][]tileDTO{}
	for _, t := range tiles {
		if !locked[t.GroupID] {
			byGroup[t.GroupID] = append(byGroup[t.GroupID], toDTO(t))
		}
	}
	out := make([]groupDTO, 0, len(groups))
	for _, g := range groups {
		ts := byGroup[g.ID]
		if ts == nil {
			ts = []tileDTO{}
		}
		out = append(out, groupDTO{Group: g, Locked: locked[g.ID], Tiles: ts})
	}
	boards, err := s.store.Boards()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	name, _ := s.store.Username(s.auth.UserID(r))
	writeJSON(w, map[string]any{
		"settings":     settings,
		"boards":       boards,
		"groups":       out,
		"integrations": integrations.All(langOf(r)),
		"docker":       s.docker != nil,
		"username":     name,
	})
}

var allowedSettings = map[string]bool{
	"title": true, "theme": true, "accent": true, "background": true,
	"columns": true, "searchEngine": true, "openInNewTab": true, "showGreeting": true,
	"localHosts": true, "showUptime": true, "language": true, "uniformTiles": true,
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var m map[string]string
	if err := readJSON(r, &m); err != nil {
		writeErr(w, 400, err)
		return
	}
	for k := range m {
		if !allowedSettings[k] {
			delete(m, k)
		}
	}
	if err := s.store.SaveSettings(m); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- groups ----

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string `json:"name"`
		BoardID int64  `json:"boardId"`
	}
	if err := readJSON(r, &body); err != nil || strings.TrimSpace(body.Name) == "" {
		writeErr(w, 400, errors.New("Name fehlt"))
		return
	}
	g, err := s.store.CreateGroup(strings.TrimSpace(body.Name), body.BoardID)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, g)
}

// ---- tiles ----

type tileInput struct {
	store.Tile
	Secrets map[string]string `json:"secrets"`
	// Monitor is a pointer so a request without the field keeps monitoring on.
	Monitor *bool `json:"monitor"`
}

// buildTile merges the submitted form with the stored tile. Empty secret
// fields keep the stored value, so keys never have to be sent back to the browser.
func (s *Server) buildTile(in tileInput) (store.Tile, error) {
	t := in.Tile
	t.Monitor = in.Monitor == nil || *in.Monitor
	t.Name = strings.TrimSpace(t.Name)
	t.URL = strings.TrimSpace(t.URL)
	if t.Name == "" {
		return t, errors.New("Name fehlt")
	}
	t.URL = normalizeURL(t.URL, "http://")
	t.APIURL = normalizeURL(t.APIURL, "http://")
	// Public addresses usually sit behind a reverse proxy with TLS.
	t.ExternalURL = normalizeURL(t.ExternalURL, "https://")
	if t.Integration == "" {
		t.Integration = "link"
	}
	integ, ok := integrations.Get(t.Integration)
	if !ok {
		return t, fmt.Errorf("unbekannter Dienst-Typ: %s", t.Integration)
	}
	if t.Config == nil {
		t.Config = map[string]string{}
	}
	t.Secrets = map[string]string{}
	if t.ID != 0 {
		old, err := s.store.Tile(t.ID)
		if err != nil {
			return t, err
		}
		if old.Integration == t.Integration {
			for k, v := range old.Secrets {
				t.Secrets[k] = v
			}
		}
	}
	for k, v := range in.Secrets {
		if v != "" {
			t.Secrets[k] = v
		}
	}
	// Keep only fields the integration knows about.
	cfg := map[string]string{}
	sec := map[string]string{}
	for _, f := range integ.Def().Fields {
		if f.Secret {
			if v := t.Secrets[f.Key]; v != "" {
				sec[f.Key] = v
			}
		} else if v := strings.TrimSpace(t.Config[f.Key]); v != "" {
			cfg[f.Key] = v
		}
	}
	t.Config, t.Secrets = cfg, sec
	return t, nil
}

func normalizeURL(u, scheme string) string {
	u = strings.TrimSpace(u)
	if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		u = scheme + u
	}
	return u
}

func (s *Server) handleSaveTile(w http.ResponseWriter, r *http.Request) {
	var in tileInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	if r.Method == http.MethodPut {
		id, err := pathID(r)
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		in.ID = id
	} else {
		in.ID = 0
	}
	t, err := s.buildTile(in)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if t.GroupID == 0 {
		writeErr(w, 400, errors.New("Gruppe fehlt"))
		return
	}
	if t.ID != 0 {
		if _, code, err := s.accessibleTile(r, t.ID); err != nil {
			writeErr(w, code, err)
			return
		}
	}
	if ok, _ := s.groupUnlocked(r, t.GroupID); !ok {
		writeErr(w, 403, errLocked)
		return
	}
	if err := s.store.SaveTile(&t); err != nil {
		writeErr(w, 500, err)
		return
	}
	s.forget(t.ID)
	s.requestCheck(t.ID)
	writeJSON(w, toDTO(t))
}

func (s *Server) handleDeleteTile(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if _, code, err := s.accessibleTile(r, id); err != nil {
		writeErr(w, code, err)
		return
	}
	if err := s.store.DeleteTile(id); err != nil {
		writeErr(w, 500, err)
		return
	}
	s.forget(id)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleLayout(w http.ResponseWriter, r *http.Request) {
	var l store.Layout
	if err := readJSON(r, &l); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.store.SaveLayout(l); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// handleTestTile checks a tile's settings before saving it.
func (s *Server) handleTestTile(w http.ResponseWriter, r *http.Request) {
	var in tileInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	t, err := s.buildTile(in)
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if t.URL == "" && t.APIURL == "" {
		writeJSON(w, map[string]any{"ok": false, "error": "Bitte eine Adresse angeben"})
		return
	}
	integ, _ := integrations.Get(t.Integration)
	for _, f := range integ.Def().Fields {
		if f.Required && t.Config[f.Key] == "" && t.Secrets[f.Key] == "" {
			writeJSON(w, map[string]any{"ok": false, "error": tr(r, "„%s“ fehlt", tr(r, f.Label))})
			return
		}
	}
	ctx, cancel := timeout(r, 15*time.Second)
	defer cancel()
	inst := s.instance(t, langOf(r))
	start := time.Now()
	if !integ.Def().HasWidget {
		st := ping(ctx, inst.BaseURL, t.Insecure)
		if !st.Online {
			writeJSON(w, map[string]any{"ok": false, "error": "Dienst nicht erreichbar"})
			return
		}
		writeJSON(w, map[string]any{"ok": true, "ms": st.MS})
		return
	}
	if tester, ok := integ.(integrations.Tester); ok {
		err = tester.Test(ctx, inst)
	}
	var widget *integrations.Widget
	if err == nil {
		widget, err = integ.Fetch(ctx, inst)
	}
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "ms": time.Since(start).Milliseconds(), "widget": widget})
}

// ---- live data ----

func (s *Server) widget(ctx context.Context, t store.Tile, lang string) (*integrations.Widget, error) {
	key := widgetKey(t.ID, lang)
	s.mu.Lock()
	c, ok := s.widgets[key]
	s.mu.Unlock()
	if ok && time.Since(c.at) < widgetTTL {
		return c.val, c.err
	}
	integ, ok := integrations.Get(t.Integration)
	if !ok {
		return nil, errors.New("unbekannter Dienst-Typ")
	}
	w, err := integ.Fetch(ctx, s.instance(t, lang))
	s.mu.Lock()
	s.widgets[key] = cached[*integrations.Widget]{at: time.Now(), val: w, err: err}
	s.mu.Unlock()
	return w, err
}

func (s *Server) handleWidget(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	t, code, err := s.accessibleTile(r, id)
	if err != nil {
		writeErr(w, code, err)
		return
	}
	ctx, cancel := timeout(r, 15*time.Second)
	defer cancel()
	data, err := s.widget(ctx, t, langOf(r))
	if err != nil {
		writeJSON(w, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"widget": data})
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	var body struct {
		Action string            `json:"action"`
		Params map[string]string `json:"params"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	t, code, err := s.accessibleTile(r, id)
	if err != nil {
		writeErr(w, code, err)
		return
	}
	integ, _ := integrations.Get(t.Integration)
	actioner, ok := integ.(integrations.Actioner)
	if !ok {
		writeErr(w, 400, errors.New("dieser Dienst unterstützt keine Aktionen"))
		return
	}
	ctx, cancel := timeout(r, 15*time.Second)
	defer cancel()
	if err := actioner.Action(ctx, s.instance(t, langOf(r)), body.Action, body.Params); err != nil {
		writeErr(w, 502, err)
		return
	}
	s.forget(id)
	writeJSON(w, map[string]bool{"ok": true})
}

// handleImage passes covers and thumbnails through, so the browser never needs
// the service's API key.
func (s *Server) handleImage(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	t, code, err := s.accessibleTile(r, id)
	if err != nil {
		writeErr(w, code, err)
		return
	}
	integ, _ := integrations.Get(t.Integration)
	src, ok := integ.(integrations.ImageSource)
	if !ok {
		writeErr(w, 404, errors.New("nicht gefunden"))
		return
	}
	ctx, cancel := timeout(r, 15*time.Second)
	defer cancel()
	resp, err := integrations.FetchImage(ctx, src, s.instance(t, langOf(r)), r.URL.Query().Get("p"))
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.Header().Set("Cache-Control", "private, max-age=86400")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	io.Copy(w, io.LimitReader(resp.Body, 10<<20))
}

// configured reports whether all required fields of a tile are filled in.
// Tiles created as plain links before their service got an integration are not.
func configured(integ integrations.Integration, t store.Tile) bool {
	for _, f := range integ.Def().Fields {
		if f.Required && t.Config[f.Key] == "" && t.Secrets[f.Key] == "" {
			return false
		}
	}
	return true
}

type searchGroup struct {
	TileID int64               `json:"tileId"`
	Name   string              `json:"name"`
	Icon   string              `json:"icon"`
	Items  []integrations.Item `json:"items"`
	Error  string              `json:"error,omitempty"`
}

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if len(q) < 2 {
		writeJSON(w, []searchGroup{})
		return
	}
	tiles, err := s.accessibleTiles(r)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	// Only search the dashboard that is currently open.
	if board, _ := strconv.ParseInt(r.URL.Query().Get("board"), 10, 64); board != 0 {
		groups, _ := s.store.Groups()
		onBoard := map[int64]bool{}
		for _, g := range groups {
			onBoard[g.ID] = g.BoardID == board
		}
		filtered := tiles[:0]
		for _, t := range tiles {
			if onBoard[t.GroupID] {
				filtered = append(filtered, t)
			}
		}
		tiles = filtered
	}
	ctx, cancel := timeout(r, 8*time.Second)
	defer cancel()
	var mu sync.Mutex
	var wg sync.WaitGroup
	out := []searchGroup{}
	for _, t := range tiles {
		integ, ok := integrations.Get(t.Integration)
		if !ok {
			continue
		}
		searcher, ok := integ.(integrations.Searcher)
		if !ok || !configured(integ, t) {
			continue
		}
		wg.Add(1)
		go func(t store.Tile) {
			defer wg.Done()
			items, err := searcher.Search(ctx, s.instance(t, langOf(r)), q)
			g := searchGroup{TileID: t.ID, Name: t.Name, Icon: t.Icon, Items: items}
			if err != nil {
				g.Error = err.Error()
			}
			mu.Lock()
			out = append(out, g)
			mu.Unlock()
		}(t)
	}
	wg.Wait()
	// Libraries you already own first (Jellyfin), then request services, then the rest.
	rank := map[string]int{"jellyfin": 0, "jellyseerr": 1}
	kind := map[int64]string{}
	for _, t := range tiles {
		kind[t.ID] = t.Integration
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, ok := rank[kind[out[i].TileID]]
		if !ok {
			ri = 9
		}
		rj, ok := rank[kind[out[j].TileID]]
		if !ok {
			rj = 9
		}
		return ri < rj
	})
	writeJSON(w, out)
}

func (s *Server) handleDiscover(w http.ResponseWriter, r *http.Request) {
	if s.docker == nil {
		writeErr(w, 404, errors.New("Kein Docker-Socket eingebunden – siehe Anleitung in der docker-compose.yml"))
		return
	}
	ctx, cancel := timeout(r, 8*time.Second)
	defer cancel()
	sugg, err := s.docker.Suggest(ctx, requestHost(r))
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	// Mark containers that already have a tile with the same URL.
	tiles, _ := s.store.Tiles()
	existing := map[string]bool{}
	for _, t := range tiles {
		existing[strings.TrimRight(t.URL, "/")] = true
	}
	type item struct {
		Suggestion any  `json:"suggestion"`
		Exists     bool `json:"exists"`
	}
	out := []item{}
	for _, sg := range sugg {
		sg.Group = tr(r, sg.Group)
		out = append(out, item{Suggestion: sg, Exists: sg.URL != "" && existing[strings.TrimRight(sg.URL, "/")]})
	}
	writeJSON(w, out)
}

// ---- backup ----

type exportTile struct {
	Name        string            `json:"name"`
	URL         string            `json:"url"`
	ExternalURL string            `json:"externalUrl,omitempty"`
	APIURL      string            `json:"apiUrl,omitempty"`
	Icon        string            `json:"icon,omitempty"`
	Description string            `json:"description,omitempty"`
	Integration string            `json:"integration"`
	Config      map[string]string `json:"config,omitempty"`
	Insecure    bool              `json:"insecure,omitempty"`
	ShowWidget  bool              `json:"showWidget"`
	NoMonitor   bool              `json:"noMonitor,omitempty"`
}

type exportData struct {
	Version  int               `json:"version"`
	Settings map[string]string `json:"settings"`
	Groups   []exportGroup     `json:"groups"`
}

type exportGroup struct {
	Board      string       `json:"board,omitempty"`
	Layout     string       `json:"layout,omitempty"`
	Name       string       `json:"name"`
	Visibility string       `json:"visibility,omitempty"`
	Tiles      []exportTile `json:"tiles"`
}

// handleExport writes groups, tiles and settings. API keys are left out on purpose.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.store.Settings()
	groups, err := s.store.Groups()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	tiles, _ := s.store.Tiles()
	locked, _ := s.lockedGroups(r)
	boards, _ := s.store.Boards()
	boardNames := map[int64]string{}
	for _, b := range boards {
		boardNames[b.ID] = b.Name
	}
	data := exportData{Version: 1, Settings: settings}
	for _, g := range groups {
		// Locked groups stay out of the backup; protected ones are restored as hidden (no passwords in backups).
		if locked[g.ID] {
			continue
		}
		eg := exportGroup{Name: g.Name, Board: boardNames[g.BoardID], Layout: g.Layout, Tiles: []exportTile{}}
		if g.Visibility != store.Visible {
			eg.Visibility = store.Hidden
		}
		for _, t := range tiles {
			if t.GroupID == g.ID {
				eg.Tiles = append(eg.Tiles, exportTile{Name: t.Name, URL: t.URL, ExternalURL: t.ExternalURL, APIURL: t.APIURL, Icon: t.Icon,
					Description: t.Description, Integration: t.Integration, Config: t.Config, Insecure: t.Insecure, ShowWidget: t.ShowWidget, NoMonitor: !t.Monitor})
			}
		}
		data.Groups = append(data.Groups, eg)
	}
	w.Header().Set("Content-Disposition", `attachment; filename="heimdeck-backup.json"`)
	writeJSON(w, data)
}

// handleImport adds the groups and tiles of a backup to the dashboard.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	var data exportData
	if err := readJSON(r, &data); err != nil || data.Version != 1 {
		writeErr(w, 400, errors.New("keine gültige Heimdeck-Sicherung"))
		return
	}
	if len(data.Settings) > 0 {
		for k := range data.Settings {
			if !allowedSettings[k] {
				delete(data.Settings, k)
			}
		}
		logErr("import settings", s.store.SaveSettings(data.Settings))
	}
	count := 0
	boards, _ := s.store.Boards()
	boardIDs := map[string]int64{}
	for _, b := range boards {
		boardIDs[b.Name] = b.ID
	}
	for _, eg := range data.Groups {
		boardID := boardIDs[eg.Board]
		if eg.Board != "" && boardID == 0 {
			b := store.Board{Name: eg.Board}
			if err := s.store.SaveBoard(&b); err == nil {
				boardID = b.ID
				boardIDs[eg.Board] = b.ID
			}
		}
		g, err := s.store.CreateGroup(eg.Name, boardID)
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		if eg.Visibility == store.Hidden || eg.Layout == "uniform" || eg.Layout == "auto" {
			if eg.Visibility == store.Hidden {
				g.Visibility = store.Hidden
			}
			if eg.Layout == "uniform" || eg.Layout == "auto" {
				g.Layout = eg.Layout
			}
			logErr("import group", s.store.UpdateGroup(g))
		}
		for _, et := range eg.Tiles {
			monitor := !et.NoMonitor
			t, err := s.buildTile(tileInput{Tile: store.Tile{
				GroupID: g.ID, Name: et.Name, URL: et.URL, ExternalURL: et.ExternalURL, APIURL: et.APIURL, Icon: et.Icon, Description: et.Description,
				Integration: et.Integration, Config: et.Config, Insecure: et.Insecure, ShowWidget: et.ShowWidget,
			}, Monitor: &monitor})
			if err != nil {
				continue
			}
			if s.store.SaveTile(&t) == nil {
				count++
			}
		}
	}
	writeJSON(w, map[string]any{"ok": true, "tiles": count})
}
