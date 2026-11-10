package server

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/i18n"
	"github.com/heimdeck/heimdeck/internal/logging"
)

// Workflows from Telegram: /workflow with a sentence has the AI draft one,
// the bot shows what it would do (and a dry run) and saves it only on a tap;
// /automationen lists them to switch on and off or to change with a sentence.
// It works with the same checks as the editor – the bot's request has no
// unlocked groups – and in a group only for its admins.

const (
	tgDraftTTL = 30 * time.Minute
	tgEditTTL  = 10 * time.Minute
)

// tgDraft is a workflow the AI drafted, until it is saved or dropped.
type tgDraft struct {
	r    rule
	chat int64
	user int64
	at   time.Time
}

// tgEdit: the chat waits for a sentence that changes a workflow.
type tgEdit struct {
	id   string
	from int64 // the Telegram account that asked
	at   time.Time
}

// workflowCmd handles /workflow sentence.
func (b *tgBot) workflowCmd(ctx context.Context, m tgMessage, user int64, lang, text string) {
	if strings.TrimSpace(text) == "" {
		b.reply(ctx, m, i18n.T(lang, "Beschreib den Workflow in einem Satz, z. B.:\n/workflow Wenn der NAS über 90 % voll ist, schick mir eine Nachricht\n\nMit /automationen siehst du deine Workflows und kannst sie ändern."), "", nil)
		return
	}
	go b.draftWorkflow(ctx, m, user, lang, text, nil)
}

// draftWorkflow asks the AI for a draft and offers it with buttons.
func (b *tgBot) draftWorkflow(ctx context.Context, m tgMessage, user int64, lang, text string, current *rule) {
	defer logging.Recover("Telegram-Bot")
	u, err := b.s.store.User(user)
	if err != nil || b.s.aiFor(u) == nil {
		b.reply(ctx, m, i18n.T(lang, "Die KI ist für dein Konto nicht eingerichtet"), "", nil)
		return
	}
	stop := b.typing(ctx, m.Chat.ID)
	dctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	draft, problem, err := b.s.draftRule(b.s.botRequest(dctx, user, lang), u, text, current)
	var dry ruleLogEntry
	if err == nil && problem == "" {
		dry = b.s.runRule(dctx, user, draft, "", runDry)
	}
	cancel()
	stop()
	if err != nil {
		b.reply(ctx, m, "⚠ "+err.Error(), "", nil)
		return
	}
	var out strings.Builder
	head := i18n.T(lang, "Entwurf")
	if current != nil {
		head = i18n.T(lang, "Geändert")
	}
	out.WriteString("📝 " + head + ": " + firstNonEmptyStr(draft.Name, "…") + "\n\n" + describeRule(lang, draft))
	if problem != "" {
		out.WriteString("\n\n⚠ " + i18n.T(lang, problem) + "\n" + i18n.T(lang, "So kann ich ihn nicht speichern – beschreib es bitte anders oder bau ihn im Editor fertig."))
		b.reply(ctx, m, out.String(), "", nil)
		return
	}
	if dry.Result != "" {
		out.WriteString("\n\n🧪 " + i18n.T(lang, "Probelauf jetzt:") + "\n" + dry.Result)
	}
	if draft.watches() {
		out.WriteString("\n\n" + i18n.T(lang, "Der KI-Wächter fragt die KI regelmäßig – bei Cloud-Modellen kostet das jedes Mal."))
	}
	b.mu.Lock()
	b.seq++
	id := fmt.Sprintf("d%d", b.seq)
	for k, d := range b.drafts {
		if time.Since(d.at) > tgDraftTTL {
			delete(b.drafts, k)
		}
	}
	b.drafts[id] = &tgDraft{r: draft, chat: m.Chat.ID, user: user, at: time.Now()}
	b.mu.Unlock()
	save := "✅ " + i18n.T(lang, "Speichern")
	if current != nil {
		save = "✅ " + i18n.T(lang, "Änderung speichern")
	}
	keyboard := [][]map[string]string{
		{{"text": save, "callback_data": "ws:" + id}},
		{{"text": "💤 " + i18n.T(lang, "Ausgeschaltet speichern"), "callback_data": "wo:" + id}, {"text": "✖ " + i18n.T(lang, "Verwerfen"), "callback_data": "wx:" + id}},
	}
	b.reply(ctx, m, out.String(), "", keyboard)
}

