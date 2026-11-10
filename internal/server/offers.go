package server

import (
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/store"
)

// What Heimdeck showed a person: the buttons of a tile (on the dashboard, in
// search results, offered to the AI) and the pictures it handed out. A button
// press is only carried out for a button the tile offers – the request names
// a button, it cannot invent one (any service, any target, any parameters the
// service would accept). The same for pictures of tiles that belong to
// someone else: only the ones the tile itself showed.

const offerTTL = 6 * time.Hour

type offerStore struct {
	mu   sync.Mutex
	m    map[string]time.Time
	adds int
}

var offers = offerStore{m: map[string]time.Time{}}

func paramsKey(p map[string]string) string {
	if len(p) == 0 {
		return "{}"
	}
	b, _ := json.Marshal(p) // map keys come out sorted
	return string(b)
}

func actionKey(user, tile int64, action string, params map[string]string) string {
	return "a\x00" + strconv.FormatInt(user, 10) + "\x00" + strconv.FormatInt(tile, 10) + "\x00" + action + "\x00" + paramsKey(params)
}

func imageKey(user, tile int64, path string) string {
	return "i\x00" + strconv.FormatInt(user, 10) + "\x00" + strconv.FormatInt(tile, 10) + "\x00" + path
}

func (o *offerStore) note(key string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	now := time.Now()
	o.m[key] = now
	if o.adds++; o.adds%2000 == 0 {
		for k, at := range o.m {
			if now.Sub(at) > offerTTL {
				delete(o.m, k)
			}
		}
	}
}

func (o *offerStore) has(key string) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	at, ok := o.m[key]
	return ok && time.Since(at) < offerTTL
}

// noteAction remembers a button shown to the user.
func noteAction(user, tile int64, a integrations.Action) {
	offers.note(actionKey(user, tile, a.ID, a.Params))
}

// noteItems remembers the buttons and pictures of entries shown to the user.
func noteItems(user, tile int64, items []integrations.Item) {
	for _, it := range items {
		for _, a := range it.Actions {
			noteAction(user, tile, a)
		}
		noteImage(user, tile, it.Image)
	}
}

// noteWidget remembers what a widget sent to the user offers.
func noteWidget(user, tile int64, w *integrations.Widget) {
	if w == nil {
		return
	}
	for _, a := range w.Actions {
		noteAction(user, tile, a)
	}
	noteItems(user, tile, w.Items)
}

// noteImage remembers a picture address from Heimdeck's image proxy.
func noteImage(user, tile int64, address string) {
	if !strings.HasPrefix(address, "/api/tiles/") {
		return
	}
	u, err := url.Parse(address)
	if err != nil {
		return
	}
	if p := u.Query().Get("p"); p != "" {
		offers.note(imageKey(user, tile, p))
	}
}

func imageShown(user, tile int64, path string) bool {
	return offers.has(imageKey(user, tile, path))
}

var (
	errNotOffered    = errors.New("Diesen Knopf bietet die Kachel gerade nicht an – bitte die Seite neu laden")
	errButtonChanged = errors.New("Der Knopf der Kachel hat sich geändert – bitte den Workflow prüfen")
)

// checkOffered says whether the tile offers this button to the user: shown
// lately, or on the tile now. label, when given (workflows, proposals), has to
// match the button as it is now – a button list that changed order must not
// make a workflow press a different button.
func (s *Server) checkOffered(user int64, t store.Tile, lang, action string, params map[string]string, label string) error {
	current, found := s.currentButton(t, lang, action, params)
	if found {
		if label != "" && label != current && !strings.HasPrefix(label, current+" · ") {
			return errButtonChanged
		}
		return nil
	}
	if offers.has(actionKey(user, t.ID, action, params)) {
		return nil // e.g. from search results, which are not on the tile
	}
	if label != "" {
		return errButtonChanged
	}
	return errNotOffered
}

// currentButton finds the button on the tile as it is now (values hidden by
// the owner stay hidden) and returns its label as the editors show it.
func (s *Server) currentButton(t store.Tile, lang, action string, params map[string]string) (string, bool) {
	w, pending, err := s.widget(t, lang, 6*time.Second)
	if pending || err != nil || w == nil {
		return "", false
	}
	w = hideParts(w, t.Hide, lang)
	want := paramsKey(params)
	for _, a := range w.Actions {
		if a.ID == action && paramsKey(a.Params) == want {
			return a.Label, true
		}
	}
	for _, it := range w.Items {
		for _, a := range it.Actions {
			if a.ID == action && paramsKey(a.Params) == want {
				return a.Label + " – " + clip(it.Title, 60), true
			}
		}
	}
	return "", false
}
