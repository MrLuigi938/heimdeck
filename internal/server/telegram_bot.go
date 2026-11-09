package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/i18n"
	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/logging"
)

// The AI in Telegram: the bot a user set up for notifications also answers
// questions – in the chat the notifications go to (a private chat with the
// bot, or a group). It is the same AI as the chat window with the same
// rights: the user's tiles without locked or hidden groups, and actions only
// as buttons that a person taps. Heimdeck asks Telegram for new messages
// itself (long polling), so nothing has to be reachable from the internet.
// Messages from any other chat are ignored.

const (
	tgPollWait   = 50 // seconds Telegram keeps a getUpdates request open
	tgHistory    = 16 // messages kept per chat
	tgIdle       = 2 * time.Hour
	tgPendingTTL = 30 * time.Minute
	tgMaxMessage = 3800 // Telegram allows 4096 characters per message
	tgReconcile  = 20 * time.Second
	tgAnswerTime = 4 * time.Minute
)

var tgClient = &http.Client{Timeout: (tgPollWait + 20) * time.Second}

// tgError is an error answer of the Bot API.
type tgError struct {
	Status      int
	Description string
	RetryAfter  int
}

func (e *tgError) Error() string {
	return fmt.Sprintf("Telegram: Status %d %s", e.Status, e.Description)
}

// tgCall calls a Bot API method and decodes its result into out.
func tgCall(ctx context.Context, token, method string, body, out any) error {
	b, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, telegramAPI+"/bot"+token+"/"+method, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	return tgDo(req, out)
}

// tgUpload sends a file (a camera picture) with multipart/form-data.
func tgUpload(ctx context.Context, token, method string, fields map[string]string, field, filename, ctype string, data []byte) error {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	for k, v := range fields {
		w.WriteField(k, v)
	}
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, field, filename))
	h.Set("Content-Type", ctype)
	part, err := w.CreatePart(h)
	if err != nil {
		return err
	}
	part.Write(data)
	w.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, telegramAPI+"/bot"+token+"/"+method, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	return tgDo(req, nil)
}

func tgDo(req *http.Request, out any) error {
	resp, err := tgClient.Do(req)
	if err != nil {
		return errors.New(logging.Redact(err.Error())) // the address holds the token
	}
	defer resp.Body.Close()
	var res struct {
		OK          bool            `json:"ok"`
		Result      json.RawMessage `json:"result"`
		Description string          `json:"description"`
		Parameters  struct {
			RetryAfter int `json:"retry_after"`
		} `json:"parameters"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&res); err != nil {
		return &tgError{Status: resp.StatusCode}
	}
	if !res.OK {
		return &tgError{Status: resp.StatusCode, Description: res.Description, RetryAfter: res.Parameters.RetryAfter}
	}
	if out != nil {
		return json.Unmarshal(res.Result, out)
	}
	return nil
}

type tgUser struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type tgChatInfo struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name"`
	Username  string `json:"username"`
}

type tgMessage struct {
	MessageID int64      `json:"message_id"`
	From      *tgUser    `json:"from"`
	Chat      tgChatInfo `json:"chat"`
	Text      string     `json:"text"`
	Caption   string     `json:"caption"`
	ReplyTo   *tgMessage `json:"reply_to_message"`
	Photo     []struct {
		FileID   string `json:"file_id"`
		Width    int    `json:"width"`
		FileSize int    `json:"file_size"`
	} `json:"photo"`
	Document *struct {
		FileID   string `json:"file_id"`
		FileName string `json:"file_name"`
		MimeType string `json:"mime_type"`
		FileSize int    `json:"file_size"`
	} `json:"document"`
}

type tgCallback struct {
	ID      string     `json:"id"`
	From    tgUser     `json:"from"`
	Message *tgMessage `json:"message"`
	Data    string     `json:"data"`
}

type tgUpdate struct {
	UpdateID     int64       `json:"update_id"`
	Message      *tgMessage  `json:"message"`
	ChannelPost  *tgMessage  `json:"channel_post"`
	Callback     *tgCallback `json:"callback_query"`
	MyChatMember *struct {
		Chat tgChatInfo `json:"chat"`
	} `json:"my_chat_member"`
}

// tgBots are the running bots, one per bot token.
type tgBots struct {
	mu   sync.Mutex
	bots map[string]*tgBot
	kick chan struct{}
}

// TelegramBots runs the AI bots of the users who switched it on, and starts or
// stops them when settings change. It also sends the morning reports.
func (s *Server) TelegramBots(ctx context.Context) {
	defer logging.Recover("Telegram-Bot")
	tick := time.NewTicker(tgReconcile)
	defer tick.Stop()
	for {
		s.reconcileBots(ctx)
		s.morningReports(ctx, time.Now())
		select {
		case <-ctx.Done():
			s.tg.mu.Lock()
			for token, b := range s.tg.bots {
				b.cancel()
				delete(s.tg.bots, token)
			}
			s.tg.mu.Unlock()
			return
		case <-tick.C:
		case <-s.tg.kick:
		}
	}
}

// kickBots looks at the bot settings again right away.
func (s *Server) kickBots() {
	select {
	case s.tg.kick <- struct{}{}:
	default:
	}
}

// tgChatUser is whom a chat belongs to, and whether the AI answers there (or
// the bot only listens for taps on buttons of automations).
type tgChatUser struct {
	user int64
	ai   bool
}

// reconcileBots runs a bot for every token that a user with the AI switched on
// uses – or with automations that ask in Telegram – and lets it answer exactly
// their chats.
func (s *Server) reconcileBots(ctx context.Context) {
	want := map[string]map[int64]tgChatUser{} // token → chat → user
	users, _ := s.store.Users()
	for _, u := range users {
		if u.Disabled {
			continue
		}
		settings := s.settingsFor(u.ID)
		ai := settings["telegramAI"] == "true" && s.aiFor(u) != nil
		if !ai && !s.rulesAsk(u.ID) {
			continue
		}
		chat, err := strconv.ParseInt(strings.TrimSpace(settings["telegramChat"]), 10, 64)
		token := s.notifySecrets(u.ID).TelegramToken
		if err != nil || !telegramTokenRe.MatchString(token) {
			continue
		}
		if want[token] == nil {
			want[token] = map[int64]tgChatUser{}
		}
		if _, taken := want[token][chat]; !taken {
			want[token][chat] = tgChatUser{user: u.ID, ai: ai}
		}
	}
	s.tg.mu.Lock()
	defer s.tg.mu.Unlock()
	for token, b := range s.tg.bots {
		if want[token] == nil {
			b.cancel()
			delete(s.tg.bots, token)
		}
	}
	for token, chats := range want {
		b := s.tg.bots[token]
		if b == nil {
			b = s.newBot(ctx, token)
			s.tg.bots[token] = b
		}
		b.mu.Lock()
		b.chats = chats
		b.mu.Unlock()
	}
}

// botChats returns the chats a running bot has seen, for "find chat ID" –
// while the bot polls, Telegram does not hand the messages out a second time.
func (s *Server) botChats(token string) ([]telegramChat, bool) {
	s.tg.mu.Lock()
	b := s.tg.bots[token]
	s.tg.mu.Unlock()
	if b == nil {
		return nil, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]telegramChat{}, b.seen...), true
}

