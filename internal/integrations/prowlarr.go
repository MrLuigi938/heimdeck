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
		Description: "Indexer: wie viele aktiv sind und welche gerade ausfallen, Suchanfragen, Grabs, Fehlerquote, Antwortzeit, verbundene Apps",
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
			{Label: in.T("Indexer aktiv"), Value: fmt.Sprintf("%d / %d", enabled, len(indexers))},
			{Label: in.T("Gestört"), Value: fmt.Sprint(len(status)), Tone: toneIf(len(status) > 0, "bad")},
		},
		ItemsTitle: in.T("Gestörte Indexer"),
	}
	// Totals over all indexers since Prowlarr keeps statistics.
	var ixStats struct {
		Indexers []struct {
			Queries      int     `json:"numberOfQueries"`
			Grabs        int     `json:"numberOfGrabs"`
			Failed       int     `json:"numberOfFailedQueries"`
			ResponseTime float64 `json:"averageResponseTime"`
		} `json:"indexers"`
	}
	if p.get(ctx, in, "/indexerstats", &ixStats) == nil && len(ixStats.Indexers) > 0 {
		queries, grabs, failed := 0, 0, 0
		var rt float64
		rtN := 0
		for _, s := range ixStats.Indexers {
			queries += s.Queries
			grabs += s.Grabs
			failed += s.Failed
			if s.ResponseTime > 0 {
				rt += s.ResponseTime
				rtN++
			}
		}
		w.Stats = append(w.Stats,
			Stat{Label: in.T("Suchanfragen"), Value: in.Num(queries)},
			Stat{Label: in.T("Grabs"), Value: in.Num(grabs)},
			Stat{Label: in.T("Fehlgeschlagen"), Value: in.Num(failed), Tone: toneIf(queries > 0 && float64(failed)/float64(queries) > 0.2, "warn")})
		if rtN > 0 {
			w.Stats = append(w.Stats, Stat{Label: in.T("Antwortzeit"), Value: fmt.Sprintf("%.0f ms", rt/float64(rtN))})
		}
	}
	var apps []struct {
		Name string `json:"name"`
	}
	if p.get(ctx, in, "/applications", &apps) == nil {
		w.Stats = append(w.Stats, Stat{Label: in.T("Verbundene Apps"), Value: fmt.Sprint(len(apps))})
	}
	for _, s := range status {
		sub := ""
		if s.DisabledTill != "" {
			sub = in.T("deaktiviert bis %s", in.Date(s.DisabledTill))
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
