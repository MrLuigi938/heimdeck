package server

import (
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/store"
)

var errLocked = errors.New("Diese Gruppe ist gesperrt – bitte zuerst entsperren")

// ---- access to protected groups ----

func sessionToken(r *http.Request) string {
	if c, err := r.Cookie(auth.CookieName); err == nil {
		return c.Value
	}
	return ""
}

// lockedGroups returns the protected groups this session has not unlocked.
// Tiles in these groups are never sent to the browser.
func (s *Server) lockedGroups(r *http.Request) (map[int64]bool, error) {
	groups, err := s.store.Groups()
	if err != nil {
		return nil, err
	}
	token := sessionToken(r)
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	open := s.unlocked[token]
	locked := map[int64]bool{}
	for _, g := range groups {
		if g.Visibility != store.Protected {
			continue
		}
		if until, ok := open[g.ID]; ok && now.Before(until) {
			continue
		}
		locked[g.ID] = true
	}
	return locked, nil
}

// accessibleTiles returns all tiles outside locked groups.
func (s *Server) accessibleTiles(r *http.Request) ([]store.Tile, error) {
	locked, err := s.lockedGroups(r)
	if err != nil {
		return nil, err
	}
	tiles, err := s.store.Tiles()
	if err != nil {
		return nil, err
	}
	out := tiles[:0]
	for _, t := range tiles {
		if !locked[t.GroupID] {
			out = append(out, t)
		}
	}
	return out, nil
}

// accessibleTile loads a tile and refuses it if its group is locked.
func (s *Server) accessibleTile(r *http.Request, id int64) (store.Tile, int, error) {
	t, err := s.store.Tile(id)
	if err != nil {
		return t, 404, err
	}
	locked, err := s.lockedGroups(r)
	if err != nil {
		return t, 500, err
	}
	if locked[t.GroupID] {
		return t, 403, errLocked
	}
	return t, 200, nil
}

func (s *Server) groupUnlocked(r *http.Request, id int64) (bool, error) {
	locked, err := s.lockedGroups(r)
	return !locked[id], err
}

// ---- group handlers ----

