package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/integrations"
)

// handleHook receives a message for a webhook tile: /hook/<key>, without a
// login – the key in the address is the password. GET with ?title=…&message=…
// works too, for services that can only call an address (e.g. Shelly actions).
func (s *Server) handleHook(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	tiles, err := s.store.Tiles()
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	var tileID int64
	for _, t := range tiles {
		if t.Integration == "webhook" && integrations.HookKeyMatches(t.Config["hookKey"], key) {
			tileID = t.ID
			break
		}
	}
	if tileID == 0 {
		time.Sleep(200 * time.Millisecond) // slow down guessing
		writeErr(w, 404, errors.New("nicht gefunden"))
		return
	}
	m, err := hookMessage(r)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	integrations.ReceiveHook(key, m)
	s.forget(tileID) // the tile shows the message with its next refresh
	slog.Debug("Webhook empfangen", "kachel", tileID, "titel", m.Title)
	writeJSON(w, map[string]bool{"ok": true})
}

const hookFieldMax = 300

// hookMessage reads a message from JSON, a form, plain text or the query.
// Common field names are understood, including Uptime Kuma's format.
func hookMessage(r *http.Request) (integrations.HookMessage, error) {
	m := integrations.HookMessage{At: time.Now()}
	vals := map[string]any{}
	for k, v := range r.URL.Query() {
		vals[strings.ToLower(k)] = v[0]
	}
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		body, err := io.ReadAll(io.LimitReader(r.Body, 16<<10))
		if err != nil {
			return m, errors.New("ungültige Anfrage")
		}
		ct, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
		trimmed := strings.TrimSpace(string(body))
		switch {
		case ct == "application/json" || strings.HasPrefix(trimmed, "{"):
			var obj map[string]any
			if err := json.Unmarshal(body, &obj); err != nil {
				return m, errors.New("ungültiges JSON")
			}
			for k, v := range obj {
				vals[strings.ToLower(k)] = v
			}
		case ct == "application/x-www-form-urlencoded":
			form, _ := url.ParseQuery(trimmed)
			for k, v := range form {
				vals[strings.ToLower(k)] = v[0]
			}
		case trimmed != "":
			vals["message"] = trimmed
		}
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := vals[k]; ok && v != nil {
				s := strings.TrimSpace(fmt.Sprint(v))
				if s != "" && !strings.HasPrefix(s, "map[") {
					return s
				}
			}
		}
		return ""
	}
	status := str("status", "level", "state", "severity")
	// Uptime Kuma: {"msg": …, "monitor": {"name": …}, "heartbeat": {"status": 0|1}}
	if mon, ok := vals["monitor"].(map[string]any); ok && vals["title"] == nil {
		vals["title"] = mon["name"]
	}
	if hb, ok := vals["heartbeat"].(map[string]any); ok && status == "" && hb["status"] != nil {
		status = fmt.Sprint(hb["status"])
	}
	m.Title = cut(str("title", "name", "subject", "heading", "event"))
	m.Text = cut(str("message", "text", "msg", "body", "description", "content"))
	m.Value = cut(str("value"))
	m.Tone = integrations.HookTone(status)
	if m.Title == "" && m.Text == "" && m.Value == "" {
		m.Text = "Webhook"
	}
	return m, nil
}

func cut(s string) string {
	if r := []rune(s); len(r) > hookFieldMax {
		return string(r[:hookFieldMax]) + "…"
	}
	return s
}
