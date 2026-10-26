package server

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/i18n"
	"github.com/heimdeck/heimdeck/internal/store"
)

// inviteTTL is how long an invitation link works.
const inviteTTL = 7 * 24 * time.Hour

var (
	errBadAccount = errors.New("Benutzername angeben und Passwort mit mindestens 8 Zeichen wählen")
	errLastAdmin  = errors.New("Es muss mindestens ein aktiver Admin bleiben")
	errSelf       = errors.New("Das eigene Konto kann hier nicht gesperrt, herabgestuft oder gelöscht werden")
	errInvite     = errors.New("Dieser Einladungslink ist ungültig oder abgelaufen")
)

func validAccount(name, password string) bool {
	return name != "" && len(name) <= 64 && len(password) >= 8
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// ---- accounts (admin) ----

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.Users()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	invites, err := s.store.Invites()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, map[string]any{"users": users, "invites": invites, "me": uid(r)})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	if !validAccount(body.Username, body.Password) {
		writeErr(w, 400, errBadAccount)
		return
	}
	if body.Role != store.RoleAdmin {
		body.Role = store.RoleUser
	}
	id, err := s.auth.Register(nil, body.Username, body.Password, body.Role)
	if err != nil {
		code := 500
		if errors.Is(err, store.ErrNameTaken) {
			code = 409
		}
		writeErr(w, code, err)
		return
	}
	s.newAccount(id, langOf(r))
	slog.Info("Benutzer angelegt", "benutzer", body.Username, "rolle", body.Role, "von", s.username(r))
	u, _ := s.store.User(id)
	writeJSON(w, u)
}

