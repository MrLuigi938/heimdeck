package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/i18n"
	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/store"
)

// AI chat stage 2: instead of getting the whole dashboard up front, the model
// gets tools and asks for what it needs – the list of tiles, one tile's live
// data, a search through the user's services. Heimdeck runs them with the
// user's rights (the same tiles as 📋: no locked or hidden groups, hidden
// values left out). The model never runs an action: propose_action only puts
// a button into the chat, which the user clicks or not.
//
// The answer streams as plain text as before; between the text, events for
// the chat (a tool was used, an action is proposed) are sent as JSON framed
// by the record separator \x1e and the unit separator \x1f.

const (
	toolRounds     = 6
	toolCallsRound = 5
	toolTileItems  = 25
	toolTileChars  = 4000
	toolSearchHits = 8
	toolSearchMax  = 60
	toolWait       = 6 * time.Second
)

var errNoTools = errors.New("the model does not support tools")

// toolDefs are the tools in the OpenAI function-calling format.
func toolDefs(memory bool) []any {
	fn := func(name, desc string, props map[string]any, required ...string) map[string]any {
		params := map[string]any{"type": "object", "properties": props}
		if len(required) > 0 {
			params["required"] = required
		}
		return map[string]any{"type": "function", "function": map[string]any{"name": name, "description": desc, "parameters": params}}
	}
	str := func(desc string) map[string]any { return map[string]any{"type": "string", "description": desc} }
	defs := []any{
		fn("list_tiles", "List the tiles on the user's Heimdeck dashboard: name, service, group and whether it is reachable. Use it to find out which services the user has.", map[string]any{}),
		fn("read_tile", "Read what one tile shows right now: its live values, lists and warnings, and the actions the user could run on it (each with a key like a1).",
			map[string]any{"name": str("The tile's name as listed, or the service, e.g. \"Sonarr\"")}, "name"),
		fn("search_services", "Search all of the user's services that support search at once (media libraries, requests, documents, photos, smart home …). Results may carry action keys.",
			map[string]any{"query": str("What to search for")}, "query"),
		fn("propose_action", "Propose an action to the user as a button in the chat (e.g. pause downloads, switch a light). You cannot run actions yourself – the user decides by clicking. Only use keys you got from read_tile or search_services in this conversation turn.",
			map[string]any{"action": str("The action key, e.g. a1"), "reason": str("One short sentence why")}, "action"),
	}
	if memory {
		defs = append(defs, fn("remember", "Save a short fact or preference for later conversations (e.g. what a device is called). Only when the user explicitly asks you to remember something (\"remember that …\", \"merk dir …\").",
			map[string]any{"text": str("The fact in one short sentence, in the user's words")}, "text"))
	}
	return defs
}

// proposal is an action offered to the user as a button.
type proposal struct {
	TileID  int64             `json:"tileId"`
	Tile    string            `json:"tile"`
	Action  string            `json:"action"`
	Params  map[string]string `json:"params,omitempty"`
	Label   string            `json:"label"`
	Confirm string            `json:"confirm,omitempty"`
	Tone    string            `json:"tone,omitempty"`
}

// toolRun is the state of one chat request with tools.
type toolRun struct {
	s       *Server
	ctx     context.Context
	lang    string
	tiles   []store.Tile
	groups  map[int64]aiGroup
	access  map[int64]boardAccess
	actions map[string]proposal
	user    int64
	memory  bool // the remember tool may be used
}

func (s *Server) newToolRun(ctx context.Context, r *http.Request) *toolRun {
	tiles, groups := s.aiTiles(r)
	access, _ := s.groupsFor(uid(r))
	return &toolRun{s: s, ctx: ctx, lang: langOf(r), tiles: tiles, groups: groups, access: access, actions: map[string]proposal{}, user: uid(r)}
}

// canAct says whether the user may press this tile's buttons.
func (tr *toolRun) canAct(t store.Tile) bool {
	integ, ok := integrations.Get(t.Integration)
	if !ok {
		return false
	}
	_, ok = integ.(integrations.Actioner)
	return ok && tr.access[t.GroupID].Act && tr.s.dockerAllowed(t)
}

