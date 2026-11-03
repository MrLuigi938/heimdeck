package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/heimdeck/heimdeck/internal/camera"
	"github.com/heimdeck/heimdeck/internal/i18n"
	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/logging"
	"github.com/heimdeck/heimdeck/internal/store"
)

// Camera tiles: the live stream and still pictures go through Heimdeck (the
// browser never gets the camera's address or login), and a watcher per tile
// collects its events.

const (
	liveMax       = 30 * time.Minute // a forgotten tab does not keep a camera awake for ever
	notifyCamGap  = 5 * time.Minute
	watcherRetry  = 30 * time.Second
	cameraTimeout = 20 * time.Second
)

// cameraTile loads a camera tile the user may see.
func (s *Server) cameraTile(w http.ResponseWriter, r *http.Request) (store.Tile, *integrations.Instance, bool) {
	id, err := pathID(r)
	if err != nil {
		writeErr(w, 400, err)
		return store.Tile{}, nil, false
	}
	t, code, err := s.accessibleTile(r, id)
	if err != nil {
		writeErr(w, code, err)
		return t, nil, false
	}
	if t.Integration != "camera" {
		writeErr(w, 404, errNotFound)
		return t, nil, false
	}
	return t, s.instance(t, langOf(r)), true
}

// handleCameraLive streams the camera as fragmented MP4 until the viewer leaves.
func (s *Server) handleCameraLive(w http.ResponseWriter, r *http.Request) {
	t, in, ok := s.cameraTile(w, r)
	if !ok {
		return
	}
	src, err := integrations.CameraSource(r.Context(), in)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	live, err := camera.Watch(r.Context(), src)
	if err != nil {
		slog.Debug("Kamera-Livebild", "kachel", t.Name, "fehler", camera.RedactURL(err.Error()))
		writeErr(w, 502, errors.New(in.T(err.Error())))
		return
	}
	defer live.Close()
	slog.Debug("Kamera-Livebild gestartet", "kachel", t.Name, "codec", live.Codec)
	w.Header().Set("Content-Type", "video/mp4")
	w.Header().Set("X-Codec", live.Codec)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	rc := http.NewResponseController(w)
	if _, err := w.Write(live.Init); err != nil {
		return
	}
	rc.Flush()
	stop := time.NewTimer(liveMax)
	defer stop.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-stop.C:
			return
		case frame, ok := <-live.Frames:
			if !ok {
				return
			}
			rc.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if _, err := w.Write(frame); err != nil {
				return
			}
			rc.Flush()
		}
	}
}

// handleCameraStill returns a current picture: the camera's own JPEG when it
// has a snapshot address, else a key frame from the stream as a tiny MP4.
func (s *Server) handleCameraStill(w http.ResponseWriter, r *http.Request) {
	t, in, ok := s.cameraTile(w, r)
	if !ok {
		return
	}
	ctx, cancel := timeout(r, cameraTimeout)
	defer cancel()
	// fresh: how old a kept picture may be, in seconds – the tile asks for
	// about half its refresh time, a click on ⟳ for a new one.
	fresh, err := strconv.Atoi(r.URL.Query().Get("fresh"))
	if err != nil || fresh <= 0 {
		fresh = 30
	}
	taken := time.Now()
	data, ct, err := integrations.CameraSnapshot(ctx, in)
	codec := ""
	if errors.Is(err, integrations.ErrNoSnapshot) {
		var src camera.Source
		if src, err = integrations.CameraSource(ctx, in); err == nil {
			data, codec, taken, err = camera.Still(ctx, src, time.Duration(fresh)*time.Second)
			ct = "video/mp4"
		}
	}
	if err != nil {
		slog.Debug("Kamera-Standbild", "kachel", t.Name, "fehler", camera.RedactURL(err.Error()))
		writeErr(w, 502, errors.New(in.T(err.Error())))
		return
	}
	w.Header().Set("X-Taken", taken.UTC().Format(time.RFC3339))
	writeCameraImage(w, data, ct, codec)
}

func writeCameraImage(w http.ResponseWriter, data []byte, ct, codec string) {
	w.Header().Set("Content-Type", ct)
	if codec != "" {
		w.Header().Set("X-Codec", codec)
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Write(data)
}

// handleCameraEvent returns the picture of one event.
func (s *Server) handleCameraEvent(w http.ResponseWriter, r *http.Request) {
	t, _, ok := s.cameraTile(w, r)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("event"), 10, 64)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	e, ok := camera.EventImage(t.ID, id)
	if !ok {
		writeErr(w, 404, errNotFound)
		return
	}
	w.Header().Set("X-Taken", e.At.UTC().Format(time.RFC3339))
	writeCameraImage(w, e.Image, e.Type, e.Codec)
}