type tgBot struct {
	s      *Server
	token  string
	cancel context.CancelFunc

	mu      sync.Mutex
	name    string // the bot's username, without @
	chats   map[int64]tgChatUser
	convs   map[int64]*tgConv
	pending map[string]*tgPending
	seen    []telegramChat
	seq     int
	lastErr string
}

type tgConv struct {
	msgs []chatMessage
	at   time.Time
	busy bool
}

// tgPending is an action offered as a button, until it is tapped.
type tgPending struct {
	p     proposal
	chat  int64
	user  int64
	at    time.Time
	asked bool // the confirmation question was shown
}

func (s *Server) newBot(ctx context.Context, token string) *tgBot {
	bctx, cancel := context.WithCancel(ctx)
	b := &tgBot{s: s, token: token, cancel: cancel, chats: map[int64]tgChatUser{}, convs: map[int64]*tgConv{}, pending: map[string]*tgPending{}}
	go b.run(bctx)
	return b
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// problem logs a polling error once (not every minute) and says how long to wait.
func (b *tgBot) problem(err error, backoff *time.Duration) time.Duration {
	wait := *backoff
	msg := err.Error()
	var te *tgError
	if errors.As(err, &te) {
		switch {
		case te.Status == 401 || te.Status == 404:
			msg, wait = "Telegram kennt diesen Bot-Token nicht", 10*time.Minute
		case te.Status == 409:
			msg, wait = "Für diesen Bot ist ein Webhook eingerichtet oder ein anderes Programm fragt ihn schon ab – so kann die KI dort nicht antworten", time.Minute
		case te.RetryAfter > 0:
			wait = time.Duration(te.RetryAfter) * time.Second
		}
	}
	*backoff = min(*backoff*2, time.Minute)
	b.mu.Lock()
	if b.lastErr != msg {
		slog.Warn("Telegram-Bot", "fehler", logging.Redact(msg))
		b.lastErr = msg
	}
	b.mu.Unlock()
	return wait
}

func (b *tgBot) run(ctx context.Context) {
	defer logging.Recover("Telegram-Bot")
	backoff := 5 * time.Second
	var me tgUser
	for {
		err := tgCall(ctx, b.token, "getMe", map[string]any{}, &me)
		if err == nil {
			break
		}
		if ctx.Err() != nil {
			return
		}
		sleepCtx(ctx, b.problem(err, &backoff))
	}
	b.mu.Lock()
	b.name = me.Username
	b.mu.Unlock()
	b.setCommands(ctx)
	slog.Info("Telegram-Bot beantwortet Fragen mit der KI", "bot", "@"+me.Username)

	// What was written while Heimdeck was not running is skipped.
	var offset int64
	var old []tgUpdate
	if tgCall(ctx, b.token, "getUpdates", map[string]any{"offset": -1, "timeout": 0}, &old) == nil && len(old) > 0 {
		offset = old[len(old)-1].UpdateID + 1
		b.note(old[len(old)-1])
	}
	// channel_post and edited_message stay on the list Telegram remembers,
	// so "find chat ID" still sees channels after the bot is switched off.
	allowed := []string{"message", "edited_message", "channel_post", "callback_query", "my_chat_member"}
	for ctx.Err() == nil {
		var ups []tgUpdate
		err := tgCall(ctx, b.token, "getUpdates", map[string]any{"offset": offset, "timeout": tgPollWait, "allowed_updates": allowed}, &ups)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			sleepCtx(ctx, b.problem(err, &backoff))
			continue
		}
		backoff = 5 * time.Second
		b.mu.Lock()
		b.lastErr = ""
		b.mu.Unlock()
		for _, u := range ups {
			offset = u.UpdateID + 1
			b.note(u)
			switch {
			case u.Callback != nil:
				go b.onButton(ctx, *u.Callback)
			case u.Message != nil:
				b.onMessage(ctx, *u.Message)
			}
		}
	}
}

// setCommands fills the command menu in Telegram, in the language of the
// (first) user the bot answers.
func (b *tgBot) setCommands(ctx context.Context) {
	lang, ai, seen := i18n.DE, false, false
	b.mu.Lock()
	for _, c := range b.chats {
		if !seen || c.ai && !ai {
			lang, seen = b.s.userLang(c.user), true
		}
		ai = ai || c.ai
	}
	b.mu.Unlock()
	// Command names in the user's language; both languages' work anyway.
	names := []string{"lage", "kamera", "szene", "web", "merken", "vergessen", "neu", "hilfe"}
	if lang == i18n.EN {
		names = []string{"status", "camera", "scene", "web", "remember", "forget", "new", "help"}
	}
	descs := []string{"Lagebericht", "Aktuelles Kamerabild", "Eine Szene starten", "Mit Websuche fragen", "Etwas merken", "Gemerktes ansehen und löschen", "Neues Gespräch", "Was ich kann"}
	if !ai {
		names, descs = names[2:3], descs[2:3] // only scenes and the buttons of automations
	}
	var cmds []map[string]string
	for i, n := range names {
		cmds = append(cmds, map[string]string{"command": n, "description": i18n.T(lang, descs[i])})
	}
	tgCall(ctx, b.token, "setMyCommands", map[string]any{"commands": cmds}, nil)
}

// note remembers the chats that wrote, for "find chat ID".
func (b *tgBot) note(u tgUpdate) {
	var c *tgChatInfo
	switch {
	case u.Message != nil:
		c = &u.Message.Chat
	case u.ChannelPost != nil:
		c = &u.ChannelPost.Chat
	case u.MyChatMember != nil:
		c = &u.MyChatMember.Chat
	}
	if c == nil {
		return
	}
	name := strings.TrimSpace(firstNonEmptyStr(c.Title, strings.TrimSpace(c.FirstName+" "+c.LastName)))
	if c.Username != "" {
		name = strings.TrimSpace(name + " (@" + c.Username + ")")
	}
	entry := telegramChat{ID: strconv.FormatInt(c.ID, 10), Name: name, Type: c.Type}
	b.mu.Lock()
	defer b.mu.Unlock()
	list := []telegramChat{entry}
	for _, e := range b.seen {
		if e.ID != entry.ID && len(list) < 20 {
			list = append(list, e)
		}
	}
	b.seen = list
}

func (s *Server) userLang(user int64) string { return i18n.Normalize(s.settingsFor(user)["language"]) }

// tgCommand splits "/web@MyBot question" into "web" and "question".
func tgCommand(text, botName string) (string, string) {
	if !strings.HasPrefix(text, "/") {
		return "", text
	}
	head, rest, _ := strings.Cut(text[1:], " ")
	cmd, at, hasAt := strings.Cut(head, "@")
	if hasAt && !strings.EqualFold(at, botName) {
		return "other", "" // meant for another bot in the group
	}
	return strings.ToLower(cmd), strings.TrimSpace(rest)
}

