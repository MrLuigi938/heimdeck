package store

import "time"

// GroupShare is a single group offered to another user – instead of the
// whole dashboard. Like a dashboard share it has to be accepted, is view
// only, and Actions allows the buttons on its tiles.
type GroupShare struct {
	GroupID   int64  `json:"groupId"`
	GroupName string `json:"groupName"`
	BoardID   int64  `json:"boardId"`
	OwnerID   int64  `json:"ownerId"`
	Owner     string `json:"owner"`
	UserID    int64  `json:"userId"`
	Username  string `json:"username"`
	Accepted  bool   `json:"accepted"`
	Actions   bool   `json:"actions"`
	Created   int64  `json:"created"`
}

const groupShareSchema = `
CREATE TABLE IF NOT EXISTS group_shares (
	group_id INTEGER NOT NULL,
	user_id INTEGER NOT NULL,
	accepted INTEGER NOT NULL DEFAULT 0,
	actions INTEGER NOT NULL DEFAULT 0,
	created INTEGER NOT NULL DEFAULT 0,
	PRIMARY KEY (group_id, user_id)
);`

const groupShareQuery = `SELECT s.group_id, g.name, g.board_id, b.owner_id, o.username, s.user_id, u.username, s.accepted, s.actions, s.created
	FROM group_shares s JOIN groups g ON g.id = s.group_id JOIN boards b ON b.id = g.board_id
	JOIN users o ON o.id = b.owner_id JOIN users u ON u.id = s.user_id`

func (s *Store) queryGroupShares(where string, args ...any) ([]GroupShare, error) {
	rows, err := s.db.Query(groupShareQuery+` WHERE `+where+` ORDER BY s.created, g.position, s.group_id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []GroupShare{}
	for rows.Next() {
		var sh GroupShare
		if err := rows.Scan(&sh.GroupID, &sh.GroupName, &sh.BoardID, &sh.OwnerID, &sh.Owner, &sh.UserID, &sh.Username, &sh.Accepted, &sh.Actions, &sh.Created); err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// GroupSharesFor returns the groups shared with a user, accepted or not.
func (s *Store) GroupSharesFor(uid int64) ([]GroupShare, error) {
	return s.queryGroupShares(`s.user_id = ? AND u.disabled = 0 AND o.disabled = 0`, uid)
}

// GroupSharesBy returns the shares of the groups a user owns.
func (s *Store) GroupSharesBy(owner int64) ([]GroupShare, error) {
	return s.queryGroupShares(`b.owner_id = ?`, owner)
}

func (s *Store) GroupShare(groupID, uid int64) (GroupShare, error) {
	list, err := s.queryGroupShares(`s.group_id = ? AND s.user_id = ?`, groupID, uid)
	if err == nil && len(list) == 0 {
		err = ErrNotFound
	}
	if err != nil {
		return GroupShare{}, err
	}
	return list[0], nil
}

// OfferGroupShare offers a group to a user; an existing share keeps its state
// and only gets the new permission.
func (s *Store) OfferGroupShare(groupID, uid int64, actions bool) error {
	_, err := s.db.Exec(`INSERT INTO group_shares (group_id, user_id, actions, created) VALUES (?, ?, ?, ?)
		ON CONFLICT(group_id, user_id) DO UPDATE SET actions = excluded.actions`, groupID, uid, actions, time.Now().Unix())
	return err
}

func (s *Store) AcceptGroupShare(groupID, uid int64) error {
	res, err := s.db.Exec(`UPDATE group_shares SET accepted = 1 WHERE group_id = ? AND user_id = ?`, groupID, uid)
	if err == nil {
		if n, _ := res.RowsAffected(); n == 0 {
			err = ErrNotFound
		}
	}
	return err
}

func (s *Store) DeleteGroupShare(groupID, uid int64) error {
	_, err := s.db.Exec(`DELETE FROM group_shares WHERE group_id = ? AND user_id = ?`, groupID, uid)
	return err
}
