package store

import (
	"database/sql"
	"errors"
	"time"
)

// Roles: the admin manages the accounts and the instance-wide settings
// (Docker, Proxmox, network, updates); users only see their own dashboards
// and the ones shared with them.
const (
	RoleAdmin = "admin"
	RoleUser  = "user"
)

var ErrNameTaken = errors.New("Dieser Benutzername ist schon vergeben")

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
	Disabled bool   `json:"disabled"`
	Created  int64  `json:"created"` // unix seconds, 0 for accounts from before 1.4
	// External: someone from outside the home. Heimdeck does not reach into
	// the home network for their tiles (no LAN addresses, no localhost).
	External bool `json:"external"`
}

const userCols = `id, username, role, disabled, created, external`

func scanUser(sc interface{ Scan(...any) error }) (User, error) {
	var u User
	err := sc.Scan(&u.ID, &u.Username, &u.Role, &u.Disabled, &u.Created, &u.External)
	return u, err
}

func (s *Store) Users() ([]User, error) {
	rows, err := s.db.Query(`SELECT ` + userCols + ` FROM users ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []User{}
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

func (s *Store) User(id int64) (User, error) {
	u, err := scanUser(s.db.QueryRow(`SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return u, err
}

// UpdateUser changes the role and the disabled and external flags; a disabled account is logged out.
func (s *Store) UpdateUser(u User) error {
	if _, err := s.db.Exec(`UPDATE users SET role = ?, disabled = ?, external = ? WHERE id = ?`, u.Role, u.Disabled, u.External, u.ID); err != nil {
		return err
	}
	if u.Disabled {
		_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, u.ID)
		return err
	}
	return nil
}

// AdminCount returns the number of active admins, so the last one cannot be removed.
func (s *Store) AdminCount() (n int) {
	s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled = 0`).Scan(&n)
	return
}

// DeleteUser removes an account with its dashboards, shares and settings.
func (s *Store) DeleteUser(id int64) error {
	boards, err := s.BoardsOf(id)
	if err != nil {
		return err
	}
	for _, b := range boards {
		if err := s.DeleteBoard(b.ID); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`DELETE FROM shares WHERE user_id = ?`,
		`DELETE FROM sessions WHERE user_id = ?`,
		`DELETE FROM user_settings WHERE user_id = ?`,
		`DELETE FROM users WHERE id = ?`,
	} {
		if _, err := s.db.Exec(q, id); err != nil {
			return err
		}
	}
	return nil
}

// AdoptBoards gives dashboards without an owner (created before the first
// account existed) to the user.
func (s *Store) AdoptBoards(owner int64) error {
	_, err := s.db.Exec(`UPDATE boards SET owner_id = ? WHERE owner_id = 0`, owner)
	return err
}

// ---- personal settings ----

func (s *Store) UserSettings(uid int64) (map[string]string, error) {
	rows, err := s.db.Query(`SELECT key, value FROM user_settings WHERE user_id = ?`, uid)
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

func (s *Store) SaveUserSettings(uid int64, m map[string]string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for k, v := range m {
		if _, err := tx.Exec(`INSERT INTO user_settings (user_id, key, value) VALUES (?, ?, ?) ON CONFLICT(user_id, key) DO UPDATE SET value = excluded.value`, uid, k, v); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MoveSettings moves instance settings into the personal settings of a user
// and removes them from the instance (once, when updating to several users).
func (s *Store) MoveSettings(uid int64, keys []string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, k := range keys {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO user_settings (user_id, key, value) SELECT ?, key, value FROM settings WHERE key = ?`, uid, k); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM settings WHERE key = ?`, k); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// ---- ownership ----

// TileBoard returns the dashboard a tile is on.
func (s *Store) TileBoard(tileID int64) (Board, error) {
	b, err := scanBoard(s.db.QueryRow(`SELECT b.id, b.owner_id, b.name, b.icon, b.background, b.theme, b.position
		FROM tiles t JOIN groups g ON g.id = t.group_id JOIN boards b ON b.id = g.board_id WHERE t.id = ?`, tileID))
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return b, err
}

// TileOwners maps every tile to the owner of its dashboard.
func (s *Store) TileOwners() (map[int64]int64, error) {
	rows, err := s.db.Query(`SELECT t.id, b.owner_id FROM tiles t JOIN groups g ON g.id = t.group_id JOIN boards b ON b.id = g.board_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]int64{}
	for rows.Next() {
		var t, o int64
		if err := rows.Scan(&t, &o); err != nil {
			return nil, err
		}
		out[t] = o
	}
	return out, rows.Err()
}

// ---- shares ----

// Share is a dashboard offered to another user. It shows up for them only
// after they accepted it, and only the owner can change it; Actions allows
// them to use the buttons on its tiles (switching, pausing …).
type Share struct {
	BoardID   int64  `json:"boardId"`
	BoardName string `json:"boardName"`
	BoardIcon string `json:"boardIcon"`
	OwnerID   int64  `json:"ownerId"`
	Owner     string `json:"owner"`
	UserID    int64  `json:"userId"`
	Username  string `json:"username"`
	Accepted  bool   `json:"accepted"`
	Actions   bool   `json:"actions"`
	Created   int64  `json:"created"`
}

