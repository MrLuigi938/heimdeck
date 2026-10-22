package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/i18n"
	"github.com/heimdeck/heimdeck/internal/logging"
	"github.com/heimdeck/heimdeck/internal/store"
)

// Outage notifications: a service counts as down once it failed the given
// number of minute checks in a row (setting notifyAfter, default 2). Then
// Discord, ntfy and/or Telegram get a message, and another one when it is back.
// Browsers with the dashboard open show the same alerts (TileStatus.Alert).

// notifySecrets are kept encrypted, like the API keys of the tiles.
type notifySecrets struct {
	DiscordWebhook string `json:"discordWebhook,omitempty"`
	NtfyToken      string `json:"ntfyToken,omitempty"`
	TelegramToken  string `json:"telegramToken,omitempty"`
}

const notifySecretsKey = "_notifySecrets"

func (s *Server) notifySecrets() notifySecrets {
	var ns notifySecrets
	settings, _ := s.store.Settings()
	if plain, err := s.store.OpenSecret(settings[notifySecretsKey]); err == nil && plain != nil {
		json.Unmarshal(plain, &ns)
	}
	return ns
}

// outage tracks one address (internal or public) of a tile.
type outage struct {
	fails    int
	since    time.Time // first failed check
	notified bool
}

func outageKey(id int64, kind string) string { return strconv.FormatInt(id, 10) + ":" + kind }

// trackOutages updates the failure counters after a full check round and
// sends notifications for new outages and recoveries.
func (s *Server) trackOutages(tiles []store.Tile, latest map[int64]TileStatus, now time.Time) {
	settings, _ := s.store.Settings()
	after, err := strconv.Atoi(settings["notifyAfter"])
	if err != nil || after < 1 {
		after = 2
	}
	recovery := settings["notifyRecovery"] != "false"
	external := settings["notifyExternal"] != "false"
	lang := i18n.Normalize(settings["language"])
	names := map[int64]store.Tile{}
	for _, t := range tiles {
		names[t.ID] = t
	}

	var msgs []notification
	s.mu.Lock()
	seen := map[string]bool{}
	for id, st := range latest {
		checks := map[string]bool{"int": st.Online}
		if st.Ext != nil && external {
			checks["ext"] = st.Ext.Online
		}
		for kind, online := range checks {
			key := outageKey(id, kind)
			seen[key] = true
			o := s.outages[key]
			if o == nil {
				o = &outage{}
				s.outages[key] = o
			}
			t := names[id]
			if online {
				if o.notified && recovery {
					msgs = append(msgs, recoveredMsg(lang, t, kind, now.Sub(o.since)))
				}
				*o = outage{}
				continue
			}
			if o.fails == 0 {
				o.since = now
			}
			o.fails++
			if o.fails >= after && !o.notified {
				o.notified = true
				msgs = append(msgs, downMsg(lang, t, kind, o.since))
			}
		}
	}
	// Tiles that were deleted or are no longer monitored.
	for key := range s.outages {
		if !seen[key] {
			delete(s.outages, key)
		}
	}
	// Mark confirmed outages for the browsers.
	for id, st := range latest {
		var kinds []string
		for _, kind := range []string{"int", "ext"} {
			if o := s.outages[outageKey(id, kind)]; o != nil && o.notified {
				kinds = append(kinds, kind)
			}
		}
		st.Alert = strings.Join(kinds, ",")
		latest[id] = st
	}
	s.mu.Unlock()

	if len(msgs) > 0 {
		go s.send(settings, msgs)
	}
}

type notification struct {
	Title, Body, URL string
	Down             bool
}

func where(lang, kind string) string {
	if kind == "ext" {
		return " (" + i18n.T(lang, "öffentliche Adresse") + ")"
	}
	return ""
}

func downMsg(lang string, t store.Tile, kind string, since time.Time) notification {
	u := t.URL
	if kind == "ext" {
		u = t.ExternalURL
	}
	return notification{Down: true, URL: u,
		Title: i18n.T(lang, "%s ist nicht erreichbar", t.Name) + where(lang, kind),
		Body:  i18n.T(lang, "Seit %s keine Antwort von %s", since.Format("15:04"), u)}
}

