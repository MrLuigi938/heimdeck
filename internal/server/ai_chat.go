package server

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// The AI chat window: a conversation with the model, with files the user
// adds (text, Word documents, pictures for models that can see) and – when
// the admin set up a web search – results from the web. The model itself
// never browses; Heimdeck searches and passes the results on.

type chatMessage struct {
	Role   string   `json:"role"` // user | assistant
	Text   string   `json:"text"`
	Images []string `json:"images"` // data:image/…;base64,… (scaled down in the browser)
}

const (
	maxChatMessages = 40
	maxChatChars    = 240_000 // whole conversation incl. file contents
	maxChatImages   = 4       // per message
	maxImageChars   = 6 << 20 // per picture, as data URL
)

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	u, err := s.store.User(uid(r))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	c := s.aiConfig()
	if s.aiFor(u) == nil {
		writeErr(w, 403, errors.New("Die KI ist nicht eingerichtet"))
		return
	}
	var body struct {
		Messages  []chatMessage `json:"messages"`
		Hits      []aiHit       `json:"hits"`
		Date      string        `json:"date"`
		Web       bool          `json:"web"`
		Dashboard bool          `json:"dashboard"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 30<<20)).Decode(&body); err != nil || len(body.Messages) == 0 {
		writeErr(w, 400, errors.New("Frage fehlt"))
		return
	}
	if len(body.Messages) > maxChatMessages {
		body.Messages = body.Messages[len(body.Messages)-maxChatMessages:]
	}
	total := 0
	for _, m := range body.Messages {
		total += len(m.Text)
	}
	if total > maxChatChars {
		writeErr(w, 413, errors.New("Das Gespräch ist zu lang – bitte einen neuen Chat beginnen"))
		return
	}
	last := body.Messages[len(body.Messages)-1]
	if last.Role != "user" || strings.TrimSpace(last.Text) == "" && len(last.Images) == 0 {
		writeErr(w, 400, errors.New("Frage fehlt"))
		return
	}

	lang := map[string]string{"de": "German", "en": "English"}[langOf(r)]
	system := "You are the assistant inside Heimdeck, a self-hosted home dashboard, talking to its user. Answer in " + lang +
		" unless the user writes in another language. Be concise and helpful; use Markdown (lists, **bold**, code blocks) where it helps. " +
		"When the user gives you a document or text to change, return the complete changed version in one code block so they can save it."
	if body.Date != "" {
		system += " The user's current local date and time: " + clip(body.Date, 60) + "."
	}
	var hits []string
	for i, h := range body.Hits {
		if i == 60 {
			break
		}
		line := "- [" + clip(h.Service, 40) + "] " + clip(h.Title, 160)
		if h.Subtitle != "" {
			line += " — " + clip(h.Subtitle, 200)
		}
		hits = append(hits, line)
	}
	if len(hits) > 0 {
		system += "\n\nResults from the user's own services (from their Heimdeck search) – cite the service in [brackets] when you use one:\n" + strings.Join(hits, "\n")
	}
	useTools := body.Dashboard && c.Dashboard && c.Tools
	// withDashboard adds the whole dashboard (stage 1, and the fallback for
	// models without tools).
	withDashboard := func(system string) string {
		dash, n := s.dashboardContext(r)
		if n == 0 {
			return system
		}
		w.Header().Set("X-Dashboard-Tiles", strconv.Itoa(n))
		return system + "\n\nThe user's Heimdeck dashboard right now (their tiles by dashboard and group, with live data; " +
			"\"OFFLINE\" means the service did not answer the last check). Use it to answer questions about their services, " +
			"name the tile you take something from, and say so when something is not on the dashboard:\n" + dash + "\n\n"
	}
	if body.Dashboard && c.Dashboard && !useTools {
		system = withDashboard(system)
	}
	webUsed := 0
	var extra map[string]any
	if body.Web && c.webReady() && c.webProvider() == webOpenRouter {
		// OpenRouter searches itself and adds the results to the question.
		extra = openRouterWeb()
		w.Header().Set("X-Web-Via", webName(webOpenRouter))
		system += " Web search results may be added to the question; cite them with their links."
	} else if body.Web && c.webReady() {
		ctx, cancel := timeout(r, 12*time.Second)
		results, err := webResults(ctx, c, clip(last.Text, 300))
		cancel()
		switch {
		case err != nil:
			slog.Warn("Websuche für die KI", "fehler", err)
			// The chat shows why; the model must not claim it never has internet.
			w.Header().Set("X-Web-Error", url.PathEscape(webErrText(r, err)))
			system += " The web search the user switched on failed this time (" + err.Error() + "). If the question needs current information, say briefly that the web search did not work – do not say that you never have internet access."
		case len(results) == 0:
			w.Header().Set("X-Web-Error", url.PathEscape(tr(r, "Die Websuche hat nichts gefunden")))
			system += " The web search found nothing for this question."
		default:
			webUsed = len(results)
			system += "\n\nWeb search results for the last question (cite them as [1], [2] … with the link when you use them):\n" + strings.Join(results, "\n")
		}
	} else {
		system += " You have no internet access; if asked, say that web search can be switched on in the chat (when the admin set it up)."
	}

	toolPrompt := "\n\nYou can look at the user's Heimdeck dashboard with tools: list_tiles (which services they have), read_tile (what a tile shows now, " +
		"and its actions), search_services (search their media, documents, smart home …). Use them whenever a question is about their services or home – " +
		"do not guess, and name the tile you take something from. You never run actions yourself: when the user wants something done (pause, switch, request …), " +
		"read the tile or search to get the action key, call propose_action, and tell them to click the button."
	sys := system
	if useTools {
		sys += toolPrompt
	}
	msgs := []any{map[string]string{"role": "system", "content": sys}}
	for _, m := range body.Messages {
		role := "user"
		if m.Role == "assistant" {
			role = "assistant"
		}
		var images []string
		for _, img := range m.Images {
			if role == "user" && strings.HasPrefix(img, "data:image/") && len(img) <= maxImageChars && len(images) < maxChatImages {
				images = append(images, img)
			}
		}
		if len(images) == 0 {
			msgs = append(msgs, map[string]string{"role": role, "content": m.Text})
			continue
		}
		parts := []any{map[string]string{"type": "text", "text": m.Text}}
		for _, img := range images {
			parts = append(parts, map[string]any{"type": "image_url", "image_url": map[string]string{"url": img}})
		}
		msgs = append(msgs, map[string]any{"role": role, "content": parts})
	}
	if webUsed > 0 {
		w.Header().Set("X-Web-Results", strconv.Itoa(webUsed))
	}
	if useTools {
		if err := s.chatWithTools(w, r, c, u, msgs, extra); !errors.Is(err, errNoTools) {
			return
		}
		// The model cannot use tools: give it the whole dashboard instead.
		w.Header().Set("X-AI-Tools", "fallback")
		msgs[0] = map[string]string{"role": "system", "content": withDashboard(system)}
	}
	s.streamChat(w, r, c, u, msgs, len(hits), extra)
}

// handleExtract returns the text of a Word document (.docx) for the chat;
// plain text files are read by the browser itself.
func (s *Server) handleExtract(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
		Data string `json:"data"` // base64
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 20<<20)).Decode(&body); err != nil {
		writeErr(w, 413, errors.New("Die Datei ist zu groß"))
		return
	}
	data, err := base64.StdEncoding.DecodeString(body.Data)
	if err != nil {
		writeErr(w, 400, errors.New("ungültige Datei"))
		return
	}
	text, err := docxText(data)
	if err != nil {
		writeErr(w, 415, errors.New("Diese Datei kann Heimdeck nicht lesen – Text, Markdown, CSV, JSON und Word (.docx) gehen"))
		return
	}
	writeJSON(w, map[string]string{"text": text})
}

// docxText pulls the paragraphs out of word/document.xml.
func docxText(data []byte) (string, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", err
	}
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", err
		}
		defer rc.Close()
		dec := xml.NewDecoder(io.LimitReader(rc, 50<<20))
		var b strings.Builder
		for {
			tok, err := dec.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				return "", err
			}
			switch t := tok.(type) {
			case xml.StartElement:
				switch t.Name.Local {
				case "tab":
					b.WriteByte('\t')
				case "br":
					b.WriteByte('\n')
				}
			case xml.EndElement:
				if t.Name.Local == "p" {
					b.WriteByte('\n')
				}
			case xml.CharData:
				b.Write(t)
			}
			if b.Len() > maxChatChars {
				break
			}
		}
		return strings.TrimSpace(b.String()), nil
	}
	return "", errors.New("no document")
}
