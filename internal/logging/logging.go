// Package logging sets up Heimdeck's log output. Errors and warnings are
// always written; the debug mode (setting or HEIMDECK_DEBUG=true) adds every
// request, live data fetch and status check, to find out what Heimdeck is doing.
package logging

import (
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"runtime/debug"
	"runtime/pprof"
	"strings"
	"sync/atomic"
)

var (
	level  = new(slog.LevelVar)
	forced atomic.Bool // HEIMDECK_DEBUG=true: on regardless of the setting
)

// Setup routes slog and the standard log package to stderr. With
// HEIMDECK_DEBUG set to true the debug mode stays on.
func Setup() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
	if v := strings.ToLower(os.Getenv("HEIMDECK_DEBUG")); v == "true" || v == "1" {
		forced.Store(true)
		level.Set(slog.LevelDebug)
	}
}

// SetDebug switches the debug mode from the setting; HEIMDECK_DEBUG wins.
func SetDebug(on bool) {
	if forced.Load() {
		return
	}
	if on == Debug() {
		return
	}
	if on {
		level.Set(slog.LevelDebug)
		slog.Info("Debug-Modus eingeschaltet")
	} else {
		slog.Info("Debug-Modus ausgeschaltet")
		level.Set(slog.LevelInfo)
	}
}

func Debug() bool  { return level.Level() <= slog.LevelDebug }
func Forced() bool { return forced.Load() }

// secretParam matches credentials in addresses, e.g. SABnzbd's ?apikey=…,
// which Go's error messages quote in full.
var secretParam = regexp.MustCompile(`(?i)([?&;](?:api_?key|apitoken|token|access_token|key|pass|password|passwd|pwd|auth|secret|sid|u|p)=)[^&;\s"']+`)

// userInfo matches user:password@ in URLs.
var userInfo = regexp.MustCompile(`(://)[^/@\s"']+:[^/@\s"']+@`)

// webhookPath matches the token part of Discord webhook addresses.
var webhookPath = regexp.MustCompile(`(/api/webhooks/[0-9]+/)[^/\s"']+`)

// botPath matches Telegram bot tokens in Bot API addresses (/bot123:AA…/).
var botPath = regexp.MustCompile(`(/bot)[0-9]+:[A-Za-z0-9_-]+`)

// Redact hides credentials in text that goes to the log.
func Redact(s string) string {
	s = secretParam.ReplaceAllString(s, "${1}***")
	s = webhookPath.ReplaceAllString(s, "${1}***")
	s = botPath.ReplaceAllString(s, "${1}***")
	return userInfo.ReplaceAllString(s, "${1}***@")
}

// Recover, deferred at the top of a goroutine, logs a panic with its stack
// instead of letting it end the whole program.
func Recover(what string, attrs ...any) {
	if p := recover(); p != nil {
		slog.Error("Absturz abgefangen: "+what, append(attrs, "fehler", Redact(fmt.Sprint(p)))...)
		os.Stderr.Write(debug.Stack())
	}
}

// Goroutines returns the stacks of all goroutines, to see where Heimdeck hangs.
func Goroutines() string {
	var b strings.Builder
	pprof.Lookup("goroutine").WriteTo(&b, 1)
	return b.String()
}