func (s *Server) username(r *http.Request) string {
	name, _ := s.store.Username(uid(r))
	return name
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	var body struct {
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
		Password string  `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	u, err := s.store.User(id)
	if err != nil {
		writeErr(w, 404, errNotFound)
		return
	}
	wasAdmin := u.Role == store.RoleAdmin && !u.Disabled
	if body.Role != nil {
		if *body.Role != store.RoleAdmin && *body.Role != store.RoleUser {
			writeErr(w, 400, errors.New("unbekannte Rolle"))
			return
		}
		u.Role = *body.Role
	}
	if body.Disabled != nil {
		u.Disabled = *body.Disabled
	}
	stillAdmin := u.Role == store.RoleAdmin && !u.Disabled
	if id == uid(r) && !stillAdmin {
		writeErr(w, 400, errSelf)
		return
	}
	if wasAdmin && !stillAdmin && s.store.AdminCount() <= 1 {
		writeErr(w, 400, errLastAdmin)
		return
	}
	if body.Password != "" {
		if len(body.Password) < 8 {
			writeErr(w, 400, errors.New("Das neue Passwort braucht mindestens 8 Zeichen"))
			return
		}
		hash, _ := auth.HashPassword(body.Password)
		if err := s.store.SetPassword(u.Username, hash); err != nil {
			writeErr(w, 500, err)
			return
		}
		slog.Info("Passwort zurückgesetzt", "benutzer", u.Username, "von", s.username(r))
	}
	if err := s.store.UpdateUser(u); err != nil {
		writeErr(w, 500, err)
		return
	}
	slog.Info("Benutzer geändert", "benutzer", u.Username, "rolle", u.Role, "gesperrt", u.Disabled, "von", s.username(r))
	writeJSON(w, u)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if id == uid(r) {
		writeErr(w, 400, errSelf)
		return
	}
	u, err := s.store.User(id)
	if err != nil {
		writeErr(w, 404, errNotFound)
		return
	}
	if u.Role == store.RoleAdmin && !u.Disabled && s.store.AdminCount() <= 1 {
		writeErr(w, 400, errLastAdmin)
		return
	}
	if err := s.store.DeleteUser(id); err != nil {
		writeErr(w, 500, err)
		return
	}
	slog.Info("Benutzer gelöscht", "benutzer", u.Username, "von", s.username(r))
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- invitations ----

func (s *Server) handleCreateInvite(w http.ResponseWriter, r *http.Request) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		writeErr(w, 500, err)
		return
	}
	token := hex.EncodeToString(buf)
	expires := time.Now().Add(inviteTTL)
	id, err := s.store.CreateInvite(hashToken(token), uid(r), expires)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	slog.Info("Einladungslink erstellt", "von", s.username(r), "gültig bis", expires.Format("2006-01-02 15:04"))
	// The token is only shown now; the database keeps its hash.
	writeJSON(w, map[string]any{"id": id, "token": token, "expires": expires.Unix()})
}

func (s *Server) handleDeleteInvite(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.store.DeleteInvite(id); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// handleInviteInfo tells the join page whether the link still works.
func (s *Server) handleInviteInfo(w http.ResponseWriter, r *http.Request) {
	valid := s.store.InviteValid(hashToken(r.PathValue("token")))
	if !valid {
		time.Sleep(200 * time.Millisecond) // slow down guessing
	}
	settings, _ := s.store.Settings()
	writeJSON(w, map[string]any{"valid": valid, "language": settings["language"]})
}

// handleJoin creates an account from an invitation and logs it in.
func (s *Server) handleJoin(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if err := readJSON(r, &c); err != nil {
		writeErr(w, 400, err)
		return
	}
	c.Username = strings.TrimSpace(c.Username)
	if !validAccount(c.Username, c.Password) {
		writeErr(w, 400, errBadAccount)
		return
	}
	if _, _, err := s.store.UserByName(c.Username); err == nil {
		writeErr(w, 409, store.ErrNameTaken)
		return
	}
	if !s.store.UseInvite(hashToken(r.PathValue("token"))) {
		time.Sleep(200 * time.Millisecond)
		writeErr(w, 403, errInvite)
		return
	}
	id, err := s.auth.Register(w, c.Username, c.Password, store.RoleUser)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	s.newAccount(id, i18n.Normalize(c.Language))
	slog.Info("Konto über Einladung angelegt", "benutzer", c.Username, "von", r.RemoteAddr)
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- sharing ----

// handlePeople lists the other active accounts, to pick whom to share with.
func (s *Server) handlePeople(w http.ResponseWriter, r *http.Request) {
	users, err := s.store.Users()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	type person struct {
		ID       int64  `json:"id"`
		Username string `json:"username"`
	}
	out := []person{}
	for _, u := range users {
		if u.ID != uid(r) && !u.Disabled {
			out = append(out, person{u.ID, u.Username})
		}
	}
	writeJSON(w, out)
}

// handleShares returns the dashboards shared with the user and the ones they share.
func (s *Server) handleShares(w http.ResponseWriter, r *http.Request) {
	in, err := s.store.SharesFor(uid(r))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	out, err := s.store.SharesBy(uid(r))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, map[string]any{"incoming": in, "outgoing": out})
}

func shareTarget(r *http.Request) (int64, error) {
	id, err := strconv.ParseInt(r.PathValue("user"), 10, 64)
	if err != nil {
		return 0, errors.New("ungültige ID")
	}
	return id, nil
}

// handleShareBoard offers a dashboard to a user, or changes whether they may use its buttons.
func (s *Server) handleShareBoard(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	target, err := shareTarget(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	var body struct {
		Actions bool `json:"actions"`
	}
	if err := readJSON(r, &body); err != nil {
		writeErr(w, 400, err)
		return
	}
	b, code, err := s.ownBoard(r, id)
	if err != nil {
		writeErr(w, code, err)
		return
	}
	u, err := s.store.User(target)
	if err != nil || u.Disabled || u.ID == uid(r) {
		writeErr(w, 400, errors.New("unbekannter Benutzer"))
		return
	}
	if err := s.store.OfferShare(id, target, body.Actions); err != nil {
		writeErr(w, 500, err)
		return
	}
	slog.Info("Dashboard freigegeben", "dashboard", b.Name, "für", u.Username, "knöpfe", body.Actions, "von", s.username(r))
	sh, _ := s.store.Share(id, target)
	writeJSON(w, sh)
}

// handleUnshareBoard ends a share: the owner takes it back, or the other user
// declines or leaves it.
func (s *Server) handleUnshareBoard(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	target, err := shareTarget(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	sh, err := s.store.Share(id, target)
	if err != nil || (sh.OwnerID != uid(r) && target != uid(r)) {
		writeErr(w, 404, errNotFound)
		return
	}
	if err := s.store.DeleteShare(id, target); err != nil {
		writeErr(w, 500, err)
		return
	}
	slog.Info("Freigabe beendet", "dashboard", sh.BoardName, "für", sh.Username, "von", s.username(r))
	writeJSON(w, map[string]bool{"ok": true})
}

func (s *Server) handleAcceptShare(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.store.AcceptShare(id, uid(r)); err != nil {
		writeErr(w, 404, errNotFound)
		return
	}
	slog.Info("Freigabe angenommen", "dashboard", id, "von", s.username(r))
	writeJSON(w, map[string]bool{"ok": true})
}
