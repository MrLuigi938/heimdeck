package server

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/heimdeck/heimdeck/internal/docker"
	"github.com/heimdeck/heimdeck/internal/integrations"
)

// dockerTile shows the containers of the Docker host Heimdeck is connected to.
// Starting/stopping and the update check are opt-in (settings dockerControl
// and dockerUpdates), because they need write access to Docker or contact
// the image registries.
type dockerTile struct{ s *Server }

func (dockerTile) Def() integrations.Definition {
	return integrations.Definition{
		ID: "docker", Name: "Docker", Icon: "docker", Category: "System",
		Description: "Laufende und gestoppte Container, auf Wunsch Updates und Start/Stopp/Neustart (in den Einstellungen einschalten)",
		HasWidget:   true, HasSearch: true, NoURL: true,
	}
}

func (d dockerTile) options() (control, updates bool) {
	settings, _ := d.s.store.Settings()
	return settings["dockerControl"] == "true", settings["dockerUpdates"] == "true"
}

func (d dockerTile) containers(ctx context.Context, in *integrations.Instance) ([]docker.Container, error) {
	if d.s.docker == nil {
		return nil, in.Errorf("Kein Docker-Socket eingebunden – siehe Anleitung in der docker-compose.yml")
	}
	return d.s.docker.Containers(ctx)
}

func (d dockerTile) Fetch(ctx context.Context, in *integrations.Instance) (*integrations.Widget, error) {
	cts, err := d.containers(ctx, in)
	if err != nil {
		return nil, err
	}
	control, updates := d.options()
	var newer map[string]bool
	if updates {
		newer = d.s.updates.Available(d.s.docker, cts)
	}
	running, unhealthy, upd := 0, 0, 0
	var stopped, outdated []docker.Container
	for _, c := range cts {
		switch {
		case c.State == "running":
			running++
			if strings.Contains(c.Status, "unhealthy") {
				unhealthy++
			}
		case c.State != "created":
			stopped = append(stopped, c)
		}
		if newer[c.Image] {
			upd++
			outdated = append(outdated, c)
		}
	}
	w := &integrations.Widget{Stats: []integrations.Stat{
		{Label: in.T("Laufen"), Value: in.Num(running), Tone: "ok"},
		{Label: in.T("Gestoppt"), Value: in.Num(len(stopped)), Tone: toneIf(len(stopped) > 0, "warn")},
	}}
	if unhealthy > 0 {
		w.Stats = append(w.Stats, integrations.Stat{Label: in.T("Ungesund"), Value: in.Num(unhealthy), Tone: "bad"})
	}
	if updates {
		v := in.Num(upd)
		if !d.s.updates.Checked() {
			v = "…"
		}
		w.Stats = append(w.Stats, integrations.Stat{Label: in.T("Updates"), Value: v, Tone: toneIf(upd > 0, "warn")})
	}
	switch {
	case len(stopped) > 0:
		w.ItemsTitle = in.T("Gestoppt")
		for i, c := range stopped {
			if i == 5 {
				break
			}
			w.Items = append(w.Items, d.item(in, c, control, newer[c.Image]))
		}
	case len(outdated) > 0:
		w.ItemsTitle = in.T("Updates verfügbar")
		for i, c := range outdated {
			if i == 5 {
				break
			}
			w.Items = append(w.Items, d.item(in, c, control, true))
		}
	}
	return w, nil
}

func toneIf(cond bool, tone string) string {
	if cond {
		return tone
	}
	return ""
}

func (d dockerTile) item(in *integrations.Instance, c docker.Container, control, newer bool) integrations.Item {
	it := integrations.Item{Title: c.Name, Subtitle: c.Image + " · " + c.Status}
	switch {
	case newer:
		it.Badge = in.T("Update verfügbar")
	case c.State == "running":
		it.Badge = in.T("Läuft")
	default:
		it.Badge = in.T("Gestoppt")
	}
	if !control {
		return it
	}
	p := func(action string) map[string]string { return map[string]string{"id": c.ID, "action": action} }
	if c.State == "running" {
		it.Actions = []integrations.Action{
			{ID: "container", Label: in.T("Neu starten"), Params: p("restart"), Confirm: in.T("„%s“ neu starten?", c.Name)},
			{ID: "container", Label: in.T("Stoppen"), Tone: "danger", Params: p("stop"), Confirm: in.T("„%s“ stoppen?", c.Name)},
		}
	} else {
		it.Actions = []integrations.Action{{ID: "container", Label: in.T("Starten"), Tone: "primary", Params: p("start")}}
	}
	return it
}

func (d dockerTile) Search(ctx context.Context, in *integrations.Instance, query string) ([]integrations.Item, error) {
	cts, err := d.containers(ctx, in)
	if err != nil {
		return nil, err
	}
	control, updates := d.options()
	var newer map[string]bool
	if updates {
		newer = d.s.updates.Available(d.s.docker, cts)
	}
	q := strings.ToLower(query)
	var hits []docker.Container
	for _, c := range cts {
		if strings.Contains(strings.ToLower(c.Name), q) || strings.Contains(strings.ToLower(c.Image), q) {
			hits = append(hits, c)
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		return strings.HasPrefix(strings.ToLower(hits[i].Name), q) && !strings.HasPrefix(strings.ToLower(hits[j].Name), q)
	})
	out := []integrations.Item{}
	for _, c := range hits {
		out = append(out, d.item(in, c, control, newer[c.Image]))
		if len(out) == 8 {
			break
		}
	}
	return out, nil
}

func (d dockerTile) Action(ctx context.Context, in *integrations.Instance, action string, p map[string]string) error {
	if action != "container" {
		return in.Errorf("unbekannte Aktion %q", action)
	}
	if control, _ := d.options(); !control {
		return errors.New(in.T("Container-Steuerung ist ausgeschaltet – in den Einstellungen unter „Docker“ einschalten"))
	}
	if d.s.docker == nil {
		return in.Errorf("Kein Docker-Socket eingebunden – siehe Anleitung in der docker-compose.yml")
	}
	if err := d.s.docker.Action(ctx, p["id"], p["action"]); err != nil {
		return errors.New(in.T(err.Error()))
	}
	return nil
}