// workflowList shows the workflows with buttons to switch and change them.
func (b *tgBot) workflowList(ctx context.Context, m tgMessage, user int64, lang string) {
	rules := b.s.rulesOf(user)
	if len(rules) == 0 {
		b.reply(ctx, m, i18n.T(lang, "Noch keine Workflows. Leg einen an mit /workflow und einem Satz, z. B. „Jeden Morgen um 7 den Lagebericht“."), "", nil)
		return
	}
	text, keyboard := workflowListView(lang, rules)
	b.reply(ctx, m, text, "", keyboard)
}

func workflowListView(lang string, rules []rule) (string, [][]map[string]string) {
	var out strings.Builder
	out.WriteString("⚙️ " + i18n.T(lang, "Deine Workflows:") + "\n")
	var keyboard [][]map[string]string
	for i, r := range rules {
		if i == 30 {
			break
		}
		mark, toggle := "⏸", "▶ "+i18n.T(lang, "Einschalten")
		if r.On {
			mark, toggle = "▶", "⏸ "+i18n.T(lang, "Ausschalten")
		}
		var trs []string
		for _, tr := range r.Triggers {
			trs = append(trs, triggerLine(lang, tr))
		}
		fmt.Fprintf(&out, "\n%s %s\n    %s", mark, r.Name, clip(strings.Join(trs, " · "+i18n.T(lang, "oder")+" "), 150))
		keyboard = append(keyboard, []map[string]string{
			{"text": clip(r.Name, 28) + ": " + toggle, "callback_data": "wt:" + r.ID},
			{"text": "✏ " + i18n.T(lang, "Ändern"), "callback_data": "we:" + r.ID},
		})
	}
	out.WriteString("\n\n" + i18n.T(lang, "Neu: /workflow und ein Satz. Ändern: ✏ tippen und dann schreiben, was anders sein soll."))
	return out.String(), keyboard
}

// takeEdit returns the workflow a message is meant to change, if the chat
// waits for one from this person.
func (b *tgBot) takeEdit(chat, from int64) (string, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	e := b.edits[chat]
	if e == nil || e.from != from || time.Since(e.at) > tgEditTTL {
		return "", false
	}
	delete(b.edits, chat)
	return e.id, true
}

