package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/store"
)

// partsDemo returns translated labels like real integrations do.
type partsDemo struct{}

func (partsDemo) Def() integrations.Definition {
	return integrations.Definition{ID: "test-parts", Name: "Parts", HasWidget: true, NoURL: true}
}
func (partsDemo) Fetch(_ context.Context, in *integrations.Instance) (*integrations.Widget, error) {
	return &integrations.Widget{
		Stats:      []integrations.Stat{{Label: in.T("Leistung"), Value: "5 W"}, {Label: in.T("Temperatur"), Value: "21 °C"}, {Label: "Eigener Name", Value: "1"}},
		ItemsTitle: in.T("Letzte Anrufe"), Items: []integrations.Item{{Title: "Mama"}},
		Alerts:  []string{"!"},
		Actions: []integrations.Action{{ID: "x", Label: "X"}},
	}, nil
}

func TestTileParts(t *testing.T) {
	s := testServer(t)
	integrations.Register(partsDemo{})
	g, _ := s.store.CreateGroup("A", 0)

	// The choice lists German keys, whatever language the editor uses.
	req := httptest.NewRequest("POST", "/api/tiles/parts", strings.NewReader(fmt.Sprintf(`{"name":"P","integration":"test-parts","groupId":%d}`, g.ID)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Lang", "en")
	rec := httptest.NewRecorder()
	withLang(http.HandlerFunc(s.handleTileParts)).ServeHTTP(rec, req)
	var res struct {
		Parts []tilePart `json:"parts"`
	}
	json.Unmarshal(rec.Body.Bytes(), &res)
	keys := []string{}
	for _, p := range res.Parts {
		keys = append(keys, p.Key+"="+p.Label)
	}
	if got := strings.Join(keys, ","); got != "Leistung=Power,Temperatur=Temperature,Eigener Name=Eigener Name,#items=Latest calls,#alerts=Notes and warnings,#actions=Buttons" {
		t.Fatalf("parts %s", got)
	}

	// Hidden in German, hidden in English too.
	tile := store.Tile{GroupID: g.ID, Name: "P", Integration: "test-parts", Hide: []string{"Temperatur", "#items", "#actions"}}
	s.store.SaveTile(&tile)
	for _, lang := range []string{"de", "en"} {
		w, _ := s.safeFetch(context.Background(), tile, lang)
		out := hideParts(w, tile.Hide, lang)
		if len(out.Stats) != 2 || out.Stats[1].Label != "Eigener Name" || out.Items != nil || out.Actions != nil || len(out.Alerts) != 1 {
			t.Fatalf("%s: %+v", lang, out)
		}
		if len(w.Stats) != 3 || len(w.Items) != 1 {
			t.Fatal("the cached widget was changed")
		}
	}
	saved, _ := s.store.Tile(tile.ID)
	if strings.Join(saved.Hide, ",") != "Temperatur,#items,#actions" {
		t.Fatalf("stored %v", saved.Hide)
	}
}
