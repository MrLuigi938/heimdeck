package server

import (
	"context"
	"errors"
	"html"
	"log/slog"
	"regexp"
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/camera"
	"github.com/heimdeck/heimdeck/internal/i18n"
	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/logging"
	"github.com/heimdeck/heimdeck/internal/store"
)

// Reports the AI writes from the dashboard: the situation report (on request
// and every morning in Telegram) and a likely cause added to outage
// notifications. Both see what the person sees on their dashboard – the same
// tiles and rules as 📋 in the chat.

const reportPrompt = "Write a short situation report about the user's home from their dashboard above. " +
	"First what needs attention: services marked OFFLINE, warnings, nearly full disks, failed downloads or jobs – or one line saying all is well. " +
	"Then today: the weather and the next appointments, if those tiles exist. Then in a line or two what is going on (downloads, streams, requests). " +
	"At most about 12 short lines, an emoji at the start of each part, no tables or headings, nothing that is not on the dashboard."

func aiLanguage(lang string) string { return map[string]string{"de": "German", "en": "English"}[lang] }

// statusReport asks the AI for the situation report.
func (s *Server) statusReport(ctx context.Context, user int64, lang string) (string, error) {
	u, err := s.store.User(user)
	if err != nil || u.Disabled || s.aiFor(u) == nil {
		return "", errors.New(i18n.T(lang, "Die KI ist für dein Konto nicht eingerichtet"))
	}
	c := s.aiConfig()
	if !c.Dashboard {
		return "", errors.New(i18n.T(lang, "Für den Lagebericht muss ein Admin der KI die Daten des Dashboards erlauben (Einstellungen → KI)."))
	}
	dash, n := s.dashboardContext(s.botRequest(ctx, user, lang))
	if n == 0 {
		return i18n.T(lang, "Auf deinem Dashboard sind keine Kacheln, über die ich berichten könnte."), nil
	}
	system := "You are the assistant inside Heimdeck, a self-hosted home dashboard. Answer in " + aiLanguage(lang) + ". " +
		"Current date and time: " + time.Now().Format("Monday, 2 January 2006, 15:04") + "." + memoryPrompt(s.memoryOf(user)) + dashboardPrompt(dash)
	msgs := []any{map[string]string{"role": "system", "content": system}, map[string]string{"role": "user", "content": reportPrompt}}
	text, _, err := aiComplete(ctx, c, msgs, false, false, nil, lang, false)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return i18n.T(lang, "Die KI hat keine Antwort gegeben."), nil
	}
	slog.Debug("Lagebericht", "benutzer", u.Username, "kacheln", n, "zeichen", len(text))
	return strings.TrimSpace(text), nil
}

var reportTimeRe = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)

// morningReports sends the daily report to the users whose time has come
// (setting telegramReport, e.g. "07:00", in the server's time zone). It
// catches up for 15 minutes, not after a long downtime.
func (s *Server) morningReports(ctx context.Context, now time.Time) {
	users, _ := s.store.Users()
	day := now.Format("2006-01-02")
	minute := now.Hour()*60 + now.Minute()
	for _, u := range users {
		if u.Disabled {
			continue
		}
		settings := s.settingsFor(u.ID)
		at := strings.TrimSpace(settings["telegramReport"])
		if !reportTimeRe.MatchString(at) || settings["_reportSent"] == day {
			continue
		}
		h, m := int(at[0]-'0')*10+int(at[1]-'0'), int(at[3]-'0')*10+int(at[4]-'0')
		if since := minute - (h*60 + m); since < 0 || since > 15 {
			continue
		}
		chat := strings.TrimSpace(settings["telegramChat"])
		token := s.notifySecrets(u.ID).TelegramToken
		if chat == "" || token == "" || s.aiFor(u) == nil {
			continue
		}
		// Marked before sending: a slow model must not get a second report going.
		if err := s.store.SaveUserSettings(u.ID, map[string]string{"_reportSent": day}); err != nil {
			continue
		}
		go s.sendMorningReport(ctx, u.ID, token, chat, i18n.Normalize(settings["language"]))
	}
}

func (s *Server) sendMorningReport(ctx context.Context, user int64, token, chat, lang string) {
	defer logging.Recover("Morgenbericht")
	rctx, cancel := context.WithTimeout(ctx, tgAnswerTime)
	text, err := s.statusReport(rctx, user, lang)
	cancel()
	if err != nil {
		text = "⚠ " + err.Error()
	}
	head := i18n.T(lang, "Guten Morgen!")
	body := map[string]any{"chat_id": chat, "link_preview_options": map[string]bool{"is_disabled": true}}
	if err := tgSendHTML(ctx, token, body, "☀️ <b>"+html.EscapeString(head)+"</b>\n\n"+tgHTML(text), "☀️ "+head+"\n\n"+text); err != nil {
		slog.Warn("Morgenbericht nicht gesendet", "fehler", logging.Redact(err.Error()))
		return
	}
	slog.Info("Morgenbericht gesendet", "benutzer", s.usernameOf(user))
}

