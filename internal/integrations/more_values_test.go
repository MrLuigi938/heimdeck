package integrations

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestJellyfinMoreValues(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /Sessions": []obj{
			{"UserName": "Luis", "DeviceName": "TV", "NowPlayingItem": obj{"Name": "Film", "RunTimeTicks": 100}, "PlayState": obj{"PositionTicks": 50, "PlayMethod": "Transcode"}},
			{"UserName": "Anna", "DeviceName": "Handy"},
		},
		"GET /Items/Counts": obj{"MovieCount": 279, "SeriesCount": 129, "EpisodeCount": 2933, "AlbumCount": 12, "SongCount": 0},
		"GET /System/Info":  obj{"Version": "10.11.0", "HasUpdateAvailable": true},
	}, nil)
	w, err := jellyfin{}.Fetch(context.Background(), f.instance(map[string]string{"apiKey": "k"}))
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	if s["Transkodiert"] != "1" || s["Geräte aktiv"] != "2" || s["Alben"] != "12" || s["Titel"] != "" || len(w.Alerts) != 1 || w.Items[0].Badge != "Transkodiert" {
		t.Fatalf("%v %+v %v", s, w.Items, w.Alerts)
	}

	idle := newFake(t, map[string]any{
		"GET /Sessions":     []obj{},
		"GET /Items/Counts": obj{},
		"GET /Items": obj{"Items": []obj{
			{"Id": "1", "Name": "Pilot", "Type": "Episode", "SeriesName": "Serie", "ParentIndexNumber": 1, "IndexNumber": 1, "DateCreated": time.Now().Add(-2 * time.Hour).Format(time.RFC3339Nano)},
			{"Id": "2", "Name": "Neuer Film", "Type": "Movie", "ProductionYear": 2026},
		}},
	}, nil)
	w, _ = jellyfin{}.Fetch(context.Background(), idle.instance(map[string]string{"apiKey": "k"}))
	if w.ItemsTitle != "Neu hinzugefügt" || len(w.Items) != 2 || w.Items[0].Title != "Serie · S01E01" || !strings.Contains(w.Items[0].Subtitle, "vor 2 Std.") || w.Items[1].Subtitle != "Film · 2026" {
		t.Fatalf("latest %+v", w)
	}
}

func TestSabnzbdHistory(t *testing.T) {
	// The queue and history share /api; answer by mode.
	srv := newFakeFunc(t, func(mode string) any {
		if mode == "history" {
			return obj{"history": obj{"day_size": "1.2 G", "week_size": "15.0 G", "slots": []obj{
				{"name": "Kaputt.Release", "status": "Failed", "bytes": 0, "completed": time.Now().Add(-time.Hour).Unix()},
				{"name": "Gut.Release", "status": "Completed", "bytes": 2e9, "completed": time.Now().Add(-3 * time.Hour).Unix()},
			}}}
		}
		return obj{"queue": obj{"paused": false, "kbpersec": "0", "mbleft": "0", "noofslots_total": 0, "slots": []obj{},
			"diskspace1": "120.5", "speedlimit": "50", "have_warnings": "2"}}
	})
	w, err := sabnzbd{}.Fetch(context.Background(), srv.instance(map[string]string{"apiKey": "k"}))
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	if s["Heute geladen"] != "1,2 GB" || s["Diese Woche"] != "15,0 GB" || s["Tempolimit"] != "50 %" || s["Warnungen"] != "2" || s["Freier Speicher"] != "120,5 GB" {
		t.Fatalf("stats %v", s)
	}
	if w.ItemsTitle != "Zuletzt fertig" || len(w.Items) != 2 || w.Items[0].Badge != "Fehlgeschlagen" || len(w.Alerts) != 1 || w.Alerts[0] != "Fehlgeschlagen: Kaputt Release" {
		t.Fatalf("history %+v %v", w.Items, w.Alerts)
	}
}

// newFakeFunc answers every request with the result of respond(mode), for
// APIs like SABnzbd's that use one address and a mode parameter.
func newFakeFunc(t *testing.T, respond func(mode string) any) *fake {
	t.Helper()
	f := &fake{bodies: map[string]string{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(respond(r.URL.Query().Get("mode")))
	}))
	t.Cleanup(f.Close)
	return f
}

func TestArrMoreValues(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /api/v3/queue":          obj{"totalRecords": 0, "records": []obj{}},
		"GET /api/v3/calendar":       []obj{},
		"GET /api/v3/health":         []obj{},
		"GET /api/v3/wanted/missing": obj{"totalRecords": 7},
		"GET /api/v3/movie":          []obj{{"sizeOnDisk": 4e9}, {"sizeOnDisk": 6e9}},
		"GET /api/v3/rootfolder":     []obj{{"path": "/a", "freeSpace": 900e9}, {"path": "/b", "freeSpace": 30e9}},
	}, nil)
	radarr := &arr{movie: true, def: Definition{ID: "radarr-test", Name: "Radarr"}}
	w, err := radarr.Fetch(context.Background(), f.instance(map[string]string{"apiKey": "k"}))
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	if s["Fehlend"] != "7" || s["Filme"] != "2" || !strings.HasPrefix(s["Auf der Platte"], "9") || !strings.HasPrefix(s["Freier Speicher"], "27") {
		t.Fatalf("stats %v", s)
	}
}