// offer remembers an action under a short key for propose_action.
func (tr *toolRun) offer(t store.Tile, a integrations.Action, about string) string {
	key := "a" + strconv.Itoa(len(tr.actions)+1)
	label := a.Label
	if about != "" {
		label += " – " + about
	}
	tr.actions[key] = proposal{TileID: t.ID, Tile: t.Name, Action: a.ID, Params: a.Params, Label: label, Confirm: a.Confirm, Tone: a.Tone}
	return key
}

// call runs one tool and returns its result for the model and, for the chat,
// a short note of what was done (or a proposal).
func (tr *toolRun) call(name, args string) (string, map[string]any) {
	var in struct {
		Name   string `json:"name"`
		Query  string `json:"query"`
		Action string `json:"action"`
		Reason string `json:"reason"`
		Text   string `json:"text"`
	}
	if args != "" && json.Unmarshal([]byte(args), &in) != nil {
		return "error: the arguments are not valid JSON", nil
	}
	switch name {
	case "list_tiles":
		return tr.listTiles(), map[string]any{"tool": "list"}
	case "read_tile":
		return tr.readTile(in.Name), map[string]any{"tool": "read", "arg": clip(in.Name, 60)}
	case "search_services":
		return tr.search(in.Query), map[string]any{"tool": "search", "arg": clip(in.Query, 80)}
	case "remember":
		if !tr.memory {
			return "error: remembering is not available here", nil
		}
		item, err := tr.s.addMemory(tr.user, in.Text, tr.lang)
		if err != nil {
			return "error: " + err.Error(), nil
		}
		return "Saved for later conversations: " + item.Text, map[string]any{"tool": "remember", "arg": clip(item.Text, 80)}
	case "propose_action":
		p, ok := tr.actions[strings.TrimSpace(in.Action)]
		if !ok {
			return "error: unknown action key – read the tile (read_tile) or search first and use one of the keys listed there", nil
		}
		return "The button \"" + p.Label + "\" (" + p.Tile + ") is now shown to the user. It runs only if they click it – tell them so; do not claim it was done.",
			map[string]any{"propose": p, "reason": clip(in.Reason, 200)}
	}
	return "error: unknown tool " + name, nil
}

func (tr *toolRun) listTiles() string {
	if len(tr.tiles) == 0 {
		return "The dashboard has no tiles."
	}
	var b strings.Builder
	last := int64(-1)
	for i, t := range tr.tiles {
		if i == 150 {
			fmt.Fprintf(&b, "… %d more\n", len(tr.tiles)-i)
			break
		}
		if t.GroupID != last {
			g := tr.groups[t.GroupID]
			fmt.Fprintf(&b, "## %s · %s\n", g.board, g.name)
			last = t.GroupID
		}
		b.WriteString("- " + clip(t.Name, 60))
		if integ, ok := integrations.Get(t.Integration); ok && t.Integration != "link" {
			b.WriteString(" (" + i18n.T(tr.lang, integ.Def().Name) + ")")
		}
		tr.s.mu.Lock()
		st, checked := tr.s.latest[t.ID]
		tr.s.mu.Unlock()
		if checked && !st.Online {
			b.WriteString(" – OFFLINE")
		}
		b.WriteString("\n")
	}
	return b.String()
}

// find returns the tiles that match a name: exact first, then by part of
// the name or by the service.
func (tr *toolRun) find(name string) []store.Tile {
	q := strings.ToLower(strings.TrimSpace(name))
	if q == "" {
		return nil
	}
	var exact, part []store.Tile
	for _, t := range tr.tiles {
		n := strings.ToLower(t.Name)
		switch {
		case n == q:
			exact = append(exact, t)
		case strings.Contains(n, q) || strings.Contains(q, n) || strings.EqualFold(t.Integration, q):
			part = append(part, t)
		}
	}
	if len(exact) > 0 {
		return exact
	}
	if len(part) > 3 {
		part = part[:3]
	}
	return part
}

