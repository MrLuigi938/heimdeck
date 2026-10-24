package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/logging"
)

// Update check: twice a day the server asks GitHub for the newest release, so
// the footer can say when a new version is out. A stable version only looks
// at stable releases; a beta also at newer betas. Setting updateCheck=false
// turns it off; development builds ("dev") never check.

const updateEvery = 12 * time.Hour

// releasesURL is GitHub's release list; a variable so tests can point it elsewhere.
var releasesURL = "https://api.github.com/repos/MrLuigi938/heimdeck/releases?per_page=20"

type releaseInfo struct {
	Version string `json:"version"`
	URL     string `json:"url"`
}

// SetVersion tells the server which version it is (set at build time).
func (s *Server) SetVersion(v string) { s.version = v }

// newerRelease is the release the footer offers, nil when up to date or turned off.
func (s *Server) newerRelease() *releaseInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.update
}

// CheckUpdates runs until ctx ends.
func (s *Server) CheckUpdates(ctx context.Context) {
	// Only released versions are compared; dev and edge builds have none.
	if !parseVersion(s.version).ok {
		return
	}
	for {
		s.checkUpdate(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(updateEvery):
		}
	}
}

func (s *Server) checkUpdate(ctx context.Context) {
	defer logging.Recover("Update-Prüfung")
	if settings, _ := s.store.Settings(); settings["updateCheck"] == "false" {
		s.mu.Lock()
		s.update = nil
		s.mu.Unlock()
		return
	}
	newest, err := newestRelease(ctx, s.version)
	if err != nil {
		slog.Debug("Update-Prüfung fehlgeschlagen", "fehler", err)
		return
	}
	s.mu.Lock()
	s.update = newest
	s.mu.Unlock()
	if newest != nil {
		slog.Info("Neue Heimdeck-Version verfügbar", "installiert", s.version, "neu", newest.Version)
	}
}

// newestRelease returns the newest release that is newer than current, or nil.
func newestRelease(ctx context.Context, current string) (*releaseInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, releasesURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, &httpStatusError{resp.StatusCode}
	}
	var list []struct {
		Tag        string `json:"tag_name"`
		URL        string `json:"html_url"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&list); err != nil {
		return nil, err
	}
	cur := parseVersion(current)
	var best *releaseInfo
	var bestV version
	for _, r := range list {
		v := parseVersion(r.Tag)
		if r.Draft || !v.ok || (r.Prerelease && cur.pre == "") || !v.newer(cur) {
			continue
		}
		if best == nil || v.newer(bestV) {
			best, bestV = &releaseInfo{Version: strings.TrimPrefix(r.Tag, "v"), URL: r.URL}, v
		}
	}
	return best, nil
}

type httpStatusError struct{ code int }

func (e *httpStatusError) Error() string { return "Status " + strconv.Itoa(e.code) }

// version is a parsed semantic version like 1.2.0 or 1.2.0-beta.1.
type version struct {
	nums [3]int
	pre  string
	ok   bool
}

func parseVersion(s string) version {
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	core, pre, _ := strings.Cut(s, "-")
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return version{}
	}
	var v version
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return version{}
		}
		v.nums[i] = n
	}
	v.pre, v.ok = pre, true
	return v
}

// newer reports whether v comes after o: higher numbers win, a release beats
// its own pre-releases, and pre-releases compare part by part (beta.2 > beta.1).
func (v version) newer(o version) bool {
	for i := range v.nums {
		if v.nums[i] != o.nums[i] {
			return v.nums[i] > o.nums[i]
		}
	}
	switch {
	case v.pre == o.pre:
		return false
	case v.pre == "":
		return true
	case o.pre == "":
		return false
	}
	a, b := strings.Split(v.pre, "."), strings.Split(o.pre, ".")
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] == b[i] {
			continue
		}
		x, errX := strconv.Atoi(a[i])
		y, errY := strconv.Atoi(b[i])
		if errX == nil && errY == nil {
			return x > y
		}
		return a[i] > b[i]
	}
	return len(a) > len(b)
}