func recoveredMsg(lang string, t store.Tile, kind string, d time.Duration) notification {
	u := t.URL
	if kind == "ext" {
		u = t.ExternalURL
	}
	mins := int(d.Round(time.Minute) / time.Minute)
	if mins < 1 {
		mins = 1
	}
	return notification{URL: u,
		Title: i18n.T(lang, "%s ist wieder erreichbar", t.Name) + where(lang, kind),
		Body:  i18n.T(lang, "Ausfall nach %d Min. behoben", mins)}
}

// send delivers the messages to all configured channels.
func (s *Server) send(settings map[string]string, msgs []notification) {
	defer logging.Recover("Benachrichtigung")
	sec := s.notifySecrets()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	for _, m := range msgs {
		if sec.DiscordWebhook != "" {
			logErr("Discord-Benachrichtigung", sendDiscord(ctx, sec.DiscordWebhook, m))
		}
		if u := strings.TrimSpace(settings["ntfyUrl"]); u != "" {
			logErr("ntfy-Benachrichtigung", sendNtfy(ctx, u, sec.NtfyToken, m))
		}
		if chat := strings.TrimSpace(settings["telegramChat"]); sec.TelegramToken != "" && chat != "" {
			logErr("Telegram-Benachrichtigung", sendTelegram(ctx, sec.TelegramToken, chat, m))
		}
	}
}

// telegramAPI is the Bot API; a variable so tests can point it elsewhere.
var telegramAPI = "https://api.telegram.org"

// sendTelegram posts the message through a bot (core.telegram.org/bots/api#sendmessage).
func sendTelegram(ctx context.Context, token, chat string, m notification) error {
	icon := "✅"
	if m.Down {
		icon = "🔴"
	}
	text := icon + " <b>" + html.EscapeString(m.Title) + "</b>\n" + html.EscapeString(m.Body)
	if strings.HasPrefix(m.URL, "http") {
		text += "\n" + html.EscapeString(m.URL)
	}
	body, _ := json.Marshal(map[string]any{"chat_id": chat, "text": text, "parse_mode": "HTML", "link_preview_options": map[string]bool{"is_disabled": true}})
	err := post(ctx, telegramAPI+"/bot"+token+"/sendMessage", body, map[string]string{"Content-Type": "application/json"})
	if err != nil && strings.Contains(err.Error(), "Status 400") {
		return errors.New("Telegram lehnt die Nachricht ab – stimmt die Chat-ID? Dem Bot vorher einmal schreiben bzw. ihn in die Gruppe aufnehmen")
	}
	if err != nil && (strings.Contains(err.Error(), "Status 401") || strings.Contains(err.Error(), "Status 404")) {
		return errors.New("Telegram kennt diesen Bot-Token nicht")
	}
	return err
}

var notifyClient = &http.Client{Timeout: 10 * time.Second}

func post(ctx context.Context, u string, body []byte, headers map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := notifyClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("Status %d", resp.StatusCode)
	}
	return nil
}

func sendDiscord(ctx context.Context, webhook string, m notification) error {
	color := 0x30a46c
	icon := "✅"
	if m.Down {
		color, icon = 0xe5484d, "🔴"
	}
	embed := map[string]any{"title": icon + " " + m.Title, "description": m.Body, "color": color,
		"footer": map[string]string{"text": "Heimdeck"}, "timestamp": time.Now().UTC().Format(time.RFC3339)}
	if strings.HasPrefix(m.URL, "http") {
		embed["url"] = m.URL
	}
	body, _ := json.Marshal(map[string]any{"username": "Heimdeck", "embeds": []any{embed}})
	return post(ctx, webhook, body, map[string]string{"Content-Type": "application/json"})
}

func sendNtfy(ctx context.Context, topicURL, token string, m notification) error {
	h := map[string]string{"Title": encodeHeader(m.Title), "Tags": "white_check_mark", "Priority": "default"}
	if m.Down {
		h["Tags"], h["Priority"] = "red_circle", "high"
	}
	if strings.HasPrefix(m.URL, "http") {
		h["Click"] = m.URL
	}
	if token != "" {
		if strings.Contains(token, ":") { // user:password
			h["Authorization"] = "Basic " + basic(token)
		} else {
			h["Authorization"] = "Bearer " + token
		}
	}
	return post(ctx, topicURL, []byte(m.Body), h)
}