func (tr *toolRun) readTile(name string) string {
	found := tr.find(name)
	if len(found) == 0 {
		return "No tile called \"" + clip(name, 60) + "\" on the dashboard. Use list_tiles to see the names."
	}
	var b strings.Builder
	for _, t := range found {
		b.WriteString(tr.s.tileDetail(t, tr.lang, toolTileItems, toolTileChars))
		if !tr.canAct(t) {
			continue
		}
		w, pending, err := tr.s.widget(t, tr.lang, toolWait)
		if pending || err != nil || w == nil {
			continue
		}
		w = hideParts(w, t.Hide, tr.lang)
		var acts []string
		for _, a := range w.Actions {
			acts = append(acts, "["+tr.offer(t, a, "")+"] "+a.Label)
		}
		for i, it := range w.Items {
			if i == toolTileItems {
				break
			}
			for _, a := range it.Actions {
				acts = append(acts, "["+tr.offer(t, a, clip(it.Title, 60))+"] "+a.Label+" – "+clip(it.Title, 80))
			}
		}
		if len(acts) > 0 {
			b.WriteString("  actions the user could run: " + strings.Join(acts, "; ") + "\n")
		}
	}
	return b.String()
}

func (tr *toolRun) search(q string) string {
	q = strings.TrimSpace(q)
	if len([]rune(q)) < 2 {
		return "error: the search needs at least two letters"
	}
	ctx, cancel := context.WithTimeout(tr.ctx, 8*time.Second)
	defer cancel()
	groups := tr.s.searchTiles(ctx, tr.lang, tr.tiles, tr.access, clip(q, 200))
	byID := map[int64]store.Tile{}
	for _, t := range tr.tiles {
		byID[t.ID] = t
	}
	var b strings.Builder
	n := 0
	for _, g := range groups {
		if g.Error != "" || len(g.Items) == 0 {
			continue
		}
		t := byID[g.TileID]
		fmt.Fprintf(&b, "## %s\n", g.Name)
		for i, it := range g.Items {
			if i == toolSearchHits || n == toolSearchMax {
				break
			}
			line := "- " + clip(it.Title, 120)
			if it.Subtitle != "" {
				line += " — " + clip(it.Subtitle, 160)
			}
			if it.Badge != "" {
				line += " [" + clip(it.Badge, 40) + "]"
			}
			if tr.canAct(t) {
				for _, a := range it.Actions {
					line += " {action [" + tr.offer(t, a, clip(it.Title, 60)) + "] " + a.Label + "}"
				}
			}
			b.WriteString(line + "\n")
			n++
		}
	}
	if n == 0 {
		return "Nothing found for \"" + clip(q, 80) + "\" in the user's services."
	}
	return b.String()
}

// sendEvent writes an event for the chat between the text.
func sendEvent(w io.Writer, ev map[string]any) {
	b, _ := json.Marshal(ev)
	io.WriteString(w, "\x1e"+string(b)+"\x1f")
}

// toolCall is a tool call as it arrives in pieces in the stream.
type toolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

