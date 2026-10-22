package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/logging"
)

const (
	slowRequest   = 5 * time.Second
	watchInterval = 15 * time.Second
	watchTimeout  = 5 * time.Second
	dumpEvery     = 5 * time.Minute // at most one goroutine dump per hang
)

// statusWriter remembers the status code for the request log.
type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.status == 0 {
		w.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

// logRequests writes API requests to the log in debug mode, slow ones always,
// and turns a panic in a handler into a logged error instead of a broken connection.
func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sw := &statusWriter{ResponseWriter: w}
		start := time.Now()
		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					panic(p)
				}
				slog.Error("Absturz bei einer Anfrage", "methode", r.Method, "pfad", r.URL.Path, "fehler", logging.Redact(fmt.Sprint(p)))
				os.Stderr.Write(debug.Stack())
				if sw.status == 0 {
					writeErr(sw, http.StatusInternalServerError, errors.New("interner Fehler – Details stehen im Protokoll"))
				}
				return
			}
			if !strings.HasPrefix(r.URL.Path, "/api/") {
				return
			}
			d := time.Since(start)
			switch {
			case d > slowRequest:
				slog.Warn("Langsame Anfrage", "methode", r.Method, "pfad", r.URL.Path, "status", sw.status, "dauer", d.Round(time.Millisecond))
			case logging.Debug():
				slog.Debug("Anfrage", "methode", r.Method, "pfad", r.URL.Path, "status", sw.status, "dauer", d.Round(time.Millisecond))
			}
		}()
		next.ServeHTTP(sw, r)
	})
}

// Watch checks every few seconds that the database and the server's shared
// state still respond. If not, it logs where every goroutine is waiting – the
// information needed when Heimdeck hangs.
func (s *Server) Watch(ctx context.Context) {
	tick := time.NewTicker(watchInterval)
	defer tick.Stop()
	var lastDump time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		problem := ""
		start := time.Now()
		pctx, cancel := context.WithTimeout(ctx, watchTimeout)
		err := s.store.Ping(pctx)
		cancel()
		if ctx.Err() != nil {
			return
		}
		if d := time.Since(start); err != nil {
			problem = fmt.Sprintf("Datenbank antwortet nicht (%v nach %v)", err, d.Round(time.Millisecond))
		} else if d > time.Second {
			slog.Warn("Datenbank antwortet langsam", "dauer", d.Round(time.Millisecond))
		}
		if problem == "" && !lockWithin(s, watchTimeout) {
			problem = fmt.Sprintf("interner Zustand seit über %v blockiert", watchTimeout)
		}
		if problem == "" {
			continue
		}
		if time.Since(lastDump) < dumpEvery {
			slog.Error("Heimdeck hängt", "problem", problem)
			continue
		}
		lastDump = time.Now()
		slog.Error("Heimdeck hängt – Stand aller Goroutinen folgt", "problem", problem)
		// Written as is: the log handler would put the multi-line dump on one escaped line.
		fmt.Fprintln(os.Stderr, logging.Goroutines())
	}
}

// lockWithin reports whether the server mutex could be taken in time.
func lockWithin(s *Server, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if s.mu.TryLock() {
			s.mu.Unlock()
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// handleGoroutines shows where all goroutines are, only in debug mode.
func (s *Server) handleGoroutines(w http.ResponseWriter, r *http.Request) {
	if !logging.Debug() {
		writeErr(w, http.StatusNotFound, errors.New("nur im Debug-Modus verfügbar"))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprint(w, logging.Goroutines())
}

// LogStartup writes what Heimdeck runs with, so a log always starts with the context.
func (s *Server) LogStartup(version, dataDir, addr string) {
	boards, groups, tiles := s.store.Counts()
	slog.Info("Heimdeck startet", "version", version, "adresse", addr, "daten", dataDir,
		"dashboards", boards, "gruppen", groups, "kacheln", tiles, "docker", s.docker != nil, "debug", logging.Debug())
}
