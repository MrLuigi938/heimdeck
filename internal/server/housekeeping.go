package server

import (
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/logging"
)

// sweep clears out, once an hour, what the server keeps in memory and nobody
// needs any more: unlocked groups of sessions whose time ran out, favicons
// long out of date, the state of deleted workflows, messages of webhook tiles
// that are gone – and in the integrations, results nobody asked for in a day.
// Without it these grow for as long as Heimdeck runs.
func (s *Server) sweep(now time.Time) {
	defer logging.Recover("Aufräumen")
	s.mu.Lock()
	for token, groups := range s.unlocked {
		for id, until := range groups {
			if now.After(until) {
				delete(groups, id)
			}
		}
		if len(groups) == 0 {
			delete(s.unlocked, token)
		}
	}
	for key, f := range s.favicons {
		if now.Sub(f.at) > 2*faviconTTL {
			delete(s.favicons, key)
		}
	}
	s.mu.Unlock()

	unlockFailures.Range(func(k, v any) bool {
		t := v.(*unlockTries)
		t.mu.Lock()
		stale := t.until.IsZero() && t.count < 5 || !t.until.IsZero() && now.After(t.until)
		t.mu.Unlock()
		if stale {
			unlockFailures.Delete(k)
		}
		return true
	})

	// Workflows that no longer exist (or whose owner is gone).
	live := map[string]bool{}
	if users, err := s.store.Users(); err == nil {
		for _, u := range users {
			for _, r := range s.rulesOf(u.ID) {
				live[stateKey(u.ID, r.ID)] = true
			}
		}
		s.rules.mu.Lock()
		for k := range s.rules.state {
			// user:workflow, or user:workflow:trigger
			parts := strings.SplitN(k, ":", 3)
			if len(parts) < 2 || !live[parts[0]+":"+parts[1]] {
				delete(s.rules.state, k)
			}
		}
		s.rules.mu.Unlock()
	}

	// Messages for webhook keys no tile uses any more.
	if tiles, err := s.store.Tiles(); err == nil {
		keys := map[string]bool{}
		for _, t := range tiles {
			if t.Integration == "webhook" && t.Config["hookKey"] != "" {
				keys[t.Config["hookKey"]] = true
			}
		}
		integrations.KeepHooks(keys)
	}
	integrations.Sweep(now)
}
