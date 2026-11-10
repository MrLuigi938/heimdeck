package server

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/store"
)

// Uploaded pictures: tile icons and backgrounds. The browser scales them
// down before sending (backgrounds to at most 2560 px, icons to 256 px), so
// a 20 MB photo from the phone arrives as a few hundred KB and the dashboard
// stays fast. The server checks type and size and keeps them in the database.

const (
	maxIconBytes       = 1 << 20 // after scaling; SVGs are sent as they are
	maxBackgroundBytes = 6 << 20
	maxUploadsPerUser  = 300
	maxUploadBytesUser = 200 << 20
)

var (
	errUploadType  = errors.New("Nur Bilder als PNG, JPEG, WebP oder GIF (Icons auch SVG)")
	errUploadSize  = errors.New("Das Bild ist zu groß")
	errUploadQuota = errors.New("Speicherplatz für eigene Bilder ist voll – alte Bilder löschen")
	uploadIDRe     = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// uploadMime finds out what a picture really is (never trusting the name).
func uploadMime(data []byte, kind string) string {
	head := bytes.TrimSpace(data[:min(len(data), 1024)])
	if kind == "icon" && (bytes.HasPrefix(head, []byte("<svg")) || bytes.HasPrefix(head, []byte("<?xml")) && bytes.Contains(head, []byte("<svg"))) {
		return "image/svg+xml"
	}
	switch m := http.DetectContentType(data); m {
	case "image/png", "image/jpeg", "image/webp", "image/gif":
		return m
	}
	return ""
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
		Data string `json:"data"` // base64
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 10<<20)).Decode(&body); err != nil {
		writeErr(w, 413, errUploadSize)
		return
	}
	if body.Kind != "icon" && body.Kind != "background" {
		writeErr(w, 400, errors.New("unbekannte Art von Bild"))
		return
	}
	data, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil || len(data) == 0 {
		writeErr(w, 400, errUploadType)
		return
	}
	limit := maxIconBytes
	if body.Kind == "background" {
		limit = maxBackgroundBytes
	}
	if len(data) > limit {
		writeErr(w, 413, errUploadSize)
		return
	}
	mime := uploadMime(data, body.Kind)
	if mime == "" {
		writeErr(w, 415, errUploadType)
		return
	}
	me := uid(r)
	if n, size := s.store.UploadUsage(me); n >= maxUploadsPerUser || size+len(data) > maxUploadBytesUser {
		writeErr(w, 413, errUploadQuota)
		return
	}
	buf := make([]byte, 16)
	rand.Read(buf)
	u := store.Upload{ID: hex.EncodeToString(buf), OwnerID: me, Kind: body.Kind, Mime: mime, Name: strings.TrimSpace(body.Name), Data: data}
	if len(u.Name) > 120 {
		u.Name = u.Name[:120]
	}
	if err := s.store.SaveUpload(u); err != nil {
		writeErr(w, 500, err)
		return
	}
	slog.Debug("Bild hochgeladen", "art", u.Kind, "typ", mime, "größe", len(data), "benutzer", s.username(r))
	writeJSON(w, map[string]any{"id": u.ID, "url": "/api/uploads/" + u.ID, "kind": u.Kind, "size": len(data)})
}

// handleGetUpload serves a picture to every logged-in user: icons and
// backgrounds also show on dashboards shared with others. The address is a
// random ID that cannot be guessed.
func (s *Server) handleGetUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !uploadIDRe.MatchString(id) {
		writeErr(w, 404, errNotFound)
		return
	}
	u, err := s.store.Upload(id)
	if err != nil {
		writeErr(w, 404, errNotFound)
		return
	}
	h := w.Header()
	h.Set("Content-Type", u.Mime)
	h.Set("Cache-Control", "private, max-age=31536000, immutable") // a new picture gets a new ID
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Write(u.Data)
}

func (s *Server) handleListUploads(w http.ResponseWriter, r *http.Request) {
	list, err := s.store.Uploads(uid(r), r.URL.Query().Get("kind"))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	count, size := s.store.UploadUsage(uid(r))
	writeJSON(w, map[string]any{"uploads": list, "count": count, "bytes": size, "maxBytes": maxUploadBytesUser})
}

func (s *Server) handleDeleteUpload(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteUpload(r.PathValue("id"), uid(r)); err != nil {
		writeErr(w, 404, errNotFound)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}

// ---- photo albums as backgrounds ----

// handleAlbums lists the albums of one of the user's own photo tiles (Immich, PhotoPrism).
func (s *Server) handleAlbums(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	t, code, err := s.ownTile(r, id)
	if err != nil {
		writeErr(w, code, err)
		return
	}
	integ, _ := integrations.Get(t.Integration)
	src, ok := integ.(integrations.AlbumSource)
	if !ok {
		writeErr(w, 400, errors.New("Dieser Dienst hat keine Alben"))
		return
	}
	ctx, cancel := timeout(r, 15*time.Second)
	defer cancel()
	list, err := src.Albums(ctx, s.instance(t, langOf(r)))
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, list)
}

// handleAlbumImages returns the pictures of an album for a background. Anyone
// who sees the tile's dashboard may load them (a shared dashboard keeps its
// background), the pictures go through the image proxy.
func (s *Server) handleAlbumImages(w http.ResponseWriter, r *http.Request) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	// The owner's own tile or one on a dashboard the user can see – not in a
	// locked group.
	t, code, err := s.accessibleTile(r, id)
	if err != nil {
		writeErr(w, code, err)
		return
	}
	integ, _ := integrations.Get(t.Integration)
	src, ok := integ.(integrations.AlbumSource)
	if !ok {
		writeErr(w, 404, errNotFound)
		return
	}
	ctx, cancel := timeout(r, 20*time.Second)
	defer cancel()
	images, err := src.AlbumImages(ctx, s.instance(t, langOf(r)), r.PathValue("album"))
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	for _, img := range images {
		noteImage(uid(r), t.ID, img)
	}
	writeJSON(w, map[string]any{"images": images})
}
