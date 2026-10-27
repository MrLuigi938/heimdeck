package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"runtime/debug"
	"slices"
	"sort"
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
		u, _ := s.store.User(id)
		out["authenticated"], out["username"], out["role"] = true, u.Username, u.Role
		if lang := s.settingsFor(id)["language"]; lang != "" {
			out["language"] = lang
		}
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
	if !validAccount(c.Username, c.Password) {
		writeErr(w, 400, errBadAccount)
		return
	}
	id, err := s.auth.Setup(w, c.Username, c.Password)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	lang := i18n.Normalize(c.Language)
	logErr("Sprache speichern", s.store.SaveSettings(map[string]string{"language": lang, settingsMoved: "1"}))
	s.newAccount(id, lang)
	writeJSON(w, map[string]bool{"ok": true})
}

// seed creates starter groups so a fresh dashboard is not empty.
func (s *Server) seed(lang string, board int64) {
	for _, name := range []string{"Medien", "Downloads", "System"} {
		s.store.CreateGroup(i18n.T(lang, name), board)
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
		} else if errors.Is(err, auth.ErrDisabled) {
			code = 403
		} else if !errors.Is(err, auth.ErrInvalid) {
			code = 500
		}
		slog.Warn("Anmeldung fehlgeschlagen", "benutzer", strings.TrimSpace(c.Username), "von", r.RemoteAddr, "grund", err)
		writeErr(w, code, err)
		return
	}
	slog.Debug("Angemeldet", "benutzer", strings.TrimSpace(c.Username), "von", r.RemoteAddr)
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
	// Shared: the tile is on a dashboard someone shared with the user. Its
	// settings stay with the owner (they can hold e.g. a webhook key).
	Shared bool `json:"shared,omitempty"`
}