// workflowButton handles the buttons of drafts and of the list.
func (b *tgBot) workflowButton(ctx context.Context, q tgCallback, kind, id string, owner int64, lang string, answer func(string)) {
	chat := q.Message.Chat.ID
	if q.Message.Chat.Type != "private" && !b.groupAdmin(ctx, chat, q.From.ID) {
		answer(i18n.T(lang, "Das dürfen in dieser Gruppe nur Admins."))
		return
	}
	clearButtons := func() {
		tgCall(ctx, b.token, "editMessageReplyMarkup", map[string]any{"chat_id": chat, "message_id": q.Message.MessageID, "reply_markup": map[string]any{"inline_keyboard": []any{}}}, nil)
	}
	switch kind {
	case "ws", "wo", "wx":
		b.mu.Lock()
		d := b.drafts[id]
		if d == nil || d.chat != chat || d.user != owner || time.Since(d.at) > tgDraftTTL {
			b.mu.Unlock()
			answer(i18n.T(lang, "Dieser Entwurf ist abgelaufen – bitte nochmal beschreiben."))
			return
		}
		delete(b.drafts, id) // each draft is used once
		b.mu.Unlock()
		clearButtons()
		if kind == "wx" {
			answer(i18n.T(lang, "Verworfen"))
			b.send(ctx, chat, "✖ "+i18n.T(lang, "Verworfen: %s", d.r.Name), nil)
			return
		}
		answer("⏳")
		ru := d.r
		if kind == "wo" {
			ru.On = false
		}
		// Checked again: a tile or a share may have changed since the draft.
		if err := b.s.checkRule(b.s.botRequest(ctx, owner, lang), &ru); err != nil {
			b.send(ctx, chat, "⚠ "+i18n.T(lang, err.Error()), nil)
			return
		}
		saved, err := b.s.putRule(owner, ru)
		if err != nil {
			b.send(ctx, chat, "⚠ "+i18n.T(lang, err.Error()), nil)
			return
		}
		slog.Info("Automation gespeichert", "regel", saved.Name, "auslöser", len(saved.Triggers), "schritte", len(saved.Steps), "benutzer", b.s.usernameOf(owner), "über", "Telegram")
		msg := "✅ " + i18n.T(lang, "Gespeichert: %s", saved.Name)
		if !saved.On {
			msg = "💤 " + i18n.T(lang, "Ausgeschaltet gespeichert: %s – einschalten mit /automationen oder in den Einstellungen.", saved.Name)
		}
		b.send(ctx, chat, msg, nil)
	case "wt":
		rules := b.s.rulesOf(owner)
		i := slices.IndexFunc(rules, func(r rule) bool { return r.ID == id })
		if i < 0 {
			answer(i18n.T(lang, "Diesen Workflow gibt es nicht mehr"))
			return
		}
		ru := rules[i]
		ru.On = !ru.On
		if _, err := b.s.putRule(owner, ru); err != nil {
			answer("⚠ " + i18n.T(lang, err.Error()))
			return
		}
		if ru.On {
			answer("▶ " + i18n.T(lang, "%s ist an", ru.Name))
		} else {
			answer("⏸ " + i18n.T(lang, "%s ist aus", ru.Name))
		}
		text, keyboard := workflowListView(lang, b.s.rulesOf(owner))
		tgCall(ctx, b.token, "editMessageText", map[string]any{"chat_id": chat, "message_id": q.Message.MessageID, "text": text, "reply_markup": map[string]any{"inline_keyboard": keyboard}}, nil)
	case "we":
		rules := b.s.rulesOf(owner)
		i := slices.IndexFunc(rules, func(r rule) bool { return r.ID == id })
		if i < 0 {
			answer(i18n.T(lang, "Diesen Workflow gibt es nicht mehr"))
			return
		}
		b.mu.Lock()
		b.edits[chat] = &tgEdit{id: id, from: q.From.ID, at: time.Now()}
		b.mu.Unlock()
		answer("✏")
		hint := i18n.T(lang, "Was soll an „%s“ anders sein? Schreib es in einem Satz.", rules[i].Name)
		if q.Message.Chat.Type != "private" {
			hint += " " + i18n.T(lang, "(In der Gruppe bitte als Antwort auf diese Nachricht.)")
		}
		b.send(ctx, chat, "✏ "+describeRule(lang, rules[i])+"\n\n"+hint, nil)
	}
}

// changeWorkflow drafts a change of a stored workflow from a sentence.
func (b *tgBot) changeWorkflow(ctx context.Context, m tgMessage, user int64, lang, id, text string) {
	for _, r := range b.s.rulesOf(user) {
		if r.ID == id {
			go b.draftWorkflow(ctx, m, user, lang, text, &r)
			return
		}
	}
	b.reply(ctx, m, i18n.T(lang, "Diesen Workflow gibt es nicht mehr"), "", nil)
}

// ---- a workflow in words ----

var dayShort = map[string][]string{
	i18n.DE: {"So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"},
	i18n.EN: {"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"},
}

func daysText(lang string, days []int) string {
	if len(days) == 0 || len(days) == 7 {
		return ""
	}
	var out []string
	for _, d := range []int{1, 2, 3, 4, 5, 6, 0} {
		if slices.Contains(days, d) {
			out = append(out, dayShort[lang][d])
		}
	}
	return strings.Join(out, ", ")
}

func opText(op string) string {
	if op == "<" || op == "=" {
		return op
	}
	return ">"
}

