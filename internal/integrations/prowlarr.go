package integrations

import (
	"context"
	"fmt"
)

type prowlarr struct{}

func init() { register(prowlarr{}) }

func (prowlarr) Def() Definition {
	return Definition{
		ID: "prowlarr", Name: "Prowlarr", Icon: "prowlarr", Category: "Medien",
		Description: "Indexer: wie viele aktiv sind und welche gerade ausfallen",
		Fields:      []Field{apiKeyField}, HasWidget: true, Images: []string{"prowlarr"}, Port: 9696,
	}
}

func (prowlarr) get(ctx context.Context, in *Instance, path string, out any) error {
	return in.do(ctx, request{path: "/api/v1" + path, headers: map[string]string{"X-Api-Key": in.V("apiKey")}}, out)
}

func (p prowlarr) Test(ctx context.Context, in *Instance) error {
	return p.get(ctx, in, "/system/status", nil)
}

func (p prowlarr) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	var indexers []struct {
		ID     int    `json:"id"`
		Name   string `json:"name"`
		Enable bool   `json:"enable"`
	}
	if err := p.get(ctx, in, "/indexer", &indexers); err != nil {
		return nil, err
	}
	var status []struct {
		IndexerID    int    `json:"indexerId"`
		DisabledTill string `json:"disabledTill"`
	}
	p.get(ctx, in, "/indexerstatus", &status)
	var health arrHealth
	p.get(ctx, in, "/health", &health)

	names := map[int]string{}
	enabled := 0
	for _, ix := range indexers {
		names[ix.ID] = ix.Name
		if ix.Enable {
			enabled++
		}
	}
	w := &Widget{
		Stats: []Stat{
			{Label: "Indexer aktiv", Value: fmt.Sprintf("%d / %d", enabled, len(indexers))},
			{Label: "Gestört", Value: fmt.Sprint(len(status)), Tone: toneIf(len(status) > 0, "bad")},
		},
		ItemsTitle: "Gestörte Indexer",
	}
	for _, s := range status {
		sub := ""
		if s.DisabledTill != "" {
			sub = "deaktiviert bis " + shortDate(s.DisabledTill)
		}
		w.Items = append(w.Items, Item{Title: names[s.IndexerID], Subtitle: sub, URL: in.Link("/")})
	}
	for _, h := range health {
		if len(w.Alerts) < 3 {
			w.Alerts = append(w.Alerts, h.Message)
		}
	}
	return w, nil
}