// sharedDTO is a tile as the people it is shared with see it: what the tile
// shows and links to, without its settings.
func sharedDTO(t store.Tile) tileDTO {
	d := toDTO(t)
	d.Config, d.Hide, d.Shared = map[string]string{}, nil, true
	return d
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

// boardDTO is a dashboard as the user sees it: their own, or one shared with
// them (Owner set, read-only, buttons only with CanAct).
type boardDTO struct {
	store.Board
	Shared bool   `json:"shared,omitempty"`
	Owner  string `json:"owner,omitempty"`
	CanAct bool   `json:"canAct"`
}

func (s *Server) handleDashboard(w http.ResponseWriter, r *http.Request) {
	me := uid(r)
	user, err := s.store.User(me)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	admin := user.Role == store.RoleAdmin
	settings := publicSettings(s.settingsFor(me))
	ns := s.notifySecrets(me)
	own, err := s.store.BoardsOf(me)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if len(own) == 0 {
		// e.g. an account whose last dashboard went with an import gone wrong
		if _, err := s.firstBoard(me); err == nil {
			own, _ = s.store.BoardsOf(me)
		}
	}
	boards := []boardDTO{}
	ownBoards := map[int64]bool{}
	for _, b := range own {
		boards = append(boards, boardDTO{Board: b, CanAct: true})
		ownBoards[b.ID] = true
	}
	shares, err := s.store.SharesFor(me)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	requests := []store.Share{}
	for _, sh := range shares {
		if !sh.Accepted {
			requests = append(requests, sh)
			continue
		}
		if b, err := s.store.Board(sh.BoardID); err == nil {
			boards = append(boards, boardDTO{Board: b, Shared: true, Owner: sh.Owner, CanAct: sh.Actions})
		}
	}
	visible := map[int64]bool{}
	for _, b := range boards {
		visible[b.ID] = true
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
	boardOf := map[int64]int64{}
	for _, g := range groups {
		boardOf[g.ID] = g.BoardID
	}
	hasProxmox := false
	byGroup := map[int64][]tileDTO{}
	for _, t := range tiles {
		if !visible[boardOf[t.GroupID]] {
			continue
		}
		hasProxmox = hasProxmox || t.Integration == "proxmox"
		if !locked[t.GroupID] {
			d := toDTO(t)
			if !ownBoards[boardOf[t.GroupID]] {
				d = sharedDTO(t)
			}
			byGroup[t.GroupID] = append(byGroup[t.GroupID], d)
		}
	}
	out := []groupDTO{}
	for _, g := range groups {
		if !visible[g.BoardID] {
			continue
		}
		ts := byGroup[g.ID]
		if ts == nil {
			ts = []tileDTO{}
		}
		out = append(out, groupDTO{Group: g, Locked: locked[g.ID], Tiles: ts})
	}
	data := map[string]any{
		"settings":      settings,
		"boards":        boards,
		"groups":        out,
		"shareRequests": requests,
		"integrations":  s.integrationsFor(admin, langOf(r)),
		"docker":        admin && s.docker != nil,
		"proxmox":       admin && hasProxmox,
		"notify":        map[string]bool{"discord": ns.DiscordWebhook != "", "ntfyToken": ns.NtfyToken != "", "telegram": ns.TelegramToken != ""},
		"username":      user.Username,
		"userId":        me,
		"role":          user.Role,
		"version":       s.version,
		"system":        s.systemInfo(),
	}
	// Updates, the debug mode and Proxmox discovery concern the whole instance.
	if admin {
		data["update"] = s.newerRelease()
		data["proxmoxHint"] = proxmoxHint()
		data["debug"] = logging.Debug()
		data["debugForced"] = logging.Forced() // HEIMDECK_DEBUG=true, the setting cannot turn it off
	}
	writeJSON(w, data)
}

// integrationsFor lists the services a user can pick; the host tiles only for admins.
func (s *Server) integrationsFor(admin bool, lang string) []integrations.Definition {
	all := integrations.All(lang)
	if admin {
		return all
	}
	out := all[:0]
	for _, d := range all {
		if !adminTiles[d.ID] {
			out = append(out, d)
		}
	}
	return out
}

// tileLayouts are the tile sizes a group (or the setting tileLayout) can have.
var tileLayouts = map[string]bool{"uniform": true, "auto": true, "icons": true, "icons-hover": true}

var allowedSettings = map[string]bool{
	"title": true, "theme": true, "accent": true, "background": true,
	"columns": true, "searchEngine": true, "searchUrl": true, "openInNewTab": true, "showGreeting": true,
	"localHosts": true, "showUptime": true, "language": true, "uniformTiles": true, "tileLayout": true,
	"notifyAfter": true, "notifyRecovery": true, "notifyExternal": true, "ntfyUrl": true, "dockerControl": true, "dockerUpdates": true,
	"debug": true, "telegramChat": true, "updateCheck": true, "shortcuts": true, "shortcutsOff": true,
	"corners": true, "tileStyle": true, "font": true, "density": true, "seasonal": true, "seasonalMode": true, "seasonalOff": true, "seasonalFx": true, "searchView": true, "searchJump": true, "searchHistory": true, "monitorExternal": true, "jumpBar": true, "jumpButton": true,
}

// checkSettings checks the addresses among the settings – from the settings
// page or from a backup – and tidies them up.
func checkSettings(m map[string]string) error {
	if u := strings.TrimSpace(m["ntfyUrl"]); u != "" {
		if p, err := url.Parse(u); err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" || len(strings.Trim(p.Path, "/")) == 0 {
			return errors.New("ntfy: bitte die vollständige Themen-Adresse angeben, z. B. https://ntfy.sh/mein-thema")
		}
		m["ntfyUrl"] = u
	}
	if u := strings.TrimSpace(m["searchUrl"]); u != "" || m["searchEngine"] == "custom" {
		if p, err := url.Parse(u); err != nil || (p.Scheme != "http" && p.Scheme != "https") || p.Host == "" {
			return errors.New("Websuche: bitte eine vollständige Adresse angeben, z. B. https://searx.example.com/search?q={q}")
		}
		m["searchUrl"] = u
	}
	return nil
}

func (s *Server) handleSaveSettings(w http.ResponseWriter, r *http.Request) {
	var m map[string]string
	if err := readJSON(r, &m); err != nil {
		writeErr(w, 400, err)
		return
	}
	admin := s.isAdmin(r)
	for k := range m {
		if !allowedSettings[k] || (instanceSettings[k] && !admin) {
			delete(m, k)
		}
	}
	if err := checkSettings(m); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.saveSettings(uid(r), admin, m); err != nil {
		slog.Error("Einstellungen speichern", "fehler", err)
		writeErr(w, 500, err)
		return
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	slog.Debug("Einstellungen gespeichert", "schlüssel", strings.Join(keys, ","))
	if v, ok := m["debug"]; ok {
		logging.SetDebug(v == "true")
	}
	if _, ok := m["updateCheck"]; ok && s.version != "" && s.version != "dev" {
		go s.checkUpdate(context.Background()) // show or hide the hint right away
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
	if body.BoardID == 0 {
		id, err := s.firstBoard(uid(r))
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		body.BoardID = id
	} else if _, code, err := s.ownBoard(r, body.BoardID); err != nil {
		writeErr(w, code, err)
		return
	}
	g, err := s.store.CreateGroup(strings.TrimSpace(body.Name), body.BoardID)
	if err != nil {
		slog.Error("Gruppe anlegen", "fehler", err)
		writeErr(w, 500, err)
		return
	}
	slog.Debug("Gruppe angelegt", "gruppe", g.ID, "name", g.Name, "dashboard", g.BoardID)
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
// fields keep the stored value, so keys never have to be sent back to the
// browser – but only for a tile on one of the owner's dashboards, so nobody
// can borrow the keys of someone else's tile.
func (s *Server) buildTile(in tileInput, owner int64) (store.Tile, error) {
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
	// The Docker and System tiles read the host without any key: only for admins.
	if adminTiles[t.Integration] {
		if u, err := s.store.User(owner); err != nil || u.Role != store.RoleAdmin {
			return t, errAdmin
		}
	}
	if t.Config == nil {
		t.Config = map[string]string{}
	}
	t.Secrets = map[string]string{}
	if t.ID != 0 {
		if b, err := s.store.TileBoard(t.ID); err != nil || b.OwnerID != owner {
			return t, errNotFound
		}
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
	// Hidden live data parts: a bounded list of short labels.
	var hide []string
	for _, h := range t.Hide {
		if h = strings.TrimSpace(h); h != "" && len(h) <= 200 && len(hide) < 100 && !slices.Contains(hide, h) {
			hide = append(hide, h)
		}
	}
	t.Hide = hide
	var err error
	if t.AppAndroid, err = appLink(androidApp(t.AppAndroid)); err == nil {
		t.AppIOS, err = appLink(t.AppIOS)
	}
	return t, err
}

var appSchemeRe = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9+.-]*:`)

var (
	androidPkgRe  = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9_]*(\.[a-zA-Z0-9_]+)+$`)
	playStoreIDRe = regexp.MustCompile(`^https://play\.google\.com/store/apps/details\?(?:.*&)?id=([a-zA-Z][a-zA-Z0-9_.]*)`)
)

// androidApp turns what people paste for the Android app – the package name
// or its Play Store address – into the link that opens the app.
func androidApp(u string) string {
	u = strings.TrimSpace(u)
	if m := playStoreIDRe.FindStringSubmatch(u); m != nil {
		u = m[1]
	}
	if androidPkgRe.MatchString(u) {
		return "intent://#Intent;package=" + u + ";end"
	}
	return u
}

// appLink checks the link a tile opens on phones instead of its website: any
// app scheme (intent:, shortcuts:, homeassistant: …) or https, but nothing a
// browser would run as a script or read from the device.
func appLink(u string) (string, error) {
	u = strings.TrimSpace(u)
	if u == "" {
		return "", nil
	}
	scheme := strings.ToLower(strings.TrimSuffix(appSchemeRe.FindString(u), ":"))
	switch scheme {
	case "":
		return "", errors.New("App-Link: bitte den Paketnamen der App (z. B. dev.jdtech.jellyfin) oder einen vollständigen Link angeben")
	case "javascript", "data", "vbscript", "file", "blob", "about", "http":
		return "", errors.New("App-Link: diese Art von Link ist nicht erlaubt")
	}
	if len(u) > 2000 {
		return "", errors.New("App-Link ist zu lang")
	}
	return u, nil
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
	if in.ID != 0 {
		if _, code, err := s.ownTile(r, in.ID); err != nil {
			writeErr(w, code, err)
			return
		}
	}
	t, err := s.buildTile(in, uid(r))
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if t.GroupID == 0 {
		writeErr(w, 400, errors.New("Gruppe fehlt"))
		return
	}
	if _, code, err := s.ownGroup(r, t.GroupID); err != nil {
		writeErr(w, code, err)
		return
	}
	if ok, _ := s.groupUnlocked(r, t.GroupID); !ok {
		writeErr(w, 403, errLocked)
		return
	}
	if err := s.store.SaveTile(&t); err != nil {
		slog.Error("Kachel speichern", "kachel", t.ID, "fehler", err)
		writeErr(w, 500, err)
		return
	}
	slog.Debug("Kachel gespeichert", "kachel", t.ID, "name", t.Name, "dienst", t.Integration, "gruppe", t.GroupID)
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
	if _, code, err := s.ownTile(r, id); err != nil {
		writeErr(w, code, err)
		return
	}
	if err := s.store.DeleteTile(id); err != nil {
		slog.Error("Kachel löschen", "kachel", id, "fehler", err)
		writeErr(w, 500, err)
		return
	}
	slog.Debug("Kachel gelöscht", "kachel", id)
	s.forget(id)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleLayout(w http.ResponseWriter, r *http.Request) {
	var l store.Layout
	if err := readJSON(r, &l); err != nil {
		writeErr(w, 400, err)
		return
	}
	// Only groups and tiles on the user's own dashboards can be moved.
	owners, err := s.store.TileOwners()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	for _, g := range l {
		if _, code, err := s.ownGroup(r, g.GroupID); err != nil {
			writeErr(w, code, err)
			return
		}
		for _, id := range g.Tiles {
			if owners[id] != uid(r) {
				writeErr(w, 404, errNotFound)
				return
			}
		}
	}
	if err := s.store.SaveLayout(l); err != nil {
		slog.Error("Anordnung speichern", "fehler", err)
		writeErr(w, 500, err)
		return
	}
	slog.Debug("Anordnung gespeichert", "gruppen", len(l))
	writeJSON(w, map[string]bool{"ok": true})
}

// handleTestTile checks a tile's settings before saving it.
func (s *Server) handleTestTile(w http.ResponseWriter, r *http.Request) {
	var in tileInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	t, err := s.buildTile(in, uid(r))
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	integ, _ := integrations.Get(t.Integration)
	if t.URL == "" && t.APIURL == "" && !integ.Def().NoURL {
		writeJSON(w, map[string]any{"ok": false, "error": "Bitte eine Adresse angeben"})
		return
	}
	for _, f := range integ.Def().Fields {
		if f.Required && t.Config[f.Key] == "" && t.Secrets[f.Key] == "" {
			writeJSON(w, map[string]any{"ok": false, "error": tr(r, "„%s“ fehlt", tr(r, f.Label))})
			return
		}
	}
	ctx, cancel := timeout(r, 15*time.Second)
	defer cancel()
	inst := s.instanceFor(r, t)
	start := time.Now()
	if !integ.Def().HasWidget {
		st := ping(ctx, inst.BaseURL, t.Insecure, s.external(uid(r)) || s.external(s.tileOwner(t)))
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
		widget = safeWidget(widget)
	}
	if err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, map[string]any{"ok": true, "ms": time.Since(start).Milliseconds(), "widget": widget})
}

// handleChoices fills the pick list of a field (e.g. Home Assistant entities)
// for the tile being edited, saved or not.
func (s *Server) handleChoices(w http.ResponseWriter, r *http.Request) {
	var in struct {
		tileInput
		Field string `json:"field"`
		Query string `json:"query"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	t, err := s.buildTile(in.tileInput, uid(r))
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	integ, _ := integrations.Get(t.Integration)
	chooser, ok := integ.(integrations.Chooser)
	if !ok {
		writeErr(w, 400, errors.New("keine Auswahl für diesen Dienst"))
		return
	}
	if t.URL == "" && t.APIURL == "" && !integ.Def().NoURL {
		writeErr(w, 400, errors.New(tr(r, "Bitte eine Adresse angeben")))
		return
	}
	ctx, cancel := timeout(r, 15*time.Second)
	defer cancel()
	list, err := chooser.Choices(ctx, s.instanceFor(r, t), in.Field, strings.TrimSpace(in.Query))
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, list)
}

// ---- live data ----

// widget returns the live data of a tile. Requests never wait for a slow
// service: a fresh result comes from the cache, a stale one is returned at
// once while it is refreshed in the background, and without any result the
// caller waits at most `wait` and then gets pending=true. Only one fetch per
// tile runs at a time, and failures are kept longer before trying again.
// (Waiting requests used to fill the browser's six connections per host, so
// with a few unreachable services every other request, e.g. deleting a group,
// queued behind them for minutes.)
func (s *Server) widget(t store.Tile, lang string, wait time.Duration) (w *integrations.Widget, pending bool, err error) {
	key := widgetKey(t.ID, lang)
	s.mu.Lock()
	c, ok := s.widgets[key]
	ttl := widgetTTL
	if integ, ok := integrations.Get(t.Integration); ok && integ.Def().Refresh > 0 {
		// a bit shorter than the tile's own interval, so each reload gets fresh data
		ttl = time.Duration(integ.Def().Refresh) * time.Second * 4 / 5
	}
	if c.err != nil {
		ttl = widgetErrTTL
	}
	if ok && time.Since(c.at) < ttl {
		s.mu.Unlock()
		return c.val, false, c.err
	}
	done, running := s.fetching[key]
	if !running {
		done = make(chan struct{})
		s.fetching[key] = done
		gen := s.widgetGen[key]
		go s.fetchWidget(t, lang, key, gen, done)
	}
	s.mu.Unlock()
	if ok {
		return c.val, false, c.err // stale, refreshed in the background
	}
	select {
	case <-done:
	case <-time.After(wait):
		return nil, true, nil
	}
	s.mu.Lock()
	c = s.widgets[key]
	s.mu.Unlock()
	return c.val, false, c.err
}

func (s *Server) fetchWidget(t store.Tile, lang, key string, gen int, done chan struct{}) {
	defer close(done)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	w, err := s.safeFetch(ctx, t, lang)
	if logging.Debug() {
		attrs := []any{"kachel", t.ID, "name", t.Name, "dienst", t.Integration, "dauer", time.Since(start).Round(time.Millisecond)}
		if err != nil {
			attrs = append(attrs, "fehler", logging.Redact(err.Error()))
		}
		slog.Debug("Live-Daten geholt", attrs...)
	}
	s.mu.Lock()
	// A tile that was edited or used in the meantime (forget) gets a fresh fetch instead.
	if s.widgetGen[key] == gen {
		s.widgets[key] = cached[*integrations.Widget]{at: time.Now(), val: w, err: err}
	}
	delete(s.fetching, key)
	s.mu.Unlock()
}

// safeFetch asks the service for the tile's live data. It runs outside of a
// request, so a panic in an integration would otherwise end the whole program.
func (s *Server) safeFetch(ctx context.Context, t store.Tile, lang string) (w *integrations.Widget, err error) {
	defer func() {
		if p := recover(); p != nil {
			slog.Error("Absturz in einer Integration", "kachel", t.ID, "name", t.Name, "dienst", t.Integration, "fehler", logging.Redact(fmt.Sprint(p)))
			os.Stderr.Write(debug.Stack())
			w, err = nil, errors.New("interner Fehler in der Integration – Details stehen im Protokoll")
		}
	}()
	integ, ok := integrations.Get(t.Integration)
	if !ok {
		return nil, errors.New("unbekannter Dienst-Typ")
	}
	if !s.dockerAllowed(t) {
		return nil, errDockerOwner
	}
	w, err = integ.Fetch(ctx, s.instance(t, lang))
	return safeWidget(w), err
}

func (s *Server) handleWidget(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	t, acc, code, err := s.tileAccess(r, id)
	if err != nil {
		writeErr(w, code, err)
		return
	}
	data, pending, err := s.widget(t, langOf(r), 3*time.Second)
	switch {
	case pending:
		writeJSON(w, map[string]any{"pending": true})
	case err != nil:
		writeJSON(w, map[string]any{"error": tr(r, err.Error())})
	default:
		data = hideParts(data, t.Hide, langOf(r))
		if !acc.Act {
			data = withoutActions(data)
		}
		writeJSON(w, map[string]any{"widget": data})
	}
}

// withoutActions drops the buttons for someone who may only look at a shared
// dashboard. The widget is shared with the cache and stays unchanged.
func withoutActions(w *integrations.Widget) *integrations.Widget {
	if w == nil {
		return nil
	}
	out := *w
	out.Actions = nil
	out.Items = stripItemActions(w.Items)
	return &out
}

func stripItemActions(items []integrations.Item) []integrations.Item {
	if items == nil {
		return nil
	}
	out := make([]integrations.Item, len(items))
	for i, it := range items {
		it.Actions = nil
		out[i] = it
	}
	return out
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
	t, acc, code, err := s.tileAccess(r, id)
	if err != nil {
		writeErr(w, code, err)
		return
	}
	if !acc.Act {
		writeErr(w, 403, errNoAct)
		return
	}
	if !s.dockerAllowed(t) {
		writeErr(w, 403, errDockerOwner)
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
	start := time.Now()
	if err := actioner.Action(ctx, s.instance(t, langOf(r)), body.Action, body.Params); err != nil {
		slog.Warn("Aktion fehlgeschlagen", "kachel", t.Name, "dienst", t.Integration, "aktion", body.Action, "fehler", logging.Redact(err.Error()))
		writeErr(w, 502, err)
		return
	}
	slog.Info("Aktion ausgeführt", "kachel", t.Name, "dienst", t.Integration, "aktion", body.Action, "benutzer", s.username(r), "dauer", time.Since(start).Round(time.Millisecond))
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
		slog.Debug("Bild nicht geladen", "kachel", t.Name, "dienst", t.Integration, "fehler", logging.Redact(err.Error()))
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
	boards, err := s.boardsFor(uid(r))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	gb, err := s.groupBoards()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	// Only search the dashboard that is currently open.
	if board, _ := strconv.ParseInt(r.URL.Query().Get("board"), 10, 64); board != 0 {
		filtered := tiles[:0]
		for _, t := range tiles {
			if gb[t.GroupID] == board {
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
		if !ok || !configured(integ, t) || !s.dockerAllowed(t) {
			continue
		}
		wg.Add(1)
		go func(t store.Tile) {
			defer wg.Done()
			defer logging.Recover("Suche", "kachel", t.ID, "dienst", t.Integration)
			start := time.Now()
			items, err := searcher.Search(ctx, s.instance(t, langOf(r)), q)
			items = safeItems(items)
			if errors.Is(err, integrations.ErrNoSearch) {
				return
			}
			if !boards[gb[t.GroupID]].Act {
				items = stripItemActions(items)
			}
			g := searchGroup{TileID: t.ID, Name: t.Name, Icon: t.Icon, Items: items}
			if err != nil {
				g.Error = err.Error()
				slog.Warn("Suche in einem Dienst fehlgeschlagen", "kachel", t.Name, "dienst", t.Integration, "dauer", time.Since(start).Round(time.Millisecond), "fehler", logging.Redact(err.Error()))
			} else {
				slog.Debug("Suche in einem Dienst", "kachel", t.Name, "dienst", t.Integration, "treffer", len(items), "dauer", time.Since(start).Round(time.Millisecond))
			}
			mu.Lock()
			out = append(out, g)
			mu.Unlock()
		}(t)
	}
	wg.Wait()
	// Libraries you already own first (Jellyfin), then request services, then the rest.
	rank := map[string]int{"jellyfin": 0, "plex": 0, "emby": 0, "jellyseerr": 1}
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
	ctx, cancel := timeout(r, 12*time.Second)
	defer cancel()
	// The admin's own tiles: their Proxmox hosts and what already has a tile.
	all, _ := s.store.Tiles()
	owners, _ := s.store.TileOwners()
	var tiles, pve []store.Tile
	for _, t := range all {
		if owners[t.ID] == uid(r) {
			tiles = append(tiles, t)
		}
	}
	for _, t := range tiles {
		if integ, ok := integrations.Get(t.Integration); ok && t.Integration == "proxmox" && configured(integ, t) {
			pve = append(pve, t)
		}
	}
	if s.docker == nil && len(pve) == 0 {
		writeErr(w, 404, errors.New("Nichts zum Durchsuchen: Docker-Socket einbinden (siehe docker-compose.yml) oder eine Proxmox-Kachel anlegen"))
		return
	}
	var sugg []docker.Suggestion
	var errs []string
	if s.docker != nil {
		ds, err := s.docker.Suggest(ctx, requestHost(r))
		if err != nil {
			errs = append(errs, "Docker: "+err.Error())
		}
		sugg = append(sugg, ds...)
	}
	for _, t := range pve {
		guests, err := integrations.ProxmoxGuests(ctx, s.instance(t, langOf(r)))
		if err != nil {
			errs = append(errs, t.Name+": "+err.Error())
			continue
		}
		sugg = append(sugg, proxmoxSuggestions(r, t, guests)...)
	}
	if len(sugg) == 0 && len(errs) > 0 {
		writeErr(w, 502, errors.New(strings.Join(errs, " · ")))
		return
	}
	// Mark services that already have a tile with the same URL.
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
	writeJSON(w, map[string]any{"items": out, "errors": errs})
}

// proxmoxSuggestions turns VMs and containers into tile proposals. The service
// is guessed from the guest's name and tags (e.g. LXCs created by the Proxmox
// community scripts are named after the service), the port from its default.
func proxmoxSuggestions(r *http.Request, host store.Tile, guests []integrations.Guest) []docker.Suggestion {
	var out []docker.Suggestion
	for _, g := range guests {
		if strings.Contains(strings.ToLower(g.Name), "heimdeck") {
			continue
		}
		kind := map[string]string{"qemu": "VM", "lxc": "LXC"}[g.Type]
		state := "exited"
		if g.Status == "running" {
			state = "running"
		}
		sg := docker.Suggestion{
			Container: docker.Container{ID: fmt.Sprintf("pve-%d-%s-%d", host.ID, g.Node, g.VMID), Name: g.Name, State: state,
				Image: fmt.Sprintf("%s · %s %d · %s", host.Name, kind, g.VMID, g.Node)},
			Name: g.Name, Integration: "link", Group: "Sonstiges", Icon: strings.ToLower(g.Name), Source: "proxmox",
		}
		port := 0
		if integ, ok := integrations.MatchName(append([]string{g.Name}, g.Tags...)...); ok {
			d := integ.Def()
			sg.Integration, sg.Icon, sg.Group, port = d.ID, d.Icon, d.Category, d.Port
			sg.Name = strings.Split(d.Name, " /")[0]
		}
		if g.IP != "" {
			switch {
			case port == 443 || port == 8443 || port == 8006:
				sg.URL = fmt.Sprintf("https://%s:%d", g.IP, port)
			case port == 80 || port == 0:
				sg.URL = "http://" + g.IP
			default:
				sg.URL = fmt.Sprintf("http://%s:%d", g.IP, port)
			}
		}
		out = append(out, sg)
	}
	return out
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
	MonitorExt  bool              `json:"monitorExternal,omitempty"`
	Grid        *store.GridPos    `json:"grid,omitempty"`
	Hide        []string          `json:"hide,omitempty"`
	AppAndroid  string            `json:"appAndroid,omitempty"`
	AppIOS      string            `json:"appIos,omitempty"`
}

type exportData struct {
	Version  int               `json:"version"`
	Settings map[string]string `json:"settings"`
	Groups   []exportGroup     `json:"groups"`
}

type exportGroup struct {
	Board      string       `json:"board,omitempty"`
	Layout     string       `json:"layout,omitempty"`
	Arrange    string       `json:"arrange,omitempty"`
	Name       string       `json:"name"`
	Visibility string       `json:"visibility,omitempty"`
	Tiles      []exportTile `json:"tiles"`
}

// handleExport writes groups, tiles and settings. API keys are left out on purpose.
// Only the user's own dashboards are in it, and the instance settings only for an admin.
func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	settings := publicSettings(s.settingsFor(uid(r)))
	if !s.isAdmin(r) {
		for k := range instanceSettings {
			delete(settings, k)
		}
	}
	delete(settings, settingsMoved)
	groups, err := s.store.Groups()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	tiles, _ := s.store.Tiles()
	locked, _ := s.lockedGroups(r)
	boards, _ := s.store.BoardsOf(uid(r))
	boardNames := map[int64]string{}
	for _, b := range boards {
		boardNames[b.ID] = b.Name
	}
	data := exportData{Version: 1, Settings: settings}
	for _, g := range groups {
		// Locked groups stay out of the backup; protected ones are restored as hidden (no passwords in backups).
		if _, own := boardNames[g.BoardID]; !own || locked[g.ID] {
			continue
		}
		eg := exportGroup{Name: g.Name, Board: boardNames[g.BoardID], Layout: g.Layout, Arrange: g.Arrange, Tiles: []exportTile{}}
		if g.Visibility != store.Visible {
			eg.Visibility = store.Hidden
		}
		for _, t := range tiles {
			if t.GroupID == g.ID {
				eg.Tiles = append(eg.Tiles, exportTile{Name: t.Name, URL: t.URL, ExternalURL: t.ExternalURL, APIURL: t.APIURL, Icon: t.Icon,
					Description: t.Description, Integration: t.Integration, Config: t.Config, Insecure: t.Insecure, ShowWidget: t.ShowWidget, NoMonitor: !t.Monitor, MonitorExt: t.MonitorExt, Grid: t.Grid, Hide: t.Hide, AppAndroid: t.AppAndroid, AppIOS: t.AppIOS})
			}
		}
		data.Groups = append(data.Groups, eg)
	}
	w.Header().Set("Content-Disposition", `attachment; filename="heimdeck-backup.json"`)
	writeJSON(w, data)
}

// handleImport adds the groups and tiles of a backup to the dashboard.
func (s *Server) handleImport(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 5<<20))
	if err != nil {
		writeErr(w, 400, errors.New("ungültige Anfrage"))
		return
	}
	if isHeimdallExport(body) {
		s.importHeimdall(w, r, body)
		return
	}
	var data exportData
	if err := json.Unmarshal(body, &data); err != nil || data.Version != 1 {
		writeErr(w, 400, errors.New("keine gültige Heimdeck-Sicherung oder Heimdall-Export"))
		return
	}
	me := uid(r)
	if len(data.Settings) > 0 {
		admin := s.isAdmin(r)
		for k := range data.Settings {
			if !allowedSettings[k] || (instanceSettings[k] && !admin) {
				delete(data.Settings, k)
			}
		}
		// Addresses that do not check out are left out, the rest is restored.
		if checkSettings(data.Settings) != nil {
			for _, k := range []string{"ntfyUrl", "searchUrl", "searchEngine"} {
				delete(data.Settings, k)
			}
			checkSettings(data.Settings)
		}
		logErr("import settings", s.saveSettings(me, admin, data.Settings))
	}
	count := 0
	boards, _ := s.store.BoardsOf(me)
	boardIDs := map[string]int64{}
	for _, b := range boards {
		boardIDs[b.Name] = b.ID
	}
	first, err := s.firstBoard(me)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	for _, eg := range data.Groups {
		boardID := boardIDs[eg.Board]
		if boardID == 0 && eg.Board == "" {
			boardID = first
		}
		if eg.Board != "" && boardID == 0 {
			b := store.Board{OwnerID: me, Name: eg.Board}
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
		// Older backups may say "columns"; that arrangement no longer exists and becomes rows.
		if eg.Visibility == store.Hidden || tileLayouts[eg.Layout] || eg.Arrange == "free" {
			if eg.Visibility == store.Hidden {
				g.Visibility = store.Hidden
			}
			if tileLayouts[eg.Layout] {
				g.Layout = eg.Layout
			}
			if eg.Arrange == "free" {
				g.Arrange = "free"
			}
			logErr("import group", s.store.UpdateGroup(g))
		}
		for _, et := range eg.Tiles {
			monitor := !et.NoMonitor
			t, err := s.buildTile(tileInput{Tile: store.Tile{
				GroupID: g.ID, Name: et.Name, URL: et.URL, ExternalURL: et.ExternalURL, APIURL: et.APIURL, Icon: et.Icon, Description: et.Description,
				Integration: et.Integration, Config: et.Config, Insecure: et.Insecure, ShowWidget: et.ShowWidget, Grid: et.Grid, Hide: et.Hide,
				MonitorExt: et.MonitorExt, AppAndroid: et.AppAndroid, AppIOS: et.AppIOS,
			}, Monitor: &monitor}, me)
			if err != nil {
				continue
			}
			if s.store.SaveTile(&t) == nil {
				count++
			}
		}
	}
	slog.Debug("Sicherung importiert", "gruppen", len(data.Groups), "kacheln", count)
	writeJSON(w, map[string]any{"ok": true, "tiles": count})
}

// importHeimdall adds the items of a Heimdall export to the open dashboard.
func (s *Server) importHeimdall(w http.ResponseWriter, r *http.Request, body []byte) {
	groups, order, err := heimdallTiles(body, "Heimdall")
	if err != nil {
		writeErr(w, 400, errors.New("keine gültige Heimdeck-Sicherung oder Heimdall-Export"))
		return
	}
	boardID, _ := strconv.ParseInt(r.URL.Query().Get("board"), 10, 64)
	if boardID == 0 {
		boardID, err = s.firstBoard(uid(r))
		if err != nil {
			writeErr(w, 500, err)
			return
		}
	} else if _, code, err := s.ownBoard(r, boardID); err != nil {
		writeErr(w, code, err)
		return
	}
	existing := map[string]int64{}
	if all, err := s.store.Groups(); err == nil {
		for _, g := range all {
			if g.BoardID == boardID {
				existing[strings.ToLower(g.Name)] = g.ID
			}
		}
	}
	count, live := 0, 0
	for _, name := range order {
		gid, ok := existing[strings.ToLower(name)]
		if !ok {
			g, err := s.store.CreateGroup(name, boardID)
			if err != nil {
				writeErr(w, 500, err)
				return
			}
			gid = g.ID
		}
		for _, in := range groups[name] {
			in.GroupID = gid
			t, err := s.buildTile(in, uid(r))
			if err != nil {
				continue
			}
			if s.store.SaveTile(&t) == nil {
				count++
				if t.Integration != "link" {
					live++
				}
			}
		}
	}
	writeJSON(w, map[string]any{"ok": true, "tiles": count, "integrations": live, "source": "heimdall"})
}

// proxmoxHint is the Proxmox address offered when adding a Proxmox tile: set
// by the LXC installer, or the local API when Heimdeck runs on the host itself.
func proxmoxHint() string {
	if u := os.Getenv("HEIMDECK_PROXMOX_URL"); u != "" {
		return u
	}
	if _, err := os.Stat("/etc/pve"); err == nil {
		return "https://127.0.0.1:8006"
	}
	return ""
}

// publicSettings drops internal entries (prefixed with "_") such as the
// encrypted notification secrets.
func publicSettings(m map[string]string) map[string]string {
	for k := range m {
		if strings.HasPrefix(k, "_") {
			delete(m, k)
		}
	}
	return m
}