func (b *tgBot) onMessage(ctx context.Context, m tgMessage) {
	b.mu.Lock()
	c, ok := b.chats[m.Chat.ID]
	user := c.user
	name := b.name
	b.mu.Unlock()
	if !ok || m.From == nil || m.From.IsBot {
		return // not a chat this bot answers in
	}
	text := strings.TrimSpace(firstNonEmptyStr(m.Text, m.Caption))
	file := len(m.Photo) > 0 || m.Document != nil
	if text == "" && !file {
		return
	}
	group := m.Chat.Type != "private"
	cmd, rest := tgCommand(text, name)
	sceneCmd := cmd == "szene" || cmd == "scene" || cmd == "szenen" || cmd == "scenes"
	if !c.ai && !sceneCmd {
		return // without the AI the bot only starts scenes and listens for buttons
	}
	mention := "@" + name
	addressed := name != "" && strings.Contains(strings.ToLower(text), strings.ToLower(mention))
	replied := m.ReplyTo != nil && m.ReplyTo.From != nil && m.ReplyTo.From.IsBot && strings.EqualFold(m.ReplyTo.From.Username, name)
	if group && cmd == "" && !addressed && !replied {
		return // in a group the bot only answers when it is spoken to
	}
	lang := b.s.userLang(user)
	// In a group only its admins may change what the AI remembers.
	adminOnly := func() bool {
		if group && !b.groupAdmin(ctx, m.Chat.ID, m.From.ID) {
			b.reply(ctx, m, i18n.T(lang, "Das dürfen in dieser Gruppe nur Admins."), "", nil)
			return false
		}
		return true
	}
	web := false
	switch cmd {
	case "start", "help", "hilfe":
		help := i18n.T(lang, "👋 Ich bin die KI deines Heimdeck. Frag mich etwas zu deinen Diensten, z. B. „Läuft alles?“ oder „Was wird gerade geladen?“ Du kannst mir auch Fotos und Dateien schicken, z. B. einen Brief zum Zusammenfassen.\n\n/lage – Lagebericht\n/kamera – aktuelles Kamerabild\n/szene – eine Szene starten\n/web Frage – mit Websuche\n/merken Text – merke ich mir für später\n/vergessen – Gemerktes ansehen und löschen\n/neu – neues Gespräch\n\nAktionen (z. B. Downloads pausieren) schlage ich nur als Knopf vor – ausgeführt wird erst, wenn du tippst.")
		if group {
			help += "\n\n" + i18n.T(lang, "In dieser Gruppe fragt ihr mich mit /ki Frage oder antwortet auf eine meiner Nachrichten (%s). Aktionen können nur Admins der Gruppe auslösen.", mention)
		}
		b.reply(ctx, m, help, "", nil)
		return
	case "neu", "new", "reset":
		b.mu.Lock()
		delete(b.convs, m.Chat.ID)
		b.mu.Unlock()
		b.reply(ctx, m, i18n.T(lang, "Neues Gespräch – das bisherige habe ich vergessen."), "", nil)
		return
	case "lage", "status", "bericht", "report":
		go b.report(ctx, m, user, lang)
		return
	case "kamera", "camera", "cam":
		go b.cameras(ctx, m, user, lang, rest)
		return
	case "szene", "scene", "szenen", "scenes":
		go b.scenes(ctx, m, user, lang, rest, group)
		return
	case "merken", "merk", "remember":
		if adminOnly() {
			b.rememberCmd(ctx, m, user, lang, rest)
		}
		return
	case "vergessen", "vergiss", "forget", "gedaechtnis", "gedächtnis", "memory":
		if adminOnly() {
			b.memoryList(ctx, m, user, lang)
		}
		return
	case "web":
		web, text = true, rest
	case "ki", "ask", "frag":
		text = rest
	case "":
		if addressed {
			text = strings.TrimSpace(regexp.MustCompile(`(?i)@`+regexp.QuoteMeta(name)+`\b`).ReplaceAllString(text, ""))
		}
	default:
		return // a command for something else
	}
	if text == "" && !file {
		return
	}

	b.mu.Lock()
	conv := b.convs[m.Chat.ID]
	if conv == nil || time.Since(conv.at) > tgIdle {
		conv = &tgConv{}
		b.convs[m.Chat.ID] = conv
	}
	if conv.busy {
		b.mu.Unlock()
		b.reply(ctx, m, i18n.T(lang, "Einen Moment – ich antworte noch auf die letzte Nachricht."), "", nil)
		return
	}
	conv.busy = true
	conv.at = time.Now()
	b.mu.Unlock()

	go func() {
		defer logging.Recover("Telegram-Bot")
		defer func() {
			b.mu.Lock()
			conv.busy = false
			b.mu.Unlock()
		}()
		// A photo or a file goes along with the question.
		stop := b.typing(ctx, m.Chat.ID)
		images, fileText, err := b.attachment(ctx, m, lang)
		stop()
		if err != nil {
			b.reply(ctx, m, "⚠ "+err.Error(), "", nil)
			return
		}
		q := text
		switch {
		case q != "":
		case len(images) > 0:
			q = i18n.T(lang, "Was ist auf dem Bild? Fasse kurz zusammen, was wichtig ist.")
		default:
			q = i18n.T(lang, "Fasse diese Datei kurz zusammen.")
		}
		if group {
			// Several people may ask in a group: the model learns who it is.
			q = strings.TrimSpace(m.From.FirstName+" "+m.From.LastName) + ": " + q
		}
		if fileText != "" {
			q = fileText + "\n\n" + q
		}
		b.mu.Lock()
		conv.msgs = trimConv(append(conv.msgs, chatMessage{Role: "user", Text: clip(q, 40000), Images: images}))
		history := append([]chatMessage{}, conv.msgs...)
		b.mu.Unlock()
		b.answer(ctx, m, user, lang, history, web, conv)
	}()
}

// trimConv keeps a conversation short enough for the model: the last
// messages, at most about 120,000 characters, and pictures only in the newest
// message that has some.
func trimConv(msgs []chatMessage) []chatMessage {
	if len(msgs) > tgHistory {
		msgs = msgs[len(msgs)-tgHistory:]
	}
	total := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		total += len(msgs[i].Text)
		if total > 120_000 && i < len(msgs)-1 {
			msgs = msgs[i+1:]
			break
		}
	}
	picture := false
	for i := len(msgs) - 1; i >= 0; i-- {
		if len(msgs[i].Images) == 0 {
			continue
		}
		if picture {
			msgs[i].Images = nil
		}
		picture = true
	}
	return msgs
}

const tgMaxFile = 15 << 20