// ---- outage notifications ----

// bundleNotifications puts several outages (or recoveries) of one check round
// into one message.
func bundleNotifications(lang string, msgs []notification) []notification {
	var down, up, out []notification
	for _, m := range msgs {
		switch {
		case m.Camera || m.Rule:
			out = append(out, m)
		case m.Down:
			down = append(down, m)
		default:
			up = append(up, m)
		}
	}
	merge := func(list []notification, title string, isDown bool) []notification {
		if len(list) < 2 {
			return list
		}
		lines := make([]string, len(list))
		for i, m := range list {
			lines[i] = "• " + m.Title
		}
		return []notification{{Down: isDown, Title: i18n.T(lang, title, len(list)), Body: strings.Join(lines, "\n")}}
	}
	return append(append(merge(down, "%d Dienste sind nicht erreichbar", true), merge(up, "%d Dienste sind wieder erreichbar", false)...), out...)
}

// explainOutages asks the AI whether the dashboard shows a cause for the
// outages – another service on the same host down, a stopped VM, the internet
// connection … It returns "" when it finds nothing or is not allowed to look.
func (s *Server) explainOutages(user int64, lang string, msgs []notification) string {
	var lines []string
	for _, m := range msgs {
		if m.Down && !m.Camera {
			lines = append(lines, "- "+m.Title+" ("+m.Body+")")
		}
	}
	u, err := s.store.User(user)
	c := s.aiConfig()
	if len(lines) == 0 || err != nil || u.Disabled || s.aiFor(u) == nil || !c.Dashboard {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dash, n := s.dashboardContext(s.botRequest(ctx, user, lang))
	if n == 0 {
		return ""
	}
	system := "You are the assistant inside Heimdeck, a self-hosted home dashboard. Answer in " + aiLanguage(lang) + "." + memoryPrompt(s.memoryOf(user)) + dashboardPrompt(dash)
	question := "These services just stopped answering Heimdeck's checks:\n" + strings.Join(lines, "\n") +
		"\n\nDoes the dashboard show a likely cause or something related – other services on the same host also down, a stopped VM or container, " +
		"the internet connection, a full disk, an update running? Answer in at most two short sentences and only with what the dashboard shows. " +
		"If it shows nothing related, answer exactly: -"
	text, _, err := aiComplete(ctx, c, []any{map[string]string{"role": "system", "content": system}, map[string]string{"role": "user", "content": question}}, false, false, nil, lang, false)
	text = strings.TrimSpace(text)
	if err != nil || len([]rune(text)) < 4 {
		return ""
	}
	return clip(text, 400)
}

// ---- camera pictures for Telegram ----

// cameraTiles are the cameras the user sees (not in locked or hidden groups),
// optionally only those matching a name.
func (s *Server) cameraTiles(ctx context.Context, user int64, lang, name string) []store.Tile {
	tiles, _ := s.aiTiles(s.botRequest(ctx, user, lang))
	var cams, exact []store.Tile
	q := strings.ToLower(strings.TrimSpace(name))
	for _, t := range tiles {
		if t.Integration != "camera" {
			continue
		}
		n := strings.ToLower(t.Name)
		switch {
		case q == "" || strings.Contains(n, q):
			cams = append(cams, t)
		}
		if q != "" && n == q {
			exact = append(exact, t)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	return cams
}

type cameraPic struct {
	data     []byte
	ct, name string
	taken    time.Time
}

// cameraPicture takes a current picture, like ⟳ on the tile: the camera's own
// snapshot, or else a key frame from the stream (as a tiny MP4).
func (s *Server) cameraPicture(ctx context.Context, user int64, lang string, id int64) (cameraPic, error) {
	var tile *store.Tile
	for _, t := range s.cameraTiles(ctx, user, lang, "") {
		if t.ID == id {
			tile = &t
			break
		}
	}
	if tile == nil {
		return cameraPic{}, errors.New(i18n.T(lang, "Diese Kamera gibt es nicht mehr"))
	}
	in := s.instance(*tile, lang)
	cctx, cancel := context.WithTimeout(ctx, cameraTimeout)
	defer cancel()
	taken := time.Now()
	data, ct, err := integrations.CameraSnapshot(cctx, in)
	if errors.Is(err, integrations.ErrNoSnapshot) {
		var src camera.Source
		if src, err = integrations.CameraSource(cctx, in); err == nil {
			data, _, taken, err = camera.Still(cctx, src, 10*time.Second)
			ct = "video/mp4"
		}
	}
	if err != nil {
		slog.Debug("Kamerabild für Telegram", "kachel", tile.Name, "fehler", camera.RedactURL(err.Error()))
		return cameraPic{}, integrations.CameraError(in, err)
	}
	return cameraPic{data: data, ct: ct, name: tile.Name, taken: taken}, nil
}