func triggerLine(lang string, tr ruleTrigger) string {
	switch tr.Kind {
	case "time":
		if d := daysText(lang, tr.Days); d != "" {
			return "🕑 " + i18n.T(lang, "%s um %s", d, tr.Time)
		}
		return "🕑 " + i18n.T(lang, "Täglich um %s", tr.Time)
	case "down":
		return "🔴 " + i18n.T(lang, "%s antwortet %d Min. nicht", tr.Name, max(tr.For, 1))
	case "up":
		return "🟢 " + i18n.T(lang, "%s ist wieder erreichbar", tr.Name)
	case "value":
		return "📈 " + tr.Name + " · " + tr.Stat + " " + opText(tr.Op) + " " + tr.Value
	case "camera":
		return "📹 " + tr.Name + ": " + i18n.T(lang, firstNonEmptyStr(tr.Event, "jedes Ereignis"))
	case "item":
		if tr.Gone {
			return "📋 " + i18n.T(lang, "%s: ein Eintrag ist weg", tr.Name)
		}
		return "📋 " + i18n.T(lang, "%s: neuer Eintrag", tr.Name)
	case "webhook":
		if tr.Match != "" {
			return "📨 " + tr.Name + " „" + tr.Match + "“"
		}
		return "📨 " + tr.Name
	case "ai":
		return "🤖 " + i18n.T(lang, "KI-Wächter alle %d Min.: „%s“", tr.Every, clip(tr.Prompt, 120))
	case "manual":
		return "🎬 " + i18n.T(lang, "Von Hand gestartet (Szene)")
	}
	return "⚡ " + tr.Kind
}

func condLine(lang string, c ruleCond) string {
	switch c.Kind {
	case "online":
		return i18n.T(lang, "%s ist erreichbar", c.Name)
	case "offline":
		return i18n.T(lang, "%s ist nicht erreichbar", c.Name)
	}
	return c.Name + " · " + c.Stat + " " + opText(c.Op) + " " + c.Value
}

func stepLines(lang string, steps []ruleAction, indent string, out *[]string) {
	for _, a := range steps {
		switch a.Kind {
		case "button":
			*out = append(*out, indent+"▶ "+a.Label)
		case "scene":
			*out = append(*out, indent+"🎬 "+firstNonEmptyStr(a.Label, a.Scene))
		case "wait":
			*out = append(*out, indent+"⏳ "+i18n.T(lang, "%d Min. warten", a.Minutes))
		case "http":
			*out = append(*out, indent+"🌐 "+a.Method+" "+hostOf(a.URL))
		case "if":
			if a.If == nil {
				continue
			}
			*out = append(*out, indent+"❓ "+i18n.T(lang, "Wenn %s:", condLine(lang, *a.If)))
			stepLines(lang, a.Then, indent+"    ", out)
			if len(a.Else) > 0 {
				*out = append(*out, indent+"   "+i18n.T(lang, "sonst:"))
				stepLines(lang, a.Else, indent+"    ", out)
			}
		default:
			line := "✉ " + clip(firstNonEmptyStr(a.Text, i18n.T(lang, "Nachricht")), 200)
			if a.Report {
				line = "📋 " + i18n.T(lang, "Lagebericht")
				if a.Text != "" {
					line = "✉ " + clip(a.Text, 200) + " + 📋"
				}
			}
			if a.Camera != 0 {
				line += " + 📹"
			}
			*out = append(*out, indent+line)
		}
	}
}

// describeRule says in a few lines what a workflow does.
func describeRule(lang string, r rule) string {
	var lines []string
	var trs []string
	for _, tr := range r.Triggers {
		trs = append(trs, triggerLine(lang, tr))
	}
	join := i18n.T(lang, "oder")
	if r.All {
		join = i18n.T(lang, "und")
	}
	lines = append(lines, i18n.T(lang, "Wenn:")+" "+strings.Join(trs, " "+join+" "))
	if r.Only.From != "" || len(r.Only.Days) > 0 {
		var only []string
		if r.Only.From != "" {
			only = append(only, r.Only.From+"–"+r.Only.To)
		}
		if d := daysText(lang, r.Only.Days); d != "" {
			only = append(only, d)
		}
		lines = append(lines, i18n.T(lang, "Nur:")+" "+strings.Join(only, ", "))
	}
	for _, c := range r.Conds {
		lines = append(lines, i18n.T(lang, "Und:")+" "+condLine(lang, c))
	}
	lines = append(lines, i18n.T(lang, "Dann:"))
	stepLines(lang, r.Steps, "  ", &lines)
	if r.NoAsk {
		lines = append(lines, i18n.T(lang, "Knöpfe ohne Nachfrage"))
	}
	return strings.Join(lines, "\n")
}