// attachment fetches a photo or file sent with the message: pictures as
// pictures for models that can see, text and Word documents as text.
func (b *tgBot) attachment(ctx context.Context, m tgMessage, lang string) ([]string, string, error) {
	switch {
	case len(m.Photo) > 0:
		// Telegram lists the sizes from small to large; a large one is enough.
		best := m.Photo[0]
		for _, p := range m.Photo {
			if p.Width <= 2560 {
				best = p
			}
		}
		data, err := b.download(ctx, best.FileID, lang)
		if err != nil {
			return nil, "", err
		}
		return []string{"data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(data)}, "", nil
	case m.Document != nil:
		d := m.Document
		if d.FileSize > tgMaxFile {
			return nil, "", errors.New(i18n.T(lang, "Die Datei ist zu groß"))
		}
		mime := strings.ToLower(d.MimeType)
		ext := strings.ToLower(path.Ext(d.FileName))
		textExt := map[string]bool{".txt": true, ".md": true, ".csv": true, ".json": true, ".log": true, ".yaml": true, ".yml": true, ".xml": true, ".ini": true, ".conf": true, ".toml": true}
		readable := strings.HasPrefix(mime, "image/") && mime != "image/svg+xml" || ext == ".docx" || strings.HasPrefix(mime, "text/") || textExt[ext]
		if mime == "application/pdf" || ext == ".pdf" {
			return nil, "", errors.New(i18n.T(lang, "PDFs kann ich hier noch nicht lesen – schick ein Foto der Seiten, oder nimm den KI-Chat in Heimdeck, der liest PDFs."))
		}
		if !readable {
			return nil, "", errors.New(i18n.T(lang, "Diese Datei kann ich nicht lesen – Fotos, Bilder, Text und Word (.docx) gehen."))
		}
		data, err := b.download(ctx, d.FileID, lang)
		if err != nil {
			return nil, "", err
		}
		var content string
		switch {
		case strings.HasPrefix(mime, "image/"):
			return []string{"data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)}, "", nil
		case ext == ".docx":
			if content, err = docxText(data); err != nil {
				return nil, "", errors.New(i18n.T(lang, "Diese Datei kann ich nicht lesen – Fotos, Bilder, Text und Word (.docx) gehen."))
			}
		default:
			if !utf8.Valid(data) {
				return nil, "", errors.New(i18n.T(lang, "Diese Datei kann ich nicht lesen – Fotos, Bilder, Text und Word (.docx) gehen."))
			}
			content = string(data)
		}
		return nil, "File \"" + clip(d.FileName, 120) + "\":\n```\n" + clip(content, 30000) + "\n```", nil
	}
	return nil, "", nil
}

// download fetches a file the user sent (getFile, then the file address).
func (b *tgBot) download(ctx context.Context, id, lang string) ([]byte, error) {
	var f struct {
		FilePath string `json:"file_path"`
	}
	if err := tgCall(ctx, b.token, "getFile", map[string]any{"file_id": id}, &f); err != nil || f.FilePath == "" {
		return nil, errors.New(i18n.T(lang, "Die Datei ließ sich nicht von Telegram laden – Telegram gibt Bots nur Dateien bis 20 MB."))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, telegramAPI+"/file/bot"+b.token+"/"+f.FilePath, nil)
	if err != nil {
		return nil, err
	}
	resp, err := tgClient.Do(req)
	if err != nil {
		return nil, errors.New(logging.Redact(err.Error()))
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, tgMaxFile+1))
	if err != nil || resp.StatusCode != 200 {
		return nil, errors.New(i18n.T(lang, "Die Datei ließ sich nicht von Telegram laden – Telegram gibt Bots nur Dateien bis 20 MB."))
	}
	if len(data) > tgMaxFile {
		return nil, errors.New(i18n.T(lang, "Die Datei ist zu groß"))
	}
	return data, nil
}

func (b *tgBot) answer(ctx context.Context, m tgMessage, user int64, lang string, history []chatMessage, web bool, conv *tgConv) {
	stop := b.typing(ctx, m.Chat.ID)
	actx, cancel := context.WithTimeout(ctx, tgAnswerTime)
	reply, err := b.s.botAnswer(actx, user, lang, history, web, m.Chat.Type == "private")
	cancel()
	stop()
	if err != nil {
		b.reply(ctx, m, "⚠ "+err.Error(), "", nil)
		return
	}
	b.mu.Lock()
	conv.msgs = trimConv(append(conv.msgs, chatMessage{Role: "assistant", Text: clip(reply.Text, 8000)}))
	conv.at = time.Now()
	var keyboard [][]map[string]string
	for _, p := range reply.Proposals {
		b.seq++
		id := strconv.Itoa(b.seq)
		b.pending[id] = &tgPending{p: p, chat: m.Chat.ID, user: user, at: time.Now()}
		keyboard = append(keyboard, []map[string]string{{"text": "▶ " + clip(p.Label+" · "+p.Tile, 60), "callback_data": "p:" + id}})
	}
	for id, p := range b.pending {
		if time.Since(p.at) > tgPendingTTL {
			delete(b.pending, id)
		}
	}
	b.mu.Unlock()
	b.reply(ctx, m, reply.Text, strings.Join(reply.Notes, " · "), keyboard)
}

// report sends the situation report and keeps it in the conversation, so a
// follow-up question ("why is Sonarr down?") knows what was meant.
func (b *tgBot) report(ctx context.Context, m tgMessage, user int64, lang string) {
	defer logging.Recover("Telegram-Bot")
	stop := b.typing(ctx, m.Chat.ID)
	rctx, cancel := context.WithTimeout(ctx, tgAnswerTime)
	text, err := b.s.statusReport(rctx, user, lang)
	cancel()
	stop()
	if err != nil {
		b.reply(ctx, m, "⚠ "+err.Error(), "", nil)
		return
	}
	b.reply(ctx, m, text, "", nil)
	b.mu.Lock()
	conv := b.convs[m.Chat.ID]
	if conv == nil || time.Since(conv.at) > tgIdle {
		conv = &tgConv{}
		b.convs[m.Chat.ID] = conv
	}
	if !conv.busy {
		conv.msgs = trimConv(append(conv.msgs, chatMessage{Role: "user", Text: i18n.T(lang, "Lagebericht")}, chatMessage{Role: "assistant", Text: text}))
		conv.at = time.Now()
	}
	b.mu.Unlock()
}

// cameras sends a camera's current picture – or, with several, asks which one.
func (b *tgBot) cameras(ctx context.Context, m tgMessage, user int64, lang, name string) {
	defer logging.Recover("Telegram-Bot")
	cams := b.s.cameraTiles(ctx, user, lang, name)
	switch {
	case len(cams) == 0 && name != "":
		b.reply(ctx, m, i18n.T(lang, "Keine Kamera namens „%s“ gefunden.", clip(name, 60)), "", nil)
	case len(cams) == 0:
		b.reply(ctx, m, i18n.T(lang, "Auf deinem Dashboard ist keine Kamera."), "", nil)
	case len(cams) == 1:
		b.sendCamera(ctx, m.Chat.ID, user, lang, cams[0].ID)
	default:
		var keyboard [][]map[string]string
		for i, t := range cams {
			if i == 12 {
				break
			}
			keyboard = append(keyboard, []map[string]string{{"text": "📹 " + clip(t.Name, 50), "callback_data": "c:" + strconv.FormatInt(t.ID, 10)}})
		}
		b.reply(ctx, m, i18n.T(lang, "Welche Kamera?"), "", keyboard)
	}
}

func (b *tgBot) sendCamera(ctx context.Context, chat, user int64, lang string, id int64) {
	tgCall(ctx, b.token, "sendChatAction", map[string]any{"chat_id": chat, "action": "upload_photo"}, nil)
	pic, err := b.s.cameraPicture(ctx, user, lang, id)
	if err != nil {
		b.send(ctx, chat, "⚠ "+err.Error(), nil)
		return
	}
	caption := "📹 " + pic.name + " · " + pic.taken.In(time.Local).Format("15:04:05")
	method, field, file := "sendPhoto", "photo", "camera.jpg"
	switch pic.ct {
	case "video/mp4":
		// A key frame from the stream: Telegram shows it as a still clip.
		method, field, file = "sendAnimation", "animation", "camera.mp4"
	case "image/png":
		file = "camera.png"
	}
	if err := tgUpload(ctx, b.token, method, map[string]string{"chat_id": strconv.FormatInt(chat, 10), "caption": caption}, field, file, pic.ct, pic.data); err != nil {
		slog.Warn("Telegram-Bot: Kamerabild nicht gesendet", "kachel", pic.name, "fehler", logging.Redact(err.Error()))
		b.send(ctx, chat, "⚠ "+i18n.T(lang, "Das Kamerabild ließ sich nicht an Telegram schicken."), nil)
	}
}

// rememberCmd: /merken text.
func (b *tgBot) rememberCmd(ctx context.Context, m tgMessage, user int64, lang, text string) {
	if strings.TrimSpace(text) == "" {
		b.reply(ctx, m, i18n.T(lang, "Was soll ich mir merken? Zum Beispiel: /merken Mein NAS heißt tank"), "", nil)
		return
	}
	item, err := b.s.addMemory(user, text, lang)
	if err != nil {
		b.reply(ctx, m, "⚠ "+err.Error(), "", nil)
		return
	}
	b.reply(ctx, m, "🧠 "+i18n.T(lang, "Gemerkt: %s", item.Text), "", nil)
}

// memoryList shows what the AI remembers, each with a button to forget it.
func (b *tgBot) memoryList(ctx context.Context, m tgMessage, user int64, lang string) {
	items := b.s.memoryOf(user)
	if len(items) == 0 {
		b.reply(ctx, m, i18n.T(lang, "Ich habe mir noch nichts gemerkt. Mit /merken Text merke ich mir etwas."), "", nil)
		return
	}
	var keyboard [][]map[string]string
	for _, it := range items {
		keyboard = append(keyboard, []map[string]string{{"text": "🗑 " + clip(it.Text, 55), "callback_data": "m:" + it.ID}})
	}
	b.reply(ctx, m, i18n.T(lang, "Das habe ich mir gemerkt – tippe auf einen Punkt, um ihn zu vergessen:"), "", keyboard)
}

// typing shows "… is typing" until the answer is there (Telegram shows it for 5 s).
func (b *tgBot) typing(ctx context.Context, chat int64) func() {
	tctx, cancel := context.WithCancel(ctx)
	go func() {
		for tctx.Err() == nil {
			tgCall(tctx, b.token, "sendChatAction", map[string]any{"chat_id": chat, "action": "typing"}, nil)
			sleepCtx(tctx, 4*time.Second)
		}
	}()
	return cancel
}

// reply sends an answer written in Markdown, split into several messages when
// it is long; the buttons go below the last one. Notes (what the AI looked at)
// come first in small print.
func (b *tgBot) reply(ctx context.Context, m tgMessage, text, notes string, keyboard [][]map[string]string) {
	parts := tgSplit(text, tgMaxMessage)
	for i, part := range parts {
		body := map[string]any{"chat_id": m.Chat.ID, "link_preview_options": map[string]bool{"is_disabled": true}}
		if i == 0 && m.Chat.Type != "private" {
			body["reply_parameters"] = map[string]any{"message_id": m.MessageID, "allow_sending_without_reply": true}
		}
		if i == len(parts)-1 && len(keyboard) > 0 {
			body["reply_markup"] = map[string]any{"inline_keyboard": keyboard}
		}
		formatted := tgHTML(part)
		plain := part
		if i == 0 && notes != "" {
			formatted = "<i>🔧 " + html.EscapeString(notes) + "</i>\n\n" + formatted
			plain = "🔧 " + notes + "\n\n" + plain
		}
		if err := tgSendHTML(ctx, b.token, body, formatted, plain); err != nil {
			slog.Warn("Telegram-Bot: Antwort nicht gesendet", "fehler", logging.Redact(err.Error()))
			return
		}
	}
}

// tgSendHTML sends a formatted message; when Telegram does not like the
// formatting, it sends the plain text instead.
func tgSendHTML(ctx context.Context, token string, body map[string]any, formatted, plain string) error {
	body["text"], body["parse_mode"] = formatted, "HTML"
	err := tgCall(ctx, token, "sendMessage", body, nil)
	var te *tgError
	if errors.As(err, &te) && te.Status == 400 {
		delete(body, "parse_mode")
		body["text"] = plain
		err = tgCall(ctx, token, "sendMessage", body, nil)
	}
	return err
}

// send writes a short message without reply or formatting.
func (b *tgBot) send(ctx context.Context, chat int64, text string, keyboard [][]map[string]string) {
	body := map[string]any{"chat_id": chat, "text": text}
	if len(keyboard) > 0 {
		body["reply_markup"] = map[string]any{"inline_keyboard": keyboard}
	}
	if err := tgCall(ctx, b.token, "sendMessage", body, nil); err != nil {
		slog.Warn("Telegram-Bot: Nachricht nicht gesendet", "fehler", logging.Redact(err.Error()))
	}
}

// groupAdmin says whether someone may tap action buttons in a group.
func (b *tgBot) groupAdmin(ctx context.Context, chat, user int64) bool {
	var member struct {
		Status string `json:"status"`
	}
	if err := tgCall(ctx, b.token, "getChatMember", map[string]any{"chat_id": chat, "user_id": user}, &member); err != nil {
		return false
	}
	return member.Status == "creator" || member.Status == "administrator"
}

// onButton handles a tapped action button: a confirmation first when the
// tile asks for one, then the action – with the user's rights checked again.
func (b *tgBot) onButton(ctx context.Context, q tgCallback) {
	defer logging.Recover("Telegram-Bot")
	kind, id, _ := strings.Cut(q.Data, ":")
	b.mu.Lock()
	p := b.pending[id]
	owner, known := int64(0), false
	if q.Message != nil {
		var c tgChatUser
		c, known = b.chats[q.Message.Chat.ID]
		owner = c.user
	}
	b.mu.Unlock()
	lang := i18n.DE
	switch {
	case p != nil:
		lang = b.s.userLang(p.user)
	case known:
		lang = b.s.userLang(owner) // a button that ran or expired
	}
	answer := func(text string) {
		tgCall(ctx, b.token, "answerCallbackQuery", map[string]any{"callback_query_id": q.ID, "text": text}, nil)
	}
	switch kind {
	case "s":
		// A scene from the list.
		if !known || q.Message == nil {
			answer("")
			return
		}
		if q.Message.Chat.Type != "private" && !b.groupAdmin(ctx, q.Message.Chat.ID, q.From.ID) {
			answer(i18n.T(lang, "Das dürfen in dieser Gruppe nur Admins."))
			return
		}
		r, ok := b.s.sceneOf(owner, id)
		if !ok || !r.On {
			answer(i18n.T(lang, "Die Szene gibt es nicht mehr"))
			return
		}
		answer("🎬")
		b.runScene(ctx, q.Message.Chat.ID, owner, lang, r)
		return
	case "r", "x":
		// An automation asks whether to run its buttons.
		if !known || q.Message == nil {
			answer("")
			return
		}
		if q.Message.Chat.Type != "private" && !b.groupAdmin(ctx, q.Message.Chat.ID, q.From.ID) {
			answer(i18n.T(lang, "Nur Admins dieser Gruppe können Aktionen auslösen."))
			return
		}
		answer("⏳")
		tgCall(ctx, b.token, "editMessageReplyMarkup", map[string]any{"chat_id": q.Message.Chat.ID, "message_id": q.Message.MessageID, "reply_markup": map[string]any{"inline_keyboard": []any{}}}, nil)
		b.send(ctx, q.Message.Chat.ID, b.s.answerRule(ctx, owner, id, kind == "r", lang), nil)
		return
	case "c", "m":
		// A camera from the list, or something to forget.
		if !known || q.Message == nil {
			answer("")
			return
		}
		if kind == "c" {
			answer("📹")
			cam, _ := strconv.ParseInt(id, 10, 64)
			b.sendCamera(ctx, q.Message.Chat.ID, owner, lang, cam)
			return
		}
		if q.Message.Chat.Type != "private" && !b.groupAdmin(ctx, q.Message.Chat.ID, q.From.ID) {
			answer(i18n.T(lang, "Das dürfen in dieser Gruppe nur Admins."))
			return
		}
		if item, ok := b.s.forgetMemory(owner, id); ok {
			answer(i18n.T(lang, "Vergessen: %s", clip(item.Text, 150)))
			b.send(ctx, q.Message.Chat.ID, "🗑 "+i18n.T(lang, "Vergessen: %s", item.Text), nil)
		} else {
			answer(i18n.T(lang, "Schon vergessen"))
		}
		return
	}
	if p == nil || q.Message == nil || q.Message.Chat.ID != p.chat || time.Since(p.at) > tgPendingTTL {
		answer(i18n.T(lang, "Diese Aktion ist abgelaufen – frag bitte nochmal."))
		return
	}
	if q.Message.Chat.Type != "private" && !b.groupAdmin(ctx, p.chat, q.From.ID) {
		answer(i18n.T(lang, "Nur Admins dieser Gruppe können Aktionen auslösen."))
		return
	}
	clearButtons := func() {
		tgCall(ctx, b.token, "editMessageReplyMarkup", map[string]any{"chat_id": p.chat, "message_id": q.Message.MessageID, "reply_markup": map[string]any{"inline_keyboard": []any{}}}, nil)
	}
	switch kind {
	case "n":
		b.mu.Lock()
		delete(b.pending, id)
		b.mu.Unlock()
		answer(i18n.T(lang, "Abgebrochen"))
		clearButtons()
		return
	case "p":
		if p.p.Confirm != "" {
			b.mu.Lock()
			p.asked = true
			b.mu.Unlock()
			answer("")
			b.send(ctx, p.chat, "⚠ "+p.p.Confirm, [][]map[string]string{{
				{"text": "✅ " + i18n.T(lang, "Ja, ausführen"), "callback_data": "y:" + id},
				{"text": i18n.T(lang, "Abbrechen"), "callback_data": "n:" + id},
			}})
			return
		}
	case "y":
		if !p.asked {
			answer(i18n.T(lang, "Diese Aktion ist abgelaufen – frag bitte nochmal."))
			return
		}
		clearButtons()
	default:
		return
	}
	// Each button runs once.
	b.mu.Lock()
	if b.pending[id] != p {
		b.mu.Unlock()
		answer(i18n.T(lang, "Diese Aktion ist abgelaufen – frag bitte nochmal."))
		return
	}
	delete(b.pending, id)
	b.mu.Unlock()
	answer("⏳")
	if err := b.s.botAction(ctx, p.user, lang, p.p); err != nil {
		b.send(ctx, p.chat, "⚠ "+i18n.T(lang, "Das hat nicht geklappt: %s", err.Error()), nil)
		return
	}
	b.send(ctx, p.chat, "✅ "+i18n.T(lang, "Erledigt: %s", p.p.Label+" · "+p.p.Tile), nil)
}

// ---- the AI behind the bot ----

// botRequest is a request in the user's name for code that expects one. It
// has no login session, so protected groups count as locked.
func (s *Server) botRequest(ctx context.Context, user int64, lang string) *http.Request {
	ctx = context.WithValue(auth.NewContext(ctx, user), langKey{}, lang)
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	return r
}

// botAction runs a tapped action with the same checks as the button on the dashboard.
func (s *Server) botAction(ctx context.Context, user int64, lang string, p proposal) error {
	r := s.botRequest(ctx, user, lang)
	t, acc, _, err := s.tileAccess(r, p.TileID)
	if err != nil {
		return errors.New(i18n.T(lang, "Diese Kachel gibt es nicht mehr"))
	}
	if open, _ := s.groupUnlocked(r, t.GroupID); !open || !acc.Act {
		return errors.New(i18n.T(lang, errNoAct.Error()))
	}
	if !s.dockerAllowed(t) {
		return errors.New(i18n.T(lang, errDockerOwner.Error()))
	}
	integ, _ := integrations.Get(t.Integration)
	actioner, ok := integ.(integrations.Actioner)
	if !ok {
		return errors.New(i18n.T(lang, "dieser Dienst unterstützt keine Aktionen"))
	}
	actx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	start := time.Now()
	if err := actioner.Action(actx, s.instance(t, lang), p.Action, p.Params); err != nil {
		slog.Warn("Aktion fehlgeschlagen", "kachel", t.Name, "dienst", t.Integration, "aktion", p.Action, "über", "Telegram", "fehler", logging.Redact(err.Error()))
		return errors.New(i18n.T(lang, logging.Public(err.Error())))
	}
	slog.Info("Aktion ausgeführt", "kachel", t.Name, "dienst", t.Integration, "aktion", p.Action, "benutzer", s.usernameOf(user), "über", "Telegram", "dauer", time.Since(start).Round(time.Millisecond))
	s.forget(t.ID)
	return nil
}

// botReply is a whole answer for Telegram.
type botReply struct {
	Text      string
	Notes     []string // what the AI looked at
	Proposals []proposal
}

// botAnswer asks the AI with the user's rights: the dashboard with tools when
// the admin allows it, the whole dashboard for models without tools, and the
// web when asked with /web.
func (s *Server) botAnswer(ctx context.Context, user int64, lang string, history []chatMessage, web, private bool) (botReply, error) {
	u, err := s.store.User(user)
	if err != nil || u.Disabled || s.aiFor(u) == nil {
		return botReply{}, errors.New(i18n.T(lang, "Die KI ist für dein Konto nicht eingerichtet"))
	}
	c := s.aiConfig()
	r := s.botRequest(ctx, user, lang)
	system := "You are the assistant inside Heimdeck, a self-hosted home dashboard, talking to its user in Telegram. Answer in " +
		map[string]string{"de": "German", "en": "English"}[lang] + " unless the user writes in another language. " +
		"Keep it short – it is read on a phone. You may use **bold**, lists, `code` and links, but no tables or headings. " +
		"Current date and time: " + time.Now().Format("Monday, 2 January 2006, 15:04") + "." +
		memoryPrompt(s.memoryOf(user))
	var notes []string
	var extra map[string]any
	question := history[len(history)-1].Text
	switch {
	case web && c.webReady() && c.webProvider() == webOpenRouter:
		extra = openRouterWeb()
		system += " Web search results may be added to the question; cite them with their links."
	case web && c.webReady():
		wctx, cancel := context.WithTimeout(ctx, 12*time.Second)
		results, err := webResults(wctx, c, clip(question, 300))
		cancel()
		switch {
		case err != nil:
			slog.Warn("Websuche für die KI", "fehler", err)
			notes = append(notes, "🌐 "+webErrText(r, err))
			system += " The web search failed this time. If the question needs current information, say briefly that the web search did not work."
		case len(results) == 0:
			notes = append(notes, "🌐 "+i18n.T(lang, "Die Websuche hat nichts gefunden"))
		default:
			system += "\n\nWeb search results for the last question (cite them as [1], [2] … with the link when you use them):\n" + strings.Join(results, "\n")
		}
	case web:
		notes = append(notes, "🌐 "+i18n.T(lang, "Die Websuche ist nicht eingerichtet"))
	default:
		if c.webReady() {
			system += " You have no internet access in this message; if current information is needed, say that the user can ask with /web followed by the question."
		} else {
			system += " You have no internet access."
		}
	}
	msgs := []any{map[string]string{"role": "system", "content": system}}
	for _, m := range history {
		role := "user"
		if m.Role == "assistant" {
			role = "assistant"
		}
		if len(m.Images) == 0 || role != "user" {
			msgs = append(msgs, map[string]string{"role": role, "content": m.Text})
			continue
		}
		parts := []any{map[string]string{"type": "text", "text": m.Text}}
		for _, img := range m.Images {
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": img}})
		}
		msgs = append(msgs, map[string]any{"role": role, "content": parts})
	}
	pictures := len(history[len(history)-1].Images) > 0
	if c.Dashboard && c.Tools {
		withTools := append([]any{map[string]string{"role": "system", "content": system + aiToolPrompt}}, msgs[1:]...)
		reply, err := s.botTools(ctx, r, c, withTools, extra, lang, private)
		if !errors.Is(err, errNoTools) {
			reply.Notes = append(notes, reply.Notes...)
			return reply, pictureHint(err, pictures, lang)
		}
	}
	if c.Dashboard {
		if dash, n := s.dashboardContext(r); n > 0 {
			msgs[0] = map[string]string{"role": "system", "content": system + dashboardPrompt(dash)}
			notes = append(notes, "📋 "+i18n.T(lang, "%d Kacheln", n))
		}
	}
	text, _, err := aiComplete(ctx, c, msgs, false, false, extra, lang, false)
	if err == nil && strings.TrimSpace(text) == "" {
		text = i18n.T(lang, "Die KI hat keine Antwort gegeben.")
	}
	slog.Debug("KI-Antwort über Telegram", "modell", c.Model, "benutzer", u.Username, "zeichen", len(text))
	return botReply{Text: text, Notes: notes}, pictureHint(err, pictures, lang)
}

// pictureHint explains a refused question with a picture: most likely the
// model cannot see.
func pictureHint(err error, pictures bool, lang string) error {
	if err == nil || !pictures {
		return err
	}
	return errors.New(err.Error() + "\n" + i18n.T(lang, "Kann das Modell Bilder lesen? Dafür braucht es eines mit Bildverständnis, z. B. qwen2.5-vl, llava oder gpt-4o-mini."))
}

// botTools is the conversation with tools, like the chat window's, but
// collects the whole answer instead of streaming it.
func (s *Server) botTools(ctx context.Context, r *http.Request, c aiConfig, messages []any, extra map[string]any, lang string, memory bool) (botReply, error) {
	run := s.newToolRun(ctx, r)
	run.memory = memory // in a group, nobody changes the owner's memory by chatting
	var reply botReply
	var text strings.Builder
	seen := map[string]bool{}
	for round := 0; round < toolRounds; round++ {
		part, calls, err := aiComplete(ctx, c, messages, true, round == toolRounds-1, extra, lang, memory)
		if err != nil {
			if round > 0 && errors.Is(err, errNoTools) {
				err = errors.New(i18n.T(lang, "Die KI ist gerade nicht erreichbar"))
			}
			return reply, err
		}
		if strings.TrimSpace(part) != "" {
			if text.Len() > 0 {
				text.WriteString("\n\n")
			}
			text.WriteString(strings.TrimSpace(part))
		}
		if len(calls) == 0 {
			break
		}
		if len(calls) > toolCallsRound {
			calls = calls[:toolCallsRound]
		}
		assistant := map[string]any{"role": "assistant", "tool_calls": calls, "content": nil}
		if part != "" {
			assistant["content"] = part
		}
		messages = append(messages, assistant)
		for _, call := range calls {
			result, ev := run.call(call.Function.Name, call.Function.Arguments)
			switch {
			case ev == nil:
			case ev["propose"] != nil:
				p := ev["propose"].(proposal)
				key := fmt.Sprint(p.TileID, p.Action, p.Params)
				if !seen[key] {
					seen[key] = true
					reply.Proposals = append(reply.Proposals, p)
				}
			case ev["tool"] == "list":
				reply.Notes = append(reply.Notes, i18n.T(lang, "Kacheln angesehen"))
			case ev["tool"] == "read":
				reply.Notes = append(reply.Notes, i18n.T(lang, "%s gelesen", ev["arg"]))
			case ev["tool"] == "search":
				reply.Notes = append(reply.Notes, i18n.T(lang, "gesucht: %s", ev["arg"]))
			case ev["tool"] == "remember":
				reply.Notes = append(reply.Notes, "🧠 "+i18n.T(lang, "gemerkt: %s", ev["arg"]))
			}
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": call.ID, "name": call.Function.Name, "content": result})
		}
	}
	reply.Text = text.String()
	if reply.Text == "" {
		reply.Text = i18n.T(lang, "Die KI hat keine Antwort gegeben.")
	}
	return reply, nil
}

// aiComplete asks the model for one whole answer, without streaming. With
// tools, a model that does not take them gives errNoTools.
func aiComplete(ctx context.Context, c aiConfig, messages []any, tools, last bool, extra map[string]any, lang string, memory bool) (string, []toolCall, error) {
	body := map[string]any{"model": c.Model, "stream": false, "temperature": 0.3, "messages": messages}
	if tools {
		body["tools"] = toolDefs(memory)
		if last {
			body["tool_choice"] = "none" // enough looking around – answer now
		}
	}
	for k, v := range extra {
		body[k] = v
	}
	resp, err := c.request(ctx, "POST", "/chat/completions", body)
	if err != nil {
		slog.Warn("KI nicht erreichbar", "fehler", err)
		return "", nil, errors.New(i18n.T(lang, "Die KI ist gerade nicht erreichbar"))
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 600))
		if tools && resp.StatusCode < 500 && strings.Contains(strings.ToLower(string(b)), "tool") {
			slog.Info("KI kann keine Werkzeuge, nehme die Übersicht", "modell", c.Model, "status", resp.Status)
			return "", nil, errNoTools
		}
		slog.Warn("KI lehnt ab", "status", resp.Status, "antwort", string(b))
		return "", nil, errors.New(i18n.T(lang, "Die KI antwortet mit %s – Details im Protokoll", resp.Status))
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content   *string    `json:"content"`
				ToolCalls []toolCall `json:"tool_calls"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&out); err != nil || len(out.Choices) == 0 {
		slog.Warn("KI-Antwort nicht lesbar", "fehler", err)
		return "", nil, errors.New(i18n.T(lang, "Die KI ist gerade nicht erreichbar"))
	}
	msg := out.Choices[0].Message
	text := ""
	if msg.Content != nil {
		text = *msg.Content
	}
	calls := msg.ToolCalls[:0]
	for i, tc := range msg.ToolCalls {
		if tc.Function.Name == "" {
			continue
		}
		if tc.ID == "" {
			tc.ID = "call_" + strconv.Itoa(i)
		}
		tc.Type = "function"
		calls = append(calls, tc)
	}
	return text, calls, nil
}

// ---- Markdown for Telegram ----

var (
	tgBoldRe   = regexp.MustCompile(`\*\*([^*\n]+)\*\*|__([^_\n]+)__`)
	tgItalicRe = regexp.MustCompile(`(^|[^*\w])\*([^*\s][^*\n]*?)\*([^*\w]|$)`)
	tgCodeRe   = regexp.MustCompile("`([^`\n]+)`")
	tgLinkRe   = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^\s)]+)\)`)
	tgHeadRe   = regexp.MustCompile(`^#{1,6}\s+(.*)$`)
	tgBulletRe = regexp.MustCompile(`^(\s*)[-*+]\s+`)
)

