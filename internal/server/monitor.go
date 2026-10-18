package server

import (
	"context"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/store"
)

const (
	checkInterval = time.Minute
	historyKeep   = 7 * 24 * time.Hour
	startDelay    = 15 * time.Second
	retryDelay    = 3 * time.Second
)

// TileStatus is the latest check result of a tile's internal and public address.
type TileStatus struct {
	Online bool   `json:"online"`
	MS     int64  `json:"ms"`
	Ext    *Check `json:"ext,omitempty"`
}

type Check struct {
	Online bool  `json:"online"`
	MS     int64 `json:"ms"`
}

// Monitor checks every tile once a minute and stores the results, so the
// dashboard can show the current status and the uptime history.
func (s *Server) Monitor(ctx context.Context) {
	// Give containers started together with Heimdeck a moment to come up.
	select {
	case <-ctx.Done():
		return
	case <-time.After(startDelay):
	}
	s.runChecks(ctx, nil)
	tick := time.NewTicker(checkInterval)
	defer tick.Stop()
	prune := time.NewTicker(time.Hour)
	defer prune.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			s.runChecks(ctx, nil)
		case id := <-s.recheck:
			s.runChecks(ctx, &id)
		case <-prune.C:
			logErr("Statusverlauf aufräumen", s.store.PruneChecks(time.Now().Add(-historyKeep)))
		}
	}
}

// requestCheck schedules an immediate check of one tile (after it was saved).
func (s *Server) requestCheck(id int64) {
	select {
	case s.recheck <- id:
	default:
	}
}

func (s *Server) runChecks(ctx context.Context, only *int64) {
	tiles, err := s.store.Tiles()
	if err != nil {
		log.Printf("Statusprüfung: %v", err)
		return
	}
	now := time.Now()
	var mu sync.Mutex
	var wg sync.WaitGroup
	var logged []store.Check
	latest := map[int64]TileStatus{}
	for _, t := range tiles {
		if only != nil && t.ID != *only {
			continue
		}
		internal := t.APIURL
		if internal == "" {
			internal = t.URL
		}
		if internal == "" || !t.Monitor {
			continue
		}
		wg.Add(1)
		go func(t store.Tile) {
			defer wg.Done()
			st := TileStatus{}
			in := pingTwice(ctx, internal, t.Insecure)
			st.Online, st.MS = in.Online, in.MS
			checks := []store.Check{{TileID: t.ID, Kind: "int", Online: in.Online, MS: in.MS}}
			if ext := strings.TrimSpace(t.ExternalURL); ext != "" && ext != internal {
				e := pingTwice(ctx, ext, t.Insecure)
				st.Ext = &e
				checks = append(checks, store.Check{TileID: t.ID, Kind: "ext", Online: e.Online, MS: e.MS})
			}
			mu.Lock()
			latest[t.ID] = st
			logged = append(logged, checks...)
			mu.Unlock()
		}(t)
	}
	wg.Wait()
	if ctx.Err() != nil {
		return
	}
	s.mu.Lock()
	if only == nil {
		s.latest = latest
	} else {
		for id, st := range latest {
			s.latest[id] = st
		}
	}
	s.mu.Unlock()
	// Immediate re-checks after editing are shown but not logged, to keep the history evenly spaced.
	if only == nil {
		logErr("Statusverlauf speichern", s.store.LogChecks(now, logged))
	}
}

// pingTwice retries a failed check once, so a single dropped request does
// not show up as an outage.
func pingTwice(ctx context.Context, target string, insecure bool) Check {
	c := ping(ctx, target, insecure)
	if c.Online {
		return c
	}
	select {
	case <-ctx.Done():
		return c
	case <-time.After(retryDelay):
	}
	return ping(ctx, target, insecure)
}

// ping treats any HTTP response below 500 as "online"; network errors and
// server errors count as down.
func ping(ctx context.Context, target string, insecure bool) Check {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return Check{}
	}
	client := integrations.NewClient(insecure)
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		return Check{}
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return Check{}
	}
	return Check{Online: true, MS: time.Since(start).Milliseconds()}
}