// camWatcher collects the events of one camera tile in the background.
type camWatcher struct {
	sig    string // what it was started with; a change restarts it
	cancel context.CancelFunc
}

var (
	camWatchMu sync.Mutex
	camWatch   = map[int64]*camWatcher{}
	camNotify  = map[int64]time.Time{}
)

// syncCameraWatchers starts and stops the event watchers to match the tiles.
func (s *Server) syncCameraWatchers(ctx context.Context) {
	tiles, err := s.store.Tiles()
	if err != nil {
		return
	}
	want := map[int64]string{}
	byID := map[int64]store.Tile{}
	for _, t := range tiles {
		if t.Integration != "camera" {
			continue
		}
		in := s.instance(t, i18n.DE)
		detect := integrations.CameraDetect(in)
		if detect == "" {
			continue
		}
		want[t.ID] = fmt.Sprint(detect, in.Values, t.Insecure, in.PublicOnly)
		byID[t.ID] = t
	}
	camWatchMu.Lock()
	defer camWatchMu.Unlock()
	for id, cw := range camWatch {
		if want[id] != cw.sig {
			cw.cancel()
			delete(camWatch, id)
		}
	}
	for id, sig := range want {
		if camWatch[id] != nil {
			continue
		}
		c, cancel := context.WithCancel(ctx)
		camWatch[id] = &camWatcher{sig: sig, cancel: cancel}
		go s.watchCamera(c, byID[id])
	}
}

// watchCamera runs one tile's watcher, retrying after failures.
func (s *Server) watchCamera(ctx context.Context, t store.Tile) {
	defer logging.Recover("Kamera-Ereignisse")
	on := func(e camera.Event) {
		e = camera.AddEvent(t.ID, e)
		slog.Info("Kamera-Ereignis", "kachel", t.Name, "art", e.Kind)
		s.forget(t.ID)
		s.notifyCamera(t, e)
	}
	for ctx.Err() == nil {
		in := s.instance(t, i18n.DE)
		var err error
		switch integrations.CameraDetect(in) {
		case "stream":
			var src camera.Source
			if src, err = integrations.CameraSource(ctx, in); err == nil {
				camera.WatchMotion(ctx, src, in.V("sensitivity"), on)
			}
		case "onvif":
			o := integrations.CameraONVIF(in)
			err = o.PullEvents(ctx, func(kind string) {
				go func() {
					e := camera.Event{At: time.Now(), Kind: kind}
					c, cancel := context.WithTimeout(ctx, cameraTimeout)
					defer cancel()
					if data, ct, err := integrations.CameraSnapshot(c, in); err == nil {
						e.Image, e.Type = data, ct
					} else if src, err := integrations.CameraSource(c, in); err == nil {
						if data, codec, _, err := camera.Still(c, src, 5*time.Second); err == nil {
							e.Image, e.Type, e.Codec = data, "video/mp4", codec
						}
					}
					on(e)
				}()
			})
		default:
			return
		}
		if err != nil && ctx.Err() == nil {
			slog.Warn("Kamera-Ereignisse", "kachel", t.Name, "fehler", camera.RedactURL(err.Error()))
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(watcherRetry):
		}
	}
}

// notifyCamera tells the tile's owner about an event, at most every few minutes.
func (s *Server) notifyCamera(t store.Tile, e camera.Event) {
	if t.Config["notify"] != "true" {
		return
	}
	camWatchMu.Lock()
	if time.Since(camNotify[t.ID]) < notifyCamGap {
		camWatchMu.Unlock()
		return
	}
	camNotify[t.ID] = time.Now()
	camWatchMu.Unlock()
	owner := s.tileOwner(t)
	if owner == 0 {
		return
	}
	p := s.notifyPrefs(owner)
	m := notification{Camera: true, URL: t.URL,
		Title: i18n.T(p.lang, "%s: %s", t.Name, i18n.T(p.lang, e.Kind)),
		Body:  i18n.T(p.lang, "Um %s erkannt", e.At.Format("15:04:05"))}
	go s.send(owner, p.settings, []notification{m})
}
