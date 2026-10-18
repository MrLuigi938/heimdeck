// Package store persists users, sessions, groups, tiles and settings in SQLite.
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/heimdeck/heimdeck/internal/secret"
)

var ErrNotFound = errors.New("nicht gefunden")

type Store struct {
	db  *sql.DB
	box *secret.Box
}

// Group visibility: always shown, hidden until revealed, or password protected.
const (
	Visible   = "visible"
	Hidden    = "hidden"
	Protected = "protected"
)

type Group struct {
	ID           int64  `json:"id"`
	BoardID      int64  `json:"boardId"`
	Name         string `json:"name"`
	Position     int    `json:"position"`
	Visibility   string `json:"visibility"`
	Layout       string `json:"layout"` // "" = follow the global setting, "uniform" or "auto"
	PasswordHash string `json:"-"`
}

// Board is a separate dashboard, e.g. "Privat" and "Arbeit".
type Board struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Icon       string `json:"icon"`
	Background string `json:"background"` // empty = use the global background
	Position   int    `json:"position"`
}

type Tile struct {
	ID          int64             `json:"id"`
	GroupID     int64             `json:"groupId"`
	Name        string            `json:"name"`
	URL         string            `json:"url"`
	ExternalURL string            `json:"externalUrl"`
	APIURL      string            `json:"apiUrl"`
	Icon        string            `json:"icon"`
	Description string            `json:"description"`
	Integration string            `json:"integration"`
	Config      map[string]string `json:"config"`
	Insecure    bool              `json:"insecure"`
	ShowWidget  bool              `json:"showWidget"`
	Monitor     bool              `json:"monitor"` // check and show the online status
	Position    int               `json:"position"`
	// Secrets never leave the backend; the API only reports which keys are set.
	Secrets map[string]string `json:"-"`
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id INTEGER PRIMARY KEY,
	username TEXT NOT NULL UNIQUE,
	password_hash TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS sessions (
	token TEXT PRIMARY KEY,
	user_id INTEGER NOT NULL,
	expires INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS groups (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	position INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS tiles (
	id INTEGER PRIMARY KEY,
	group_id INTEGER NOT NULL,
	name TEXT NOT NULL,
	url TEXT NOT NULL DEFAULT '',
	api_url TEXT NOT NULL DEFAULT '',
	icon TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	integration TEXT NOT NULL DEFAULT '',
	config TEXT NOT NULL DEFAULT '{}',
	secrets TEXT NOT NULL DEFAULT '',
	insecure INTEGER NOT NULL DEFAULT 0,
	show_widget INTEGER NOT NULL DEFAULT 1,
	position INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS boards (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	icon TEXT NOT NULL DEFAULT '',
	position INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS status_log (
	tile_id INTEGER NOT NULL,
	kind TEXT NOT NULL,
	ts INTEGER NOT NULL,
	online INTEGER NOT NULL,
	ms INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS status_log_ts ON status_log (ts);
CREATE TABLE IF NOT EXISTS settings (
	key TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

func Open(path string, box *secret.Box) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, err
	}
	// Columns added after the first release; "duplicate column" errors mean they already exist.
	for _, stmt := range []string{
		`ALTER TABLE tiles ADD COLUMN external_url TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE groups ADD COLUMN visibility TEXT NOT NULL DEFAULT 'visible'`,
		`ALTER TABLE groups ADD COLUMN password_hash TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE tiles ADD COLUMN monitor INTEGER NOT NULL DEFAULT 1`,
		`ALTER TABLE groups ADD COLUMN board_id INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE groups ADD COLUMN layout TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE boards ADD COLUMN background TEXT NOT NULL DEFAULT ''`,
	} {
		if _, err := db.Exec(stmt); err != nil && !strings.Contains(err.Error(), "duplicate column") {
			return nil, err
		}
	}
	st := &Store{db: db, box: box}
	if err := st.ensureBoard(); err != nil {
		return nil, err
	}
	return st, nil
}

// ensureBoard creates the first dashboard and moves groups without one onto it.
func (s *Store) ensureBoard() error {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM boards ORDER BY position, id LIMIT 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		res, err := s.db.Exec(`INSERT INTO boards (name, icon, position) VALUES ('Privat', '🏡', 0)`)
		if err != nil {
			return err
		}
		id, _ = res.LastInsertId()
	} else if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE groups SET board_id = ? WHERE board_id NOT IN (SELECT id FROM boards)`, id)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

// ---- users & sessions ----

func (s *Store) HasUser() (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n > 0, err
}

func (s *Store) CreateUser(username, hash string) error {
	_, err := s.db.Exec(`INSERT INTO users (username, password_hash) VALUES (?, ?)`, username, hash)
	return err
}

func (s *Store) UserByName(username string) (id int64, hash string, err error) {
	err = s.db.QueryRow(`SELECT id, password_hash FROM users WHERE username = ?`, username).Scan(&id, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func (s *Store) Username(id int64) (name string, err error) {
	err = s.db.QueryRow(`SELECT username FROM users WHERE id = ?`, id).Scan(&name)
	return
}

func (s *Store) FirstUsername() (name string, err error) {
	err = s.db.QueryRow(`SELECT username FROM users ORDER BY id LIMIT 1`).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return
}

func (s *Store) SetPassword(username, hash string) error {
	res, err := s.db.Exec(`UPDATE users SET password_hash = ? WHERE username = ?`, hash, username)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	_, err = s.db.Exec(`DELETE FROM sessions`)
	return err
}

func (s *Store) CreateSession(token string, userID int64, expires time.Time) error {
	_, err := s.db.Exec(`INSERT INTO sessions (token, user_id, expires) VALUES (?, ?, ?)`, token, userID, expires.Unix())
	return err
}

func (s *Store) SessionUser(token string) (int64, error) {
	var id, exp int64
	err := s.db.QueryRow(`SELECT user_id, expires FROM sessions WHERE token = ?`, token).Scan(&id, &exp)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && exp < time.Now().Unix()) {
		return 0, ErrNotFound
	}
	return id, err
}

func (s *Store) DeleteSession(token string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE token = ? OR expires < ?`, token, time.Now().Unix())
	return err
}

// ---- settings ----

func (s *Store) Settings() (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM settings`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, err
		}
		out[k] = v
	}
	return out, rows.Err()
}

func (s *Store) SaveSettings(m map[string]string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range m {
		if _, err := tx.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- groups ----

func (s *Store) Groups() ([]Group, error) {
	rows, err := s.db.Query(`SELECT ` + groupCols + ` FROM groups ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		if err := rows.Scan(&g.ID, &g.BoardID, &g.Name, &g.Position, &g.Visibility, &g.Layout, &g.PasswordHash); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

const groupCols = `id, board_id, name, position, visibility, layout, password_hash`

// CreateGroup adds a group to a board; boardID 0 means the first board.
func (s *Store) CreateGroup(name string, boardID int64) (Group, error) {
	if boardID == 0 {
		s.db.QueryRow(`SELECT id FROM boards ORDER BY position, id LIMIT 1`).Scan(&boardID)
	} else if _, err := s.Board(boardID); err != nil {
		return Group{}, err
	}
	var pos int
	s.db.QueryRow(`SELECT COALESCE(MAX(position)+1, 0) FROM groups`).Scan(&pos)
	res, err := s.db.Exec(`INSERT INTO groups (board_id, name, position) VALUES (?, ?, ?)`, boardID, name, pos)
	if err != nil {
		return Group{}, err
	}
	id, _ := res.LastInsertId()
	return Group{ID: id, BoardID: boardID, Name: name, Position: pos, Visibility: Visible}, nil
}

func (s *Store) Group(id int64) (Group, error) {
	var g Group
	err := s.db.QueryRow(`SELECT `+groupCols+` FROM groups WHERE id = ?`, id).
		Scan(&g.ID, &g.BoardID, &g.Name, &g.Position, &g.Visibility, &g.Layout, &g.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return g, err
}

func (s *Store) UpdateGroup(g Group) error {
	_, err := s.db.Exec(`UPDATE groups SET board_id = ?, name = ?, visibility = ?, layout = ?, password_hash = ? WHERE id = ?`,
		g.BoardID, g.Name, g.Visibility, g.Layout, g.PasswordHash, g.ID)
	return err
}

// ---- boards ----

func (s *Store) Boards() ([]Board, error) {
	rows, err := s.db.Query(`SELECT id, name, icon, background, position FROM boards ORDER BY position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Board{}
	for rows.Next() {
		var b Board
		if err := rows.Scan(&b.ID, &b.Name, &b.Icon, &b.Background, &b.Position); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func (s *Store) Board(id int64) (Board, error) {
	var b Board
	err := s.db.QueryRow(`SELECT id, name, icon, background, position FROM boards WHERE id = ?`, id).Scan(&b.ID, &b.Name, &b.Icon, &b.Background, &b.Position)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return b, err
}

func (s *Store) SaveBoard(b *Board) error {
	if b.ID == 0 {
		s.db.QueryRow(`SELECT COALESCE(MAX(position)+1, 0) FROM boards`).Scan(&b.Position)
		res, err := s.db.Exec(`INSERT INTO boards (name, icon, background, position) VALUES (?, ?, ?, ?)`, b.Name, b.Icon, b.Background, b.Position)
		if err != nil {
			return err
		}
		b.ID, _ = res.LastInsertId()
		return nil
	}
	_, err := s.db.Exec(`UPDATE boards SET name = ?, icon = ?, background = ? WHERE id = ?`, b.Name, b.Icon, b.Background, b.ID)
	return err
}

// DeleteBoard removes a board together with its groups and tiles.
func (s *Store) DeleteBoard(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, q := range []string{
		`DELETE FROM status_log WHERE tile_id IN (SELECT t.id FROM tiles t JOIN groups g ON t.group_id = g.id WHERE g.board_id = ?)`,
		`DELETE FROM tiles WHERE group_id IN (SELECT id FROM groups WHERE board_id = ?)`,
		`DELETE FROM groups WHERE board_id = ?`,
		`DELETE FROM boards WHERE id = ?`,
	} {
		if _, err := tx.Exec(q, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteGroup(id int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM tiles WHERE group_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM groups WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

// ---- tiles ----

const tileCols = `id, group_id, name, url, external_url, api_url, icon, description, integration, config, secrets, insecure, show_widget, monitor, position`

func (s *Store) scanTile(sc interface{ Scan(...any) error }) (Tile, error) {
	var t Tile
	var cfg, sec string
	err := sc.Scan(&t.ID, &t.GroupID, &t.Name, &t.URL, &t.ExternalURL, &t.APIURL, &t.Icon, &t.Description, &t.Integration, &cfg, &sec, &t.Insecure, &t.ShowWidget, &t.Monitor, &t.Position)
	if err != nil {
		return t, err
	}
	t.Config = map[string]string{}
	json.Unmarshal([]byte(cfg), &t.Config)
	t.Secrets = map[string]string{}
	if plain, err := s.box.Open(sec); err == nil && plain != nil {
		json.Unmarshal(plain, &t.Secrets)
	}
	return t, nil
}

func (s *Store) Tiles() ([]Tile, error) {
	rows, err := s.db.Query(`SELECT ` + tileCols + ` FROM tiles ORDER BY group_id, position, id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Tile{}
	for rows.Next() {
		t, err := s.scanTile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

func (s *Store) Tile(id int64) (Tile, error) {
	t, err := s.scanTile(s.db.QueryRow(`SELECT `+tileCols+` FROM tiles WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return t, err
}

func (s *Store) SaveTile(t *Tile) error {
	cfg, _ := json.Marshal(t.Config)
	secJSON, _ := json.Marshal(t.Secrets)
	sec, err := s.box.Seal(secJSON)
	if err != nil {
		return err
	}
	if t.ID == 0 {
		s.db.QueryRow(`SELECT COALESCE(MAX(position)+1, 0) FROM tiles WHERE group_id = ?`, t.GroupID).Scan(&t.Position)
		res, err := s.db.Exec(`INSERT INTO tiles (group_id, name, url, external_url, api_url, icon, description, integration, config, secrets, insecure, show_widget, monitor, position)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			t.GroupID, t.Name, t.URL, t.ExternalURL, t.APIURL, t.Icon, t.Description, t.Integration, string(cfg), sec, t.Insecure, t.ShowWidget, t.Monitor, t.Position)
		if err != nil {
			return err
		}
		t.ID, _ = res.LastInsertId()
		return nil
	}
	_, err = s.db.Exec(`UPDATE tiles SET group_id = ?, name = ?, url = ?, external_url = ?, api_url = ?, icon = ?, description = ?, integration = ?, config = ?, secrets = ?, insecure = ?, show_widget = ?, monitor = ? WHERE id = ?`,
		t.GroupID, t.Name, t.URL, t.ExternalURL, t.APIURL, t.Icon, t.Description, t.Integration, string(cfg), sec, t.Insecure, t.ShowWidget, t.Monitor, t.ID)
	return err
}

func (s *Store) DeleteTile(id int64) error {
	_, err := s.db.Exec(`DELETE FROM tiles WHERE id = ?`, id)
	if err == nil {
		_, err = s.db.Exec(`DELETE FROM status_log WHERE tile_id = ?`, id)
	}
	return err
}

// Layout describes the complete order: groups in order, each with its tile ids in order.
type Layout []struct {
	GroupID int64   `json:"groupId"`
	Tiles   []int64 `json:"tiles"`
}

func (s *Store) SaveLayout(l Layout) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for gi, g := range l {
		if _, err := tx.Exec(`UPDATE groups SET position = ? WHERE id = ?`, gi, g.GroupID); err != nil {
			return err
		}
		for ti, id := range g.Tiles {
			if _, err := tx.Exec(`UPDATE tiles SET group_id = ?, position = ? WHERE id = ?`, g.GroupID, ti, id); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// ---- status history ----

type Check struct {
	TileID int64
	Kind   string // "int" (internal address) or "ext" (public address)
	Online bool
	MS     int64
}

func (s *Store) LogChecks(at time.Time, checks []Check) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, c := range checks {
		if _, err := tx.Exec(`INSERT INTO status_log (tile_id, kind, ts, online, ms) VALUES (?, ?, ?, ?, ?)`,
			c.TileID, c.Kind, at.Unix(), c.Online, c.MS); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) PruneChecks(before time.Time) error {
	_, err := s.db.Exec(`DELETE FROM status_log WHERE ts < ?`, before.Unix())
	return err
}

// Bucket aggregates the checks of one tile address within a time slot.
type Bucket struct {
	TileID int64
	Kind   string
	Start  int64
	Up     int
	Total  int
	AvgMS  float64
}

func (s *Store) CheckBuckets(since time.Time, size time.Duration) ([]Bucket, error) {
	sec := int64(size.Seconds())
	rows, err := s.db.Query(`SELECT tile_id, kind, (ts / ?) * ?, SUM(online), COUNT(*), COALESCE(AVG(CASE WHEN online THEN ms END), 0)
		FROM status_log WHERE ts >= ? GROUP BY tile_id, kind, ts / ? ORDER BY 3`, sec, sec, since.Unix(), sec)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Bucket{}
	for rows.Next() {
		var b Bucket
		if err := rows.Scan(&b.TileID, &b.Kind, &b.Start, &b.Up, &b.Total, &b.AvgMS); err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// LastDown returns when each tile address was last seen offline.
func (s *Store) LastDown(since time.Time) (map[string]int64, error) {
	rows, err := s.db.Query(`SELECT tile_id, kind, MAX(ts) FROM status_log WHERE online = 0 AND ts >= ? GROUP BY tile_id, kind`, since.Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int64{}
	for rows.Next() {
		var id, ts int64
		var kind string
		if err := rows.Scan(&id, &kind, &ts); err != nil {
			return nil, err
		}
		out[CheckKey(id, kind)] = ts
	}
	return out, rows.Err()
}

func CheckKey(tileID int64, kind string) string {
	return kind + ":" + strconv.FormatInt(tileID, 10)
}