// tgHTML turns the Markdown models write into the few HTML tags Telegram
// understands: bold, italic, code, code blocks and links.
func tgHTML(md string) string {
	var out []string
	var code []string
	inCode := false
	for _, line := range strings.Split(md, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			if inCode {
				out = append(out, "<pre>"+html.EscapeString(strings.Join(code, "\n"))+"</pre>")
				code = nil
			}
			inCode = !inCode
			continue
		}
		if inCode {
			code = append(code, line)
			continue
		}
		head := false
		if m := tgHeadRe.FindStringSubmatch(line); m != nil {
			line, head = m[1], true
		}
		line = tgBulletRe.ReplaceAllString(line, "$1• ")
		line = tgInline(line)
		if head {
			line = "<b>" + line + "</b>"
		}
		out = append(out, line)
	}
	if inCode {
		out = append(out, "<pre>"+html.EscapeString(strings.Join(code, "\n"))+"</pre>")
	}
	return strings.Join(out, "\n")
}

// tgInline formats one line; code spans are left alone.
func tgInline(line string) string {
	var b strings.Builder
	last := 0
	for _, m := range tgCodeRe.FindAllStringSubmatchIndex(line, -1) {
		b.WriteString(tgText(line[last:m[0]]))
		b.WriteString("<code>" + html.EscapeString(line[m[2]:m[3]]) + "</code>")
		last = m[1]
	}
	b.WriteString(tgText(line[last:]))
	return b.String()
}

