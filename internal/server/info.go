package server

import (
	"os"
	"runtime"
)

// systemInfo is shown under Settings → Info and copied into bug reports.
func (s *Server) systemInfo() map[string]any {
	s.mu.Lock()
	checked := s.updateChecked
	s.mu.Unlock()
	info := map[string]any{
		"go":      runtime.Version(),
		"os":      runtime.GOOS + "/" + runtime.GOARCH,
		"install": installKind(),
		"started": s.started,
	}
	if !checked.IsZero() {
		info["updateChecked"] = checked
	}
	if tiles, err := s.store.Tiles(); err == nil {
		info["tiles"] = len(tiles)
	}
	return info
}

// installKind guesses how Heimdeck runs: in a container, as the service the
// install script sets up, or started by hand.
func installKind() string {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return "docker"
	}
	if os.Getenv("INVOCATION_ID") != "" { // set by systemd for its services
		return "systemd"
	}
	return "binary"
}
