package server

import (
	"errors"
	"log/slog"
	"net/http"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/store"
)

// Who may see and change what: every dashboard belongs to one user. Others
// see it only after it was shared with them and they accepted; they can
// look at it but not change it, and use its buttons only if the owner allowed
// that. The admin manages the accounts and the instance-wide settings, but
// does not see the dashboards of the others either.

var (
	errNotFound    = errors.New("nicht gefunden")
	errReadOnly    = errors.New("Dieses Dashboard ist nur zum Ansehen freigegeben")
	errNoAct       = errors.New("Die Knöpfe dieses Dashboards sind für dich nicht freigegeben")
	errAdmin       = errors.New("Das darf nur ein Admin")
	errDockerOwner = errors.New("Diese Kachel funktioniert nur, wenn sie einem Admin gehört")
)

// adminTiles read the host itself without any key – its containers, its
// load – so only admins can add them.
var adminTiles = map[string]bool{"docker": true, "system": true}

// dockerAllowed: a host tile (Docker, System) only works while its owner is
// an admin – also after a demotion.
func (s *Server) dockerAllowed(t store.Tile) bool {
	if !adminTiles[t.Integration] {
		return true
	}
	b, err := s.store.TileBoard(t.ID)
	if err != nil {
		return false
	}
	u, err := s.store.User(b.OwnerID)
	return err == nil && u.Role == store.RoleAdmin
}

// uid returns the logged-in user of an /api request.
func uid(r *http.Request) int64 { return auth.FromContext(r.Context()) }

func (s *Server) isAdmin(r *http.Request) bool {
	u, err := s.store.User(uid(r))
	return err == nil && u.Role == store.RoleAdmin
}

// adminOnly wraps handlers for instance-wide things: accounts, Docker, Proxmox discovery.
func (s *Server) adminOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.isAdmin(r) {
			writeErr(w, 403, errAdmin)
			return
		}
		h(w, r)
	}
}

// boardAccess is what a user may do on a dashboard.
type boardAccess struct {
	Own bool // owner: everything
	Act bool // may use buttons (owner, or a share with actions)
}

// boardsFor returns the dashboards a user sees: their own and the accepted shares.
func (s *Server) boardsFor(user int64) (map[int64]boardAccess, error) {
	own, err := s.store.BoardsOf(user)
	if err != nil {
		return nil, err
	}
	out := map[int64]boardAccess{}
	for _, b := range own {
		out[b.ID] = boardAccess{Own: true, Act: true}
	}
	shares, err := s.store.SharesFor(user)
	if err != nil {
		return nil, err
	}
	for _, sh := range shares {
		if sh.Accepted {
			out[sh.BoardID] = boardAccess{Act: sh.Actions}
		}
	}
	return out, nil
}

// groupBoards maps every group to its dashboard.
func (s *Server) groupBoards() (map[int64]int64, error) {
	groups, err := s.store.Groups()
	if err != nil {
		return nil, err
	}
	out := map[int64]int64{}
	for _, g := range groups {
		out[g.ID] = g.BoardID
	}
	return out, nil
}

// accessibleTiles returns the tiles the user sees: on their dashboards, outside locked groups.
func (s *Server) accessibleTiles(r *http.Request) ([]store.Tile, error) {
	boards, err := s.boardsFor(uid(r))
	if err != nil {
		return nil, err
	}
	gb, err := s.groupBoards()
	if err != nil {
		return nil, err
	}
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
		if _, ok := boards[gb[t.GroupID]]; ok && !locked[t.GroupID] {
			out = append(out, t)
		}
	}
	return out, nil
}

// tileAccess loads a tile the user may see; a tile of someone else's
// dashboard looks as if it did not exist.
func (s *Server) tileAccess(r *http.Request, id int64) (store.Tile, boardAccess, int, error) {
	t, err := s.store.Tile(id)
	if err != nil {
		return t, boardAccess{}, 404, errNotFound
	}
	b, err := s.store.TileBoard(id)
	if err != nil {
		return t, boardAccess{}, 404, errNotFound
	}
	boards, err := s.boardsFor(uid(r))
	if err != nil {
		return t, boardAccess{}, 500, err
	}
	acc, ok := boards[b.ID]
	if !ok {
		return t, acc, 404, errNotFound
	}
	if ok, err := s.groupUnlocked(r, t.GroupID); err != nil {
		return t, acc, 500, err
	} else if !ok {
		return t, acc, 403, errLocked
	}
	return t, acc, 200, nil
}

// accessibleTile loads a tile the user may look at.
func (s *Server) accessibleTile(r *http.Request, id int64) (store.Tile, int, error) {
	t, _, code, err := s.tileAccess(r, id)
	return t, code, err
}

// ownTile loads a tile the user may change.
func (s *Server) ownTile(r *http.Request, id int64) (store.Tile, int, error) {
	t, acc, code, err := s.tileAccess(r, id)
	if err == nil && !acc.Own {
		return t, 403, errReadOnly
	}
	return t, code, err
}

// ownBoard loads a dashboard the user owns.
func (s *Server) ownBoard(r *http.Request, id int64) (store.Board, int, error) {
	b, err := s.store.Board(id)
	if err != nil {
		return b, 404, errNotFound
	}
	if b.OwnerID != uid(r) {
		if _, err := s.store.Share(id, uid(r)); err == nil {
			return b, 403, errReadOnly
		}
		return b, 404, errNotFound
	}
	return b, 200, nil
}

// ownGroup loads a group on a dashboard the user owns. Locked groups are
// refused separately by the handlers that need it.
func (s *Server) ownGroup(r *http.Request, id int64) (store.Group, int, error) {
	g, err := s.store.Group(id)
	if err != nil {
		return g, 404, errNotFound
	}
	if _, code, err := s.ownBoard(r, g.BoardID); err != nil {
		return g, code, err
	}
	return g, 200, nil
}

// seeGroup loads a group the user may look at (to unlock it).
func (s *Server) seeGroup(r *http.Request, id int64) (store.Group, boardAccess, error) {
	g, err := s.store.Group(id)
	if err != nil {
		return g, boardAccess{}, errNotFound
	}
	boards, err := s.boardsFor(uid(r))
	if err != nil {
		return g, boardAccess{}, err
	}
	acc, ok := boards[g.BoardID]
	if !ok {
		return g, acc, errNotFound
	}
	return g, acc, nil
}

// firstBoard returns the user's first dashboard, creating one if they have none.
func (s *Server) firstBoard(user int64) (int64, error) {
	boards, err := s.store.BoardsOf(user)
	if err != nil {
		return 0, err
	}
	if len(boards) > 0 {
		return boards[0].ID, nil
	}
	b := store.Board{OwnerID: user, Name: "Privat", Icon: "🏡"}
	if err := s.store.SaveBoard(&b); err != nil {
		return 0, err
	}
	slog.Debug("Erstes Dashboard angelegt", "benutzer", user, "dashboard", b.ID)
	return b.ID, nil
}