func (s *Server) handleUpdateGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	var body struct {
		Name       string  `json:"name"`
		Visibility string  `json:"visibility"`
		Password   string  `json:"password"`
		BoardID    int64   `json:"boardId"`
		Layout     *string `json:"layout"`
		Arrange    *string `json:"arrange"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	g, err := s.store.Group(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	if ok, _ := s.groupUnlocked(r, id); !ok {
		writeErr(w, 403, errLocked)
		return
	}
	if name := strings.TrimSpace(body.Name); name != "" {
		g.Name = name
	}
	if body.BoardID != 0 && body.BoardID != g.BoardID {
		if _, err := s.store.Board(body.BoardID); err != nil {
			writeErr(w, 400, errors.New("unbekanntes Dashboard"))
			return
		}
		g.BoardID = body.BoardID
	}
	if body.Layout != nil {
		switch *body.Layout {
		case "", "uniform", "auto", "icons", "icons-hover":
			g.Layout = *body.Layout
		default:
			writeErr(w, 400, errors.New("unbekannte Kachelgröße"))
			return
		}
	}
	if body.Arrange != nil {
		if *body.Arrange != "" && *body.Arrange != "columns" {
			writeErr(w, 400, errors.New("unbekannte Anordnung"))
			return
		}
		g.Arrange = *body.Arrange
	}
	switch body.Visibility {
	case "":
	case store.Visible, store.Hidden:
		g.Visibility = body.Visibility
		g.PasswordHash = ""
	case store.Protected:
		if body.Password != "" {
			if len(body.Password) < 4 {
				writeErr(w, 400, errors.New("Das Gruppen-Passwort braucht mindestens 4 Zeichen"))
				return
			}
			hash, err := auth.HashPassword(body.Password)
			if err != nil {
				writeErr(w, 500, err)
				return
			}
			g.PasswordHash = hash
		}
		if g.PasswordHash == "" {
			writeErr(w, 400, errors.New("Bitte ein Passwort für die Gruppe festlegen"))
			return
		}
		g.Visibility = store.Protected
	default:
		writeErr(w, 400, errors.New("unbekannte Sichtbarkeit"))
		return
	}
	if err := s.store.UpdateGroup(g); err != nil {
		writeErr(w, 500, err)
		return
	}
	// Whoever just set the password keeps the group open.
	if g.Visibility == store.Protected {
		s.unlock(r, id)
	}
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) unlock(r *http.Request, id int64) {
	token := sessionToken(r)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unlocked[token] == nil {
		s.unlocked[token] = map[int64]time.Time{}
	}
	s.unlocked[token][id] = time.Now().Add(unlockTTL)
}

func (s *Server) handleUnlockGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	g, err := s.store.Group(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	if g.Visibility != store.Protected {
		writeJSON(w, map[string]bool{"ok": true})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(g.PasswordHash), []byte(body.Password)) != nil {
		time.Sleep(500 * time.Millisecond) // slow down guessing
		writeErr(w, 403, errors.New("Falsches Passwort"))
		return
	}
	s.unlock(r, id)
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleLockGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	s.mu.Lock()
	delete(s.unlocked[sessionToken(r)], id)
	s.mu.Unlock()
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if ok, _ := s.groupUnlocked(r, id); !ok {
		writeErr(w, 403, errLocked)
		return
	}
	if err := s.store.DeleteGroup(id); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- status & uptime ----

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	tiles, err := s.accessibleTiles(r)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	out := map[int64]TileStatus{}
	s.mu.Lock()
	for _, t := range tiles {
		if st, ok := s.latest[t.ID]; ok {
			out[t.ID] = st
		}
	}
	s.mu.Unlock()
	writeJSON(w, out)
}

type uptimeRow struct {
	TileID   int64     `json:"tileId"`
	Kind     string    `json:"kind"` // int | ext
	URL      string    `json:"url"`
	Uptime   *float64  `json:"uptime"` // percent, nil without data
	AvgMS    int64     `json:"avgMs"`
	LastDown int64     `json:"lastDown,omitempty"` // unix seconds
	Buckets  []*bucket `json:"buckets"`            // oldest first; nil = no data
}

type bucket struct {
	Start int64 `json:"t"`
	Up    int   `json:"up"`
	Total int   `json:"total"`
	AvgMS int64 `json:"ms"`
}

// handleUptime returns the status history in fixed slots: 48 × 30 min for
// 24 hours, or 72 × 1 h for three days.
func (s *Server) handleUptime(w http.ResponseWriter, r *http.Request) {
	hours := 24
	if r.URL.Query().Get("range") == "72h" {
		hours = 72
	}
	size := 30 * time.Minute
	if hours == 72 {
		size = time.Hour
	}
	slots := int(time.Duration(hours) * time.Hour / size)
	sec := int64(size.Seconds())
	end := time.Now().Unix()/sec*sec + sec
	start := end - int64(slots)*sec

	tiles, err := s.accessibleTiles(r)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	buckets, err := s.store.CheckBuckets(time.Unix(start, 0), size)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	lastDown, _ := s.store.LastDown(time.Unix(start, 0))

	rows := map[string]*uptimeRow{}
	out := []*uptimeRow{}
	for _, t := range tiles {
		if !t.Monitor {
			continue
		}
		internal := t.APIURL
		if internal == "" {
			internal = t.URL
		}
		add := func(kind, url string) {
			row := &uptimeRow{TileID: t.ID, Kind: kind, URL: url, Buckets: make([]*bucket, slots), LastDown: lastDown[store.CheckKey(t.ID, kind)]}
			rows[store.CheckKey(t.ID, kind)] = row
			out = append(out, row)
		}
		if internal != "" {
			add("int", internal)
		}
		if t.ExternalURL != "" && t.ExternalURL != internal {
			add("ext", t.ExternalURL)
		}
	}
	for _, b := range buckets {
		row := rows[store.CheckKey(b.TileID, b.Kind)]
		i := int((b.Start - start) / sec)
		if row == nil || i < 0 || i >= slots {
			continue
		}
		row.Buckets[i] = &bucket{Start: b.Start, Up: b.Up, Total: b.Total, AvgMS: int64(b.AvgMS)}
	}
	for _, row := range out {
		up, total, msSum, msN := 0, 0, int64(0), 0
		for _, b := range row.Buckets {
			if b == nil {
				continue
			}
			up += b.Up
			total += b.Total
			if b.Up > 0 {
				msSum += b.AvgMS * int64(b.Up)
				msN += b.Up
			}
		}
		if total > 0 {
			p := float64(up) / float64(total) * 100
			row.Uptime = &p
		}
		if msN > 0 {
			row.AvgMS = msSum / int64(msN)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TileID < out[j].TileID })
	writeJSON(w, map[string]any{"start": start, "slot": sec, "rows": out})
}

// ---- boards ----

type boardInput struct {
	Name       string `json:"name"`
	Icon       string `json:"icon"`
	Background string `json:"background"`
}

func (s *Server) handleCreateBoard(w http.ResponseWriter, r *http.Request) {
	var in boardInput
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, 400, errors.New("Name fehlt"))
		return
	}
	b := store.Board{Name: strings.TrimSpace(in.Name), Icon: strings.TrimSpace(in.Icon), Background: strings.TrimSpace(in.Background)}
	if err := s.store.SaveBoard(&b); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, b)
}

func (s *Server) handleUpdateBoard(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	var in boardInput
	if err := readJSON(r, &in); err != nil || strings.TrimSpace(in.Name) == "" {
		writeErr(w, 400, errors.New("Name fehlt"))
		return
	}
	b, err := s.store.Board(id)
	if err != nil {
		writeErr(w, 404, err)
		return
	}
	b.Name, b.Icon, b.Background = strings.TrimSpace(in.Name), strings.TrimSpace(in.Icon), strings.TrimSpace(in.Background)
	if err := s.store.SaveBoard(&b); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, b)
}

func (s *Server) handleDeleteBoard(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	boards, err := s.store.Boards()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if len(boards) <= 1 {
		writeErr(w, 400, errors.New("Das letzte Dashboard kann nicht gelöscht werden"))
		return
	}
	// Protected groups on the board must be unlocked before they can be deleted.
	groups, _ := s.store.Groups()
	locked, _ := s.lockedGroups(r)
	for _, g := range groups {
		if g.BoardID == id && locked[g.ID] {
			writeErr(w, 403, errLocked)
			return
		}
	}
	if err := s.store.DeleteBoard(id); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