func TestProwlarrStats(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /api/v1/indexer":       []obj{{"id": 1, "name": "A", "enable": true}},
		"GET /api/v1/indexerstatus": []obj{},
		"GET /api/v1/health":        []obj{},
		"GET /api/v1/indexerstats": obj{"indexers": []obj{
			{"numberOfQueries": 100, "numberOfGrabs": 10, "numberOfFailedQueries": 30, "averageResponseTime": 200},
			{"numberOfQueries": 50, "numberOfGrabs": 5, "numberOfFailedQueries": 0, "averageResponseTime": 400},
		}},
		"GET /api/v1/applications": []obj{{"name": "Sonarr"}, {"name": "Radarr"}},
	}, nil)
	w, err := prowlarr{}.Fetch(context.Background(), f.instance(map[string]string{"apiKey": "k"}))
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	if s["Suchanfragen"] != "150" || s["Grabs"] != "15" || s["Fehlgeschlagen"] != "30" || s["Antwortzeit"] != "300 ms" || s["Verbundene Apps"] != "2" {
		t.Fatalf("stats %v", s)
	}
}

func TestUniFiGateway(t *testing.T) {
	const site = "88f7af54-98f8-306a-a1c7-c9349722b1f6"
	f := newFake(t, map[string]any{
		"GET /proxy/network/integration/v1/sites":                      obj{"data": []obj{{"id": site, "name": "Default"}}},
		"GET /proxy/network/integration/v1/sites/" + site + "/clients": obj{"totalCount": 20, "data": []obj{}},
		"GET /proxy/network/integration/v1/sites/" + site + "/devices": obj{"data": []obj{
			{"id": "11111111-1111-1111-1111-111111111111", "name": "UDM", "model": "UDM Pro", "state": "ONLINE", "features": []string{"switching", "gateway"}},
			{"id": "22222222-2222-2222-2222-222222222222", "name": "AP Flur", "model": "U7", "state": "ONLINE", "features": []string{"accessPoint"}},
		}},
		"GET /proxy/network/integration/v1/sites/" + site + "/devices/11111111-1111-1111-1111-111111111111/statistics/latest": obj{
			"uptimeSec": 200000, "cpuUtilizationPct": 12.5, "memoryUtilizationPct": 61, "uplink": obj{"txRateBps": 4e6, "rxRateBps": 52.5e6}},
	}, nil)
	w, err := unifi{}.Fetch(context.Background(), f.instance(map[string]string{"apiKey": "k"}))
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	if s["↓ Internet"] != "52,5 Mbit/s" || s["↑ Internet"] != "4,0 Mbit/s" || s["Gateway-CPU"] != "12 %" || s["Gateway-RAM"] != "61 %" || s["LAN"] != "20" {
		t.Fatalf("stats %v", s)
	}
	if w.ItemsTitle != "Geräte" || len(w.Items) != 2 || w.Items[0].Title != "AP Flur" {
		t.Fatalf("devices %+v", w.Items)
	}
}

func TestStashAndMeTube(t *testing.T) {
	f := newFake(t, map[string]any{
		"POST /graphql": obj{"data": obj{"stats": obj{"scene_count": 10, "scenes_size": 5e9, "scenes_duration": 36000, "gallery_count": 3, "performer_count": 4, "studio_count": 2, "tag_count": 9, "total_play_count": 21, "scenes_played": 6}}},
		"GET /history": obj{
			"queue":   []obj{{"title": "Video A", "status": "downloading", "percent": 40, "speed": 2e6, "eta": 90}},
			"pending": []obj{},
			"done":    []obj{{"title": "Alt", "status": "finished"}, {"title": "Kaputt", "status": "error", "msg": "Video unavailable"}},
		},
	}, nil)
	w, err := stash{}.Fetch(context.Background(), f.instance(map[string]string{}))
	if err != nil {
		t.Fatal(err)
	}
	if s := stats(w); s["Laufzeit"] != "10 Std." || s["Galerien"] != "3" || s["Wiedergaben"] != "21" || s["Gesehene Szenen"] != "6" {
		t.Fatalf("stash %v", s)
	}
	w, err = metube{}.Fetch(context.Background(), f.instance(map[string]string{}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(w.Items[0].Subtitle, "/s") || !strings.Contains(w.Items[0].Subtitle, "noch") || len(w.Alerts) != 1 || w.Alerts[0] != "Kaputt: Video unavailable" {
		t.Fatalf("metube %+v %v", w.Items, w.Alerts)
	}
}