// chatWithTools runs the conversation with tools and streams the answer. It
// returns errNoTools – before anything was written – when the model or the
// server does not take tools, so the caller can fall back to stage 1.
func (s *Server) chatWithTools(w http.ResponseWriter, r *http.Request, c aiConfig, u store.User, messages []any, extra map[string]any) error {
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Minute)
	defer cancel()
	run := s.newToolRun(ctx, r)
	run.memory = true
	rc := http.NewResponseController(w)
	started := false
	start := time.Now()
	used := 0
	for round := 0; round < toolRounds; round++ {
		body := map[string]any{"model": c.Model, "stream": true, "temperature": 0.3, "messages": messages, "tools": toolDefs(true)}
		if round == toolRounds-1 {
			body["tool_choice"] = "none" // enough looking around – answer now
		}
		for k, v := range extra {
			body[k] = v
		}
		resp, err := c.request(ctx, "POST", "/chat/completions", body)
		if err != nil {
			if !started {
				slog.Warn("KI nicht erreichbar", "fehler", err)
				writeErr(w, 502, errors.New("Die KI ist gerade nicht erreichbar"))
				return nil
			}
			io.WriteString(w, "\n\n⚠ "+tr(r, "Die KI ist gerade nicht erreichbar"))
			return nil
		}
		if resp.StatusCode != 200 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 600))
			resp.Body.Close()
			if !started && round == 0 && resp.StatusCode < 500 && strings.Contains(strings.ToLower(string(b)), "tool") {
				slog.Info("KI kann keine Werkzeuge, nehme die Übersicht", "modell", c.Model, "status", resp.Status)
				return errNoTools
			}
			slog.Warn("KI lehnt ab", "status", resp.Status, "antwort", string(b))
			if !started {
				writeErr(w, 502, fmt.Errorf("Die KI antwortet mit %s – Details im Protokoll", resp.Status))
				return nil
			}
			io.WriteString(w, "\n\n⚠ "+tr(r, "Die KI antwortet mit %s – Details im Protokoll", resp.Status))
			return nil
		}
		if !started {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Set("X-Accel-Buffering", "no")
			w.Header().Set("X-AI-Tools", "1")
			started = true
		}
		text, calls := streamRound(resp.Body, w, rc)
		resp.Body.Close()
		if len(calls) == 0 {
			break
		}
		if len(calls) > toolCallsRound {
			calls = calls[:toolCallsRound]
		}
		assistant := map[string]any{"role": "assistant", "tool_calls": calls}
		if text != "" {
			assistant["content"] = text
		} else {
			assistant["content"] = nil
		}
		messages = append(messages, assistant)
		for _, call := range calls {
			result, ev := run.call(call.Function.Name, call.Function.Arguments)
			used++
			if ev != nil {
				sendEvent(w, ev)
				rc.Flush()
			}
			messages = append(messages, map[string]any{"role": "tool", "tool_call_id": call.ID, "name": call.Function.Name, "content": result})
		}
	}
	slog.Debug("KI-Antwort mit Werkzeugen", "modell", c.Model, "werkzeuge", used, "dauer", time.Since(start).Round(time.Millisecond), "benutzer", u.Username)
	return nil
}

// streamRound passes the text of one answer on as it comes and collects the
// tool calls, which arrive in pieces (by index).
func streamRound(body io.Reader, w io.Writer, rc *http.ResponseController) (string, []toolCall) {
	sc := bufio.NewScanner(body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var text strings.Builder
	calls := map[int]*toolCall{}
	for sc.Scan() {
		data, ok := strings.CutPrefix(strings.TrimSpace(sc.Text()), "data:")
		if !ok {
			continue
		}
		data = strings.TrimSpace(data)
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content   string `json:"content"`
					ToolCalls []struct {
						Index *int `json:"index"`
						toolCall
					} `json:"tool_calls"`
				} `json:"delta"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil || len(chunk.Choices) == 0 {
			continue
		}
		d := chunk.Choices[0].Delta
		if d.Content != "" {
			text.WriteString(d.Content)
			io.WriteString(w, d.Content)
			rc.Flush()
		}
		for i, tc := range d.ToolCalls {
			idx := i
			if tc.Index != nil {
				idx = *tc.Index
			}
			cur := calls[idx]
			if cur == nil {
				cur = &toolCall{Type: "function"}
				calls[idx] = cur
			}
			if tc.ID != "" {
				cur.ID = tc.ID
			}
			if tc.Function.Name != "" {
				cur.Function.Name = tc.Function.Name
			}
			cur.Function.Arguments += tc.Function.Arguments
		}
	}
	idx := make([]int, 0, len(calls))
	for i := range calls {
		idx = append(idx, i)
	}
	sort.Ints(idx)
	out := make([]toolCall, 0, len(idx))
	for _, i := range idx {
		c := calls[i]
		if c.Function.Name == "" {
			continue
		}
		if c.ID == "" {
			c.ID = "call_" + strconv.Itoa(i)
		}
		out = append(out, *c)
	}
	return text.String(), out
}
