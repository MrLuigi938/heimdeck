package store

import (
	"time"
)

// Values sent to a data source tile ("datasource"): one row per value and
// time. They are kept 90 days and go with the tile when it is deleted.
const pointSchema = `
CREATE TABLE IF NOT EXISTS points (
	tile_id INTEGER NOT NULL,
	name TEXT NOT NULL,
	ts INTEGER NOT NULL,
	value REAL NOT NULL
);
CREATE INDEX IF NOT EXISTS points_tile ON points (tile_id, name, ts);`

// Point is one value of a series at a time (milliseconds).
type Point struct {
	Name  string
	T     int64
	Value float64
}

// PointsMaxSeries and PointsMaxRows keep one tile from filling the database.
const (
	PointsMaxSeries = 20
	PointsMaxRows   = 200000
)

// AddPoints stores values of a tile. A value of a series that came less than
// ten seconds after the last one replaces it, so a script in a tight loop
// keeps one point per ten seconds.
func (s *Store) AddPoints(tileID int64, pts []Point) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, p := range pts {
		var last int64
		tx.QueryRow(`SELECT COALESCE(MAX(ts), 0) FROM points WHERE tile_id = ? AND name = ?`, tileID, p.Name).Scan(&last)
		if last > 0 && p.T-last < 10000 && p.T >= last {
			if _, err := tx.Exec(`UPDATE points SET value = ?, ts = ? WHERE tile_id = ? AND name = ? AND ts = ?`, p.Value, p.T, tileID, p.Name, last); err != nil {
				return err
			}
			continue
		}
		if last == 0 {
			var n int
			tx.QueryRow(`SELECT COUNT(DISTINCT name) FROM points WHERE tile_id = ?`, tileID).Scan(&n)
			if n >= PointsMaxSeries {
				continue
			}
		}
		if _, err := tx.Exec(`INSERT INTO points (tile_id, name, ts, value) VALUES (?, ?, ?, ?)`, tileID, p.Name, p.T, p.Value); err != nil {
			return err
		}
	}
	// The oldest go when a tile has too many.
	if _, err := tx.Exec(`DELETE FROM points WHERE tile_id = ? AND rowid IN (
		SELECT rowid FROM points WHERE tile_id = ? ORDER BY ts DESC LIMIT -1 OFFSET ?)`, tileID, tileID, PointsMaxRows); err != nil {
		return err
	}
	return tx.Commit()
}

// Points returns the values of a tile since a time, per series, oldest first.
func (s *Store) Points(tileID int64, since time.Time) (map[string][]Point, error) {
	rows, err := s.db.Query(`SELECT name, ts, value FROM points WHERE tile_id = ? AND ts >= ? ORDER BY ts`, tileID, since.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string][]Point{}
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.Name, &p.T, &p.Value); err != nil {
			return nil, err
		}
		out[p.Name] = append(out[p.Name], p)
	}
	return out, rows.Err()
}

// LatestPoints returns the newest value of each series of a tile.
func (s *Store) LatestPoints(tileID int64) ([]Point, error) {
	rows, err := s.db.Query(`SELECT p.name, p.ts, p.value FROM points p JOIN (
		SELECT name, MAX(ts) AS ts FROM points WHERE tile_id = ? GROUP BY name) m ON p.name = m.name AND p.ts = m.ts
		WHERE p.tile_id = ? ORDER BY p.name`, tileID, tileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Point
	for rows.Next() {
		var p Point
		if err := rows.Scan(&p.Name, &p.T, &p.Value); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PrunePoints drops values older than a time and those of deleted tiles.
func (s *Store) PrunePoints(before time.Time) error {
	if _, err := s.db.Exec(`DELETE FROM points WHERE ts < ?`, before.UnixMilli()); err != nil {
		return err
	}
	_, err := s.db.Exec(`DELETE FROM points WHERE tile_id NOT IN (SELECT id FROM tiles)`)
	return err
}
