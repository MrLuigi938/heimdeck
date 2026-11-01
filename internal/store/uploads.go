package store

import (
	"database/sql"
	"errors"
	"time"
)

// Upload is a picture someone uploaded: a tile icon or a background. The
// browser scales it down before it is sent, the database keeps the result.
type Upload struct {
	ID      string `json:"id"`
	OwnerID int64  `json:"-"`
	Kind    string `json:"kind"` // icon | background
	Mime    string `json:"mime"`
	Size    int    `json:"size"`
	Name    string `json:"name"`
	Created int64  `json:"created"`
	Data    []byte `json:"-"`
}

const uploadSchema = `
CREATE TABLE IF NOT EXISTS uploads (
	id TEXT PRIMARY KEY,
	owner_id INTEGER NOT NULL,
	kind TEXT NOT NULL,
	mime TEXT NOT NULL,
	name TEXT NOT NULL DEFAULT '',
	data BLOB NOT NULL,
	created INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS uploads_owner ON uploads (owner_id, kind);`

func (s *Store) SaveUpload(u Upload) error {
	_, err := s.db.Exec(`INSERT INTO uploads (id, owner_id, kind, mime, name, data, created) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		u.ID, u.OwnerID, u.Kind, u.Mime, u.Name, u.Data, time.Now().Unix())
	return err
}

// Upload loads one picture with its data.
func (s *Store) Upload(id string) (Upload, error) {
	var u Upload
	err := s.db.QueryRow(`SELECT id, owner_id, kind, mime, name, data, created FROM uploads WHERE id = ?`, id).
		Scan(&u.ID, &u.OwnerID, &u.Kind, &u.Mime, &u.Name, &u.Data, &u.Created)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	u.Size = len(u.Data)
	return u, err
}

// Uploads lists a user's pictures of one kind ("" = all), newest first, without data.
func (s *Store) Uploads(owner int64, kind string) ([]Upload, error) {
	rows, err := s.db.Query(`SELECT id, owner_id, kind, mime, name, length(data), created FROM uploads
		WHERE owner_id = ? AND (? = '' OR kind = ?) ORDER BY created DESC, id`, owner, kind, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Upload{}
	for rows.Next() {
		var u Upload
		if err := rows.Scan(&u.ID, &u.OwnerID, &u.Kind, &u.Mime, &u.Name, &u.Size, &u.Created); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// UploadUsage returns how many pictures and bytes a user has stored.
func (s *Store) UploadUsage(owner int64) (count, bytes int) {
	s.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(length(data)), 0) FROM uploads WHERE owner_id = ?`, owner).Scan(&count, &bytes)
	return
}

func (s *Store) DeleteUpload(id string, owner int64) error {
	res, err := s.db.Exec(`DELETE FROM uploads WHERE id = ? AND owner_id = ?`, id, owner)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