const shareQuery = `SELECT s.board_id, b.name, b.icon, b.owner_id, o.username, s.user_id, u.username, s.accepted, s.actions, s.created
	FROM shares s JOIN boards b ON b.id = s.board_id JOIN users o ON o.id = b.owner_id JOIN users u ON u.id = s.user_id`

func (s *Store) queryShares(where string, args ...any) ([]Share, error) {
	rows, err := s.db.Query(shareQuery+` WHERE `+where+` ORDER BY s.created, s.board_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Share{}
	for rows.Next() {
		var sh Share
		if err := rows.Scan(&sh.BoardID, &sh.BoardName, &sh.BoardIcon, &sh.OwnerID, &sh.Owner, &sh.UserID, &sh.Username, &sh.Accepted, &sh.Actions, &sh.Created); err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// SharesFor returns the dashboards shared with a user, accepted or not.
func (s *Store) SharesFor(uid int64) ([]Share, error) {
	return s.queryShares(`s.user_id = ? AND u.disabled = 0 AND o.disabled = 0`, uid)
}

// SharesBy returns the shares of the dashboards a user owns.
func (s *Store) SharesBy(owner int64) ([]Share, error) {
	return s.queryShares(`b.owner_id = ?`, owner)
}

func (s *Store) Share(boardID, uid int64) (Share, error) {
	list, err := s.queryShares(`s.board_id = ? AND s.user_id = ?`, boardID, uid)
	if err == nil && len(list) == 0 {
		err = ErrNotFound
	}
	if err != nil {
		return Share{}, err
	}
	return list[0], nil
}

// OfferShare offers a dashboard to a user; an existing share keeps its state
// and only gets the new permission.
func (s *Store) OfferShare(boardID, uid int64, actions bool) error {
	_, err := s.db.Exec(`INSERT INTO shares (board_id, user_id, actions, created) VALUES (?, ?, ?, ?)
		ON CONFLICT(board_id, user_id) DO UPDATE SET actions = excluded.actions`, boardID, uid, actions, time.Now().Unix())
	return err
}

func (s *Store) AcceptShare(boardID, uid int64) error {
	res, err := s.db.Exec(`UPDATE shares SET accepted = 1 WHERE board_id = ? AND user_id = ?`, boardID, uid)
	if err == nil {
		if n, _ := res.RowsAffected(); n == 0 {
			err = ErrNotFound
		}
	}
	return err
}

func (s *Store) DeleteShare(boardID, uid int64) error {
	_, err := s.db.Exec(`DELETE FROM shares WHERE board_id = ? AND user_id = ?`, boardID, uid)
	return err
}

// ---- invitations ----

// Invite is a one-time link to create an account. Only a hash of its token is stored.
type Invite struct {
	ID        int64  `json:"id"`
	CreatedBy string `json:"createdBy"`
	Expires   int64  `json:"expires"`
	External  bool   `json:"external"` // the account made with it is external
}

func (s *Store) CreateInvite(tokenHash string, by int64, expires time.Time, external bool) (int64, error) {
	s.db.Exec(`DELETE FROM invites WHERE expires < ?`, time.Now().Unix())
	res, err := s.db.Exec(`INSERT INTO invites (token_hash, created_by, expires, external) VALUES (?, ?, ?, ?)`, tokenHash, by, expires.Unix(), external)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) Invites() ([]Invite, error) {
	rows, err := s.db.Query(`SELECT i.id, COALESCE(u.username, ''), i.expires, i.external FROM invites i LEFT JOIN users u ON u.id = i.created_by WHERE i.expires >= ? ORDER BY i.id`, time.Now().Unix())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Invite{}
	for rows.Next() {
		var in Invite
		if err := rows.Scan(&in.ID, &in.CreatedBy, &in.Expires, &in.External); err != nil {
			return nil, err
		}
		out = append(out, in)
	}
	return out, rows.Err()
}

// InviteValid reports whether an unused, unexpired invitation has this token hash.
func (s *Store) InviteValid(tokenHash string) bool {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM invites WHERE token_hash = ? AND expires >= ?`, tokenHash, time.Now().Unix()).Scan(&n)
	return n > 0
}

// UseInvite removes the invitation; false if it was not valid (any more).
// It also tells whether the account made with it is external.
func (s *Store) UseInvite(tokenHash string) (ok, external bool) {
	err := s.db.QueryRow(`DELETE FROM invites WHERE token_hash = ? AND expires >= ? RETURNING external`, tokenHash, time.Now().Unix()).Scan(&external)
	return err == nil, external
}

func (s *Store) DeleteInvite(id int64) error {
	_, err := s.db.Exec(`DELETE FROM invites WHERE id = ?`, id)
	return err
}
