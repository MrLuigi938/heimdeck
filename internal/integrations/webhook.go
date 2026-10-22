package integrations

import (
	"context"
	"crypto/subtle"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Webhook: buttons on the tile call addresses of your choice (a Shelly action,
// a Home Assistant or Node-RED webhook, IFTTT …), and the tile has an address
// of its own where scripts and services can post messages, which it lists.

type webhook struct{}

func init() { register(webhook{}) }

func (webhook) Def() Definition {
	return Definition{
		ID: "webhook", Name: "Webhook", Icon: "📨", Category: "Allgemein",
		Description: "Knöpfe, die Webhooks aufrufen (z. B. Shelly, Home Assistant, Node-RED, IFTTT), und eine eigene Adresse, an die Dienste Meldungen schicken",
		NoURL:       true,
		Setup: []string{
			"Knöpfe: pro Zeile ein Knopf in der Form „Name = Adresse“, z. B. „Licht an = http://192.168.1.50/relay/0?turn=on“. Ohne Angabe wird GET verwendet; für POST „Name = POST Adresse“, ein Inhalt (z. B. JSON) folgt nach „|“.",
			"Meldungen empfangen: Heimdeck zeigt unten die Adresse dieser Kachel. Dorthin schicken andere Dienste ihre Meldungen per POST (JSON mit „title“ und „message“, Formular oder einfacher Text) oder per GET mit ?title=…&message=… – die letzten erscheinen auf der Kachel.",
			"Die Adresse enthält einen geheimen Schlüssel – wer sie kennt, kann Meldungen schicken. „Neu erzeugen“ macht die alte ungültig. Empfangene Meldungen bleiben bis zum nächsten Neustart von Heimdeck.",
		},
		Fields: []Field{
			{Key: "actions", Label: "Knöpfe", Type: "textarea",
				Help: "Pro Zeile: Name = Adresse, z. B. Licht an = http://192.168.1.50/relay/0?turn=on oder Szene = POST https://ha.local/api/webhook/abc | {\"scene\":\"kino\"}"},
			{Key: "hookKey", Label: "Adresse für eingehende Meldungen", Type: "hook"},
		},
		HasWidget: true,
	}
}

// hookKeyRe is the shape of the key the editor creates (and any long random string).
var hookKeyRe = regexp.MustCompile(`^[A-Za-z0-9_-]{16,64}$`)

type hookButton struct {
	Name, Method, URL, Body string
}

// hookButtons reads "Name = [METHOD] URL [| body]" lines.
func hookButtons(list string) []hookButton {
	var out []hookButton
	for _, line := range strings.Split(list, "\n") {
		name, rest, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		b := hookButton{Name: strings.TrimSpace(name), Method: http.MethodGet}
		rest, b.Body, _ = strings.Cut(rest, "|")
		rest, b.Body = strings.TrimSpace(rest), strings.TrimSpace(b.Body)
		if m, u, ok := strings.Cut(rest, " "); ok {
			switch strings.ToUpper(m) {
			case "GET", "POST", "PUT", "PATCH", "DELETE":
				b.Method, rest = strings.ToUpper(m), strings.TrimSpace(u)
			}
		}
		if p, err := url.Parse(rest); err == nil && (p.Scheme == "http" || p.Scheme == "https") && p.Host != "" && b.Name != "" {
			b.URL = rest
			out = append(out, b)
		}
	}
	return out
}

// HookMessage is a message posted to a tile's webhook address.
type HookMessage struct {
	At    time.Time
	Title string
	Text  string
	Value string
	Tone  string // ok | warn | bad
}

const hookKeep = 20

var hookInbox = struct {
	sync.Mutex
	byKey map[string][]HookMessage
}{byKey: map[string][]HookMessage{}}

// ReceiveHook stores a message for the tile with this key (newest first).
func ReceiveHook(key string, m HookMessage) {
	hookInbox.Lock()
	defer hookInbox.Unlock()
	list := append([]HookMessage{m}, hookInbox.byKey[key]...)
	hookInbox.byKey[key] = list[:min(len(list), hookKeep)]
}

func hookMessages(key string) []HookMessage {
	hookInbox.Lock()
	defer hookInbox.Unlock()
	return append([]HookMessage(nil), hookInbox.byKey[key]...)
}

// HookKeyMatches compares a stored key with the one in a request in constant time.
func HookKeyMatches(stored, given string) bool {
	return hookKeyRe.MatchString(stored) && subtle.ConstantTimeCompare([]byte(stored), []byte(given)) == 1
}

// HookTone guesses the tone from a status word such as "down", "error" or "ok".
func HookTone(status string) string {
	s := strings.ToLower(status)
	switch {
	case s == "":
		return ""
	case strings.Contains(s, "err") || strings.Contains(s, "fail") || strings.Contains(s, "down") || strings.Contains(s, "crit") || s == "0" || s == "bad":
		return "bad"
	case strings.Contains(s, "warn"):
		return "warn"
	case strings.Contains(s, "ok") || strings.Contains(s, "up") || strings.Contains(s, "success") || s == "1" || s == "good":
		return "ok"
	}
	return ""
}

func (webhook) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	w := &Widget{}
	for i, b := range hookButtons(in.Values["actions"]) {
		w.Actions = append(w.Actions, Action{ID: "call", Label: b.Name, Params: map[string]string{"n": strconv.Itoa(i)}})
	}
	key := in.V("hookKey")
	if !hookKeyRe.MatchString(key) {
		return w, nil
	}
	msgs := hookMessages(key)
	today := 0
	for _, m := range msgs {
		if time.Since(m.At) < 24*time.Hour {
			today++
		}
	}
	last := "–"
	if len(msgs) > 0 {
		last = in.Since(msgs[0].At)
	}
	w.Stats = []Stat{{Label: in.T("Meldungen (24 Std.)"), Value: in.Num(today)}, {Label: in.T("Letzte"), Value: last}}
	for _, m := range msgs[:min(5, len(msgs))] {
		it := Item{Title: firstNonEmpty(m.Title, m.Text, in.T("Meldung")), Subtitle: in.Since(m.At), Badge: m.Value}
		if m.Title != "" && m.Text != "" {
			it.Subtitle = m.Text + " · " + it.Subtitle
		}
		w.Items = append(w.Items, it)
		if m.Tone == "bad" && time.Since(m.At) < time.Hour {
			w.Alerts = append(w.Alerts, firstNonEmpty(m.Title, m.Text))
		}
	}
	if len(w.Items) > 0 {
		w.ItemsTitle = in.T("Letzte Meldungen")
	}
	return w, nil
}

// Action calls the webhook of the n-th button.
func (webhook) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	buttons := hookButtons(in.Values["actions"])
	n, err := strconv.Atoi(p["n"])
	if action != "call" || err != nil || n < 0 || n >= len(buttons) {
		return in.Errorf("unbekannte Aktion %q", action)
	}
	b := buttons[n]
	var body io.Reader
	if b.Body != "" {
		body = strings.NewReader(b.Body)
	}
	req, err := http.NewRequestWithContext(ctx, b.Method, b.URL, body)
	if err != nil {
		return err
	}
	if b.Body != "" {
		ct := "text/plain; charset=utf-8"
		if strings.HasPrefix(b.Body, "{") || strings.HasPrefix(b.Body, "[") {
			ct = "application/json"
		}
		req.Header.Set("Content-Type", ct)
	}
	resp, err := in.Client.Do(req)
	if err != nil {
		return friendlyNetErr(in.Lang, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode >= 300 {
		return in.Errorf("„%s“: %s", b.Name, fmt.Sprint((&HTTPError{Status: resp.StatusCode, Lang: in.Lang}).Error()))
	}
	return nil
}