func tgText(s string) string {
	s = html.EscapeString(s)
	s = tgLinkRe.ReplaceAllString(s, `<a href="$2">$1</a>`)
	s = tgBoldRe.ReplaceAllString(s, "<b>$1$2</b>")
	s = tgItalicRe.ReplaceAllString(s, "$1<i>$2</i>$3")
	return s
}

// tgSplit cuts a long answer into messages, at paragraphs where possible.
func tgSplit(text string, max int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return []string{"…"}
	}
	var parts []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			parts = append(parts, strings.TrimSpace(cur.String()))
			cur.Reset()
		}
	}
	for _, para := range strings.Split(text, "\n\n") {
		for len(para) > max {
			flush()
			cut := strings.LastIndex(para[:max], "\n")
			if cut < max/2 {
				cut = max
				for cut > 0 && !utf8Start(para[cut]) {
					cut--
				}
			}
			parts = append(parts, strings.TrimSpace(para[:cut]))
			para = para[cut:]
		}
		if cur.Len()+len(para)+2 > max {
			flush()
		}
		if cur.Len() > 0 {
			cur.WriteString("\n\n")
		}
		cur.WriteString(para)
	}
	flush()
	return parts
}

func utf8Start(c byte) bool { return c&0xC0 != 0x80 }

// scenes starts a scene with /szene name – or offers the scenes as buttons.
func (b *tgBot) scenes(ctx context.Context, m tgMessage, user int64, lang, name string, group bool) {
	defer logging.Recover("Telegram-Bot")
	if group && !b.groupAdmin(ctx, m.Chat.ID, m.From.ID) {
		b.reply(ctx, m, i18n.T(lang, "Das dürfen in dieser Gruppe nur Admins."), "", nil)
		return
	}
	list := b.s.scenes(user, name)
	switch {
	case len(list) == 0 && name != "":
		b.reply(ctx, m, i18n.T(lang, "Keine Szene namens „%s“ gefunden.", clip(name, 60)), "", nil)
	case len(list) == 0:
		b.reply(ctx, m, i18n.T(lang, "Noch keine Szenen – lege sie unter Einstellungen → Automationen an (Auslöser: nur von Hand)."), "", nil)
	case len(list) == 1 && name != "":
		b.runScene(ctx, m.Chat.ID, user, lang, list[0])
	default:
		var keyboard [][]map[string]string
		for i, r := range list {
			if i == 20 {
				break
			}
			keyboard = append(keyboard, []map[string]string{{"text": "🎬 " + clip(r.Name, 50), "callback_data": "s:" + r.ID}})
		}
		b.reply(ctx, m, i18n.T(lang, "Welche Szene?"), "", keyboard)
	}
}

func (b *tgBot) runScene(ctx context.Context, chat, user int64, lang string, r rule) {
	e := b.s.startScene(ctx, user, r, "Telegram")
	icon := "🎬 "
	if !e.OK {
		icon = "⚠ "
	}
	b.send(ctx, chat, icon+r.Name+"\n"+e.Result, nil)
}
