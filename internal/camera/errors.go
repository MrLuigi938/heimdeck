package camera

import (
	"errors"
	"strings"
)

// What a camera error may say to the browser: network and library errors name
// the camera's address, and viewers of a shared tile must not get it. Only
// texts marked as shown go out; anything else becomes a general hint and stays
// in the debug log.

// ShownError is a text written for the user, free of addresses.
type ShownError struct{ Text string }

func (e *ShownError) Error() string { return e.Text }

func shown(text string) error { return &ShownError{text} }

// Shown marks a text as safe to show.
func Shown(text string) error { return shown(text) }

var (
	errUnreachable = shown("Die Kamera ist nicht erreichbar – Adresse prüfen")
	errGeneral     = shown("Die Kamera meldet einen Fehler – Details zeigt der Debug-Modus")
)

// isNetworkError recognises a failed connection by its text, because the RTSP
// library does not keep the original error.
func isNetworkError(s string) bool {
	for _, part := range []string{"dial tcp", "dial udp", "connectex", "connection refused", "connection reset", "broken pipe",
		"i/o timeout", "no route", "no such host", "lookup ", "deadline", "network is unreachable", "EOF"} {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

// Public turns any camera error into one that may go to the browser.
func Public(err error) error {
	if err == nil {
		return nil
	}
	var s *ShownError
	if errors.As(err, &s) {
		return s
	}
	if isNetworkError(err.Error()) {
		return errUnreachable
	}
	return errGeneral
}
