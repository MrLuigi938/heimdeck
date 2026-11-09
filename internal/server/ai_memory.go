package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heimdeck/heimdeck/internal/i18n"
)

// The AI's memory: short facts each user asked it to remember ("my NAS is
// called tank", "with lights I mean the garden lights"). They go along with
// every question – in the chat window, in Telegram and in reports. The user
// sees and deletes them in the settings; the AI adds one only when asked
// (the remember tool), or the user does with /merken in Telegram.

const (
	memoryKey      = "_aiMemory" // user setting, never sent with the other settings
	maxMemory      = 50
	maxMemoryChars = 300
)

type memoryItem struct {
	ID   string `json:"id"`
	Text string `json:"text"`
	At   int64  `json:"at"`
}

var memoryMu sync.Mutex

func (s *Server) memoryOf(user int64) []memoryItem {
	settings, _ := s.store.UserSettings(user)
	var items []memoryItem
	json.Unmarshal([]byte(settings[memoryKey]), &items)
	return items
}

func (s *Server) saveMemory(user int64, items []memoryItem) error {
	b, _ := json.Marshal(items)
	return s.store.SaveUserSettings(user, map[string]string{memoryKey: string(b)})
}

// addMemory keeps one fact; the same fact twice is kept once.
func (s *Server) addMemory(user int64, text, lang string) (memoryItem, error) {
	text = strings.Join(strings.Fields(text), " ")
	if r := []rune(text); len(r) > maxMemoryChars {
		text = string(r[:maxMemoryChars])
	}
	if text == "" {
		return memoryItem{}, errors.New(i18n.T(lang, "Was soll ich mir merken?"))
	}
	memoryMu.Lock()
	defer memoryMu.Unlock()
	items := s.memoryOf(user)
	for _, it := range items {
		if strings.EqualFold(it.Text, text) {
			return it, nil
		}
	}
	if len(items) >= maxMemory {
		return memoryItem{}, errors.New(i18n.T(lang, "Das Gedächtnis ist voll (%d Punkte) – bitte erst etwas löschen.", maxMemory))
	}
	item := memoryItem{ID: strconv.FormatInt(time.Now().UnixNano(), 36), Text: text, At: time.Now().Unix()}
	if err := s.saveMemory(user, append(items, item)); err != nil {
		return memoryItem{}, err
	}
	return item, nil
}

func (s *Server) forgetMemory(user int64, id string) (memoryItem, bool) {
	memoryMu.Lock()
	defer memoryMu.Unlock()
	items := s.memoryOf(user)
	for i, it := range items {
		if it.ID == id {
			if s.saveMemory(user, append(items[:i:i], items[i+1:]...)) != nil {
				return it, false
			}
			return it, true
		}
	}
	return memoryItem{}, false
}

// memoryPrompt hands the facts to the model.
func memoryPrompt(items []memoryItem) string {
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\nWhat the user asked you to remember (use it where it helps; do not list it unasked):\n")
	for _, it := range items {
		b.WriteString("- " + it.Text + "\n")
	}
	return b.String()
}

// ---- API: the list in the settings ----

func (s *Server) handleGetMemory(w http.ResponseWriter, r *http.Request) {
	items := s.memoryOf(uid(r))
	if items == nil {
		items = []memoryItem{}
	}
	writeJSON(w, items)
}

func (s *Server) handleAddMemory(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Text string `json:"text"`
	}
	if err := readJSON(r, &in); err != nil {
		writeErr(w, 400, err)
		return
	}
	item, err := s.addMemory(uid(r), in.Text, langOf(r))
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, item)
}

func (s *Server) handleDeleteMemory(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.forgetMemory(uid(r), r.PathValue("id")); !ok {
		writeErr(w, 404, errNotFound)
		return
	}
	writeJSON(w, map[string]bool{"ok": true})
}