// encodeHeader keeps umlauts in HTTP headers intact (RFC 2047, understood by ntfy).
func encodeHeader(s string) string {
	for _, r := range s {
		if r > 127 {
			return "=?UTF-8?B?" + b64(s) + "?="
		}
	}
	return s
}

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func basic(userPass string) string { return b64(userPass) }

// ---- settings API ----

type notifyInput struct {
	DiscordWebhook *string `json:"discordWebhook"`
	NtfyToken      *string `json:"ntfyToken"`
	TelegramToken  *string `json:"telegramToken"`
}

// telegramTokenRe is the shape of a bot token from @BotFather: 123456789:AA…
var telegramTokenRe = regexp.MustCompile(`^[0-9]{5,}:[A-Za-z0-9_-]{30,}$`)

func validWebhook(u string) error {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" || p.Host == "" {
		return errors.New("Bitte eine gültige https-Adresse angeben")
	}
	return nil
}

// handleSaveNotify stores the secret parts of the notification settings. A
// missing field keeps the stored value, an empty string removes it.
func (s *Server) handleSaveNotify(w http.ResponseWriter, r *http.Request) {
	var in notifyInput
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	ns := s.notifySecrets()
	if in.DiscordWebhook != nil {
		v := strings.TrimSpace(*in.DiscordWebhook)
		if v != "" {
			if err := validWebhook(v); err != nil {
				writeErr(w, 400, err)
				return
			}
		}
		ns.DiscordWebhook = v
	}
	if in.NtfyToken != nil {
		ns.NtfyToken = strings.TrimSpace(*in.NtfyToken)
	}
	if in.TelegramToken != nil {
		v := strings.TrimSpace(*in.TelegramToken)
		if v != "" && !telegramTokenRe.MatchString(v) {
			writeErr(w, 400, errors.New("Das sieht nicht nach einem Bot-Token aus – er hat die Form 123456789:AAH…"))
			return
		}
		ns.TelegramToken = v
	}
	plain, _ := json.Marshal(ns)
	sealed, err := s.store.SealSecret(plain)
	if err == nil {
		err = s.store.SaveSettings(map[string]string{notifySecretsKey: sealed})
	}
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, ns.status())
}

// status says which secrets are set, without revealing them.
func (ns notifySecrets) status() map[string]any {
	return map[string]any{"ok": true, "discord": ns.DiscordWebhook != "", "ntfyToken": ns.NtfyToken != "", "telegram": ns.TelegramToken != ""}
}

// handleTestNotify sends a test message to every configured channel.
func (s *Server) handleTestNotify(w http.ResponseWriter, r *http.Request) {
	settings, _ := s.store.Settings()
	sec := s.notifySecrets()
	lang := langOf(r)
	m := notification{Title: i18n.T(lang, "Testnachricht von Heimdeck"), Body: i18n.T(lang, "Benachrichtigungen kommen an.")}
	ctx, cancel := timeout(r, 15*time.Second)
	defer cancel()
	var errs []string
	sent := 0
	if sec.DiscordWebhook != "" {
		if err := sendDiscord(ctx, sec.DiscordWebhook, m); err != nil {
			errs = append(errs, "Discord: "+err.Error())
		} else {
			sent++
		}
	}
	if u := strings.TrimSpace(settings["ntfyUrl"]); u != "" {
		if err := sendNtfy(ctx, u, sec.NtfyToken, m); err != nil {
			errs = append(errs, "ntfy: "+err.Error())
		} else {
			sent++
		}
	}
	if chat := strings.TrimSpace(settings["telegramChat"]); sec.TelegramToken != "" && chat != "" {
		if err := sendTelegram(ctx, sec.TelegramToken, chat, m); err != nil {
			errs = append(errs, "Telegram: "+tr(r, err.Error()))
		} else {
			sent++
		}
	}
	if len(errs) > 0 {
		// Errors of the HTTP client quote the address, which holds the token or webhook.
		for i := range errs {
			errs[i] = logging.Redact(errs[i])
		}
		writeErr(w, 502, errors.New(strings.Join(errs, " · ")))
		return
	}
	if sent == 0 {
		writeErr(w, 400, errors.New("Noch kein Kanal eingerichtet"))
		return
	}
	writeJSON(w, map[string]any{"ok": true, "sent": sent})
}
