package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/heimdeck/heimdeck/internal/integrations"
)

// sceneTile is a button on the dashboard that starts a scene of the
// automations. The scene belongs to the tile's owner and runs with their
// rights – like every other button of a shared dashboard, it runs with the
// owner's services and keys; the dashboard's sharing decides who may press it.
type sceneTile struct{ s *Server }

func (sceneTile) Def() integrations.Definition {
	return integrations.Definition{
		ID: "scene", Name: "Szene", Icon: "🎬", Category: "Allgemein",
		Description: "Ein Knopf, der eine Szene aus den Automationen startet – z. B. „Filmabend“: Downloads pausieren, Licht dimmen",
		HasWidget:   true, NoURL: true,
		Fields: []integrations.Field{{Key: "scene", Label: "Name der Szene", Type: "text", Required: true,
			Help: "So wie unter Einstellungen → Automationen angelegt (Auslöser „nur von Hand – eine Szene“)."}},
	}
}

func (t sceneTile) scene(in *integrations.Instance) (rule, int64, error) {
	tile, err := t.s.store.Tile(in.TileID)
	if err != nil {
		return rule{}, 0, in.Errorf("nicht gefunden")
	}
	owner := t.s.tileOwner(tile)
	name := strings.TrimSpace(in.V("scene"))
	r, ok := t.s.sceneOf(owner, name)
	if !ok {
		return rule{}, owner, in.Errorf("Keine Szene „%s“ – unter Einstellungen → Automationen anlegen", name)
	}
	return r, owner, nil
}

func (t sceneTile) Fetch(ctx context.Context, in *integrations.Instance) (*integrations.Widget, error) {
	r, owner, err := t.scene(in)
	if err != nil {
		return nil, err
	}
	last := "–"
	for _, e := range t.s.ruleLog(owner) {
		if e.Rule == r.ID {
			last = in.Date(time.Unix(e.At, 0).Format(time.RFC3339))
			break
		}
	}
	var steps []integrations.Item
	for _, a := range r.Steps {
		switch a.Kind {
		case "button":
			steps = append(steps, integrations.Item{Title: "▶ " + a.Label})
		case "scene":
			steps = append(steps, integrations.Item{Title: "🎬 " + a.Label})
		case "wait":
			steps = append(steps, integrations.Item{Title: "⏳ " + in.T("%d Min. warten", a.Minutes)})
		case "if":
			steps = append(steps, integrations.Item{Title: "❓ " + in.T("Wenn …") + " " + firstNonEmptyStr(a.If.Name, "")})
		default:
			steps = append(steps, integrations.Item{Title: "✉ " + firstNonEmptyStr(a.Text, in.T("Nachricht"))})
		}
	}
	w := &integrations.Widget{Stats: []integrations.Stat{{Label: in.T("Zuletzt"), Value: last}}, Items: steps,
		Actions: []integrations.Action{{ID: "run", Label: "🎬 " + in.T("Starten"), Tone: "primary", Confirm: in.T("Die Szene „%s“ starten?", r.Name)}}}
	if !r.On {
		w.Alerts = append(w.Alerts, in.T("Die Szene ist ausgeschaltet"))
		w.Actions = nil
	}
	return w, nil
}

func (t sceneTile) Action(ctx context.Context, in *integrations.Instance, action string, _ map[string]string) error {
	if action != "run" {
		return in.Errorf("unbekannte Aktion")
	}
	r, owner, err := t.scene(in)
	if err != nil {
		return err
	}
	if !r.On {
		return in.Errorf("Die Szene ist ausgeschaltet")
	}
	e := t.s.startScene(ctx, owner, r, "Kachel")
	if !e.OK {
		return errors.New(e.Result)
	}
	return nil
}
