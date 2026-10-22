package integrations

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// fake serves canned responses keyed by "METHOD /path" and records request bodies.
type fake struct {
	*httptest.Server
	mu     sync.Mutex
	bodies map[string]string
	auth   func(r *http.Request) bool
}

func newFake(t *testing.T, routes map[string]any, auth func(r *http.Request) bool) *fake {
	t.Helper()
	f := &fake{bodies: map[string]string{}, auth: auth}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if f.auth != nil && !f.auth(r) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		key := r.Method + " " + r.URL.Path
		b, _ := io.ReadAll(r.Body)
		f.mu.Lock()
		f.bodies[key] = string(b)
		f.mu.Unlock()
		resp, ok := routes[key]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch v := resp.(type) {
		case string:
			w.Header().Set("Content-Type", "text/plain")
			io.WriteString(w, v)
		case nil:
			w.WriteHeader(http.StatusNoContent)
		default:
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(v)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func (f *fake) body(key string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bodies[key]
}

func (f *fake) instance(values map[string]string) *Instance {
	return &Instance{BaseURL: f.URL, PublicURL: "https://svc.example.com", Values: values, Client: f.Client(), Lang: "de", TileID: 7}
}

func stats(w *Widget) map[string]string {
	m := map[string]string{}
	for _, s := range w.Stats {
		m[s.Label] = s.Value
	}
	return m
}

type obj = map[string]any

func TestAdGuard(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /control/status":               obj{"protection_enabled": true, "running": true, "version": "v0.107.60"},
		"GET /control/stats":                obj{"num_dns_queries": 10000, "num_blocked_filtering": 1200, "avg_processing_time": 0.012, "top_blocked_domains": []obj{{"ads.example.com": 300}, {"track.example.net": 500}}},
		"GET /control/querylog":             obj{"data": []obj{{"question": obj{"name": "ads.example.com"}, "client": "192.168.1.5", "reason": "FilteredBlackList", "time": "2026-10-19T10:00:00Z"}, {"question": obj{"name": "ads.example.com"}, "reason": "FilteredBlackList"}, {"question": obj{"name": "good.example.org"}, "client_info": obj{"name": "Laptop"}, "reason": "NotFilteredNotFound"}}},
		"GET /control/filtering/status":     obj{"user_rules": []string{"||old.example^"}},
		"POST /control/filtering/set_rules": nil,
		"POST /control/protection":          nil,
	}, func(r *http.Request) bool { u, p, ok := r.BasicAuth(); return ok && u == "admin" && p == "pw" })
	in := f.instance(map[string]string{"username": "admin", "password": "pw"})
	w, err := adguard{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	if s["DNS-Anfragen"] != "10.000" || s["Blockiert"] != "1.200" || s["Quote"] != "12,0 %" || s["Antwortzeit"] != "12 ms" || s["Schutz"] != "An" {
		t.Fatalf("stats: %v", s)
	}
	if len(w.Items) != 2 || w.Items[0].Title != "track.example.net" {
		t.Fatalf("top blocked: %+v", w.Items)
	}
	items, err := adguard{}.Search(context.Background(), in, "example")
	if err != nil || len(items) != 2 {
		t.Fatalf("search: %v %+v", err, items)
	}
	if items[0].Actions[0].ID != "allow" || items[1].Actions[0].ID != "block" || !strings.HasPrefix(items[0].Subtitle, "192.168.1.5") || items[1].Subtitle != "Laptop" {
		t.Fatalf("search items: %+v", items)
	}
	if err := (adguard{}).Action(context.Background(), in, "allow", map[string]string{"domain": "ads.example.com"}); err != nil {
		t.Fatal(err)
	}
	if b := f.body("POST /control/filtering/set_rules"); b != `{"rules":["||old.example^","@@||ads.example.com^"]}` {
		t.Fatalf("rules: %s", b)
	}
	if err := (adguard{}).Action(context.Background(), in, "allow", map[string]string{"domain": "evil^\n||x"}); err == nil {
		t.Fatal("invalid domain accepted")
	}
	(adguard{}).Action(context.Background(), in, "pause", map[string]string{"minutes": "5"})
	if b := f.body("POST /control/protection"); b != `{"duration":300000,"enabled":false}` {
		t.Fatalf("pause: %s", b)
	}
}

func TestHomeAssistant(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /api/states": []obj{
			{"entity_id": "light.kueche", "state": "on", "attributes": obj{"friendly_name": "Küche"}},
			{"entity_id": "sensor.temp", "state": "21.5", "attributes": obj{"friendly_name": "Wohnzimmer", "unit_of_measurement": "°C"}},
			{"entity_id": "update.core", "state": "on", "attributes": obj{"title": "Home Assistant Core", "installed_version": "2026.9", "latest_version": "2026.10"}},
			{"entity_id": "lock.tuer", "state": "locked", "attributes": obj{"friendly_name": "Haustür"}},
		},
		"POST /api/services/light/toggle": []obj{},
	}, func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer tok" })
	in := f.instance(map[string]string{"token": "tok"})
	w, err := homeAssistant{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if s := stats(w); s["Lichter an"] != "1" || s["Updates"] != "1" {
		t.Fatalf("overview: %v", s)
	}
	in.Values["entities"] = "sensor.temp, light.kueche, sensor.fehlt"
	w, _ = homeAssistant{}.Fetch(context.Background(), in)
	if s := stats(w); s["Wohnzimmer"] != "21.5 °C" || len(w.Items) != 1 || len(w.Alerts) != 1 {
		t.Fatalf("entities: %+v", w)
	}
	// own display names, also in the search
	in.Values["entities"] = "sensor.temp = Wohnzimmer Temperatur\nlight.kueche=Licht Küche; lock.tuer"
	w, _ = homeAssistant{}.Fetch(context.Background(), in)
	if s := stats(w); s["Wohnzimmer Temperatur"] != "21.5 °C" || len(w.Items) != 2 || w.Items[0].Title != "Licht Küche" || w.Items[1].Title != "Haustür" {
		t.Fatalf("named entities: %v %+v", s, w.Items)
	}
	if items, _ := (homeAssistant{}).Search(context.Background(), in, "küche"); len(items) != 1 || items[0].Title != "Licht Küche" {
		t.Fatalf("named search: %+v", items)
	}
	in.Values["entities"] = "sensor.temp, light.kueche, sensor.fehlt"
	items, _ := homeAssistant{}.Search(context.Background(), in, "haus")
	if len(items) != 1 || items[0].Actions[0].Confirm == "" {
		t.Fatalf("search: %+v", items)
	}
	if err := (homeAssistant{}).Action(context.Background(), in, "service", map[string]string{"entity": "light.kueche", "service": "toggle"}); err != nil {
		t.Fatal(err)
	}
	if b := f.body("POST /api/services/light/toggle"); b != `{"entity_id":"light.kueche"}` {
		t.Fatalf("body %s", b)
	}
	if err := (homeAssistant{}).Action(context.Background(), in, "service", map[string]string{"entity": "light.kueche", "service": "turn_off_everything"}); err == nil {
		t.Fatal("unknown service accepted")
	}
}

func TestLidarr(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /api/v1/queue":          obj{"totalRecords": 1, "records": []obj{{"title": "x", "size": 100, "sizeleft": 25, "status": "downloading", "artist": obj{"artistName": "Daft Punk"}, "album": obj{"title": "Discovery"}}}},
		"GET /api/v1/wanted/missing": obj{"totalRecords": 4},
		"GET /api/v1/health":         []obj{},
		"GET /api/v1/calendar":       []obj{},
		"GET /api/v1/artist":         []obj{{"id": 3, "artistName": "Daft Punk", "foreignArtistId": "abc", "monitored": true, "statistics": obj{"albumCount": 4, "trackFileCount": 30, "totalTrackCount": 50}}},
		"POST /api/v1/command":       obj{},
	}, func(r *http.Request) bool { return r.Header.Get("X-Api-Key") == "k" })
	in := f.instance(map[string]string{"apiKey": "k"})
	w, err := lidarr{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if s := stats(w); s["Fehlende Alben"] != "4" || w.Items[0].Title != "Daft Punk – Discovery" || *w.Items[0].Progress != 75 {
		t.Fatalf("%+v", w)
	}
	items, _ := lidarr{}.Search(context.Background(), in, "daft")
	if len(items) != 1 || items[0].URL != "https://svc.example.com/artist/abc" || len(items[0].Actions) != 1 {
		t.Fatalf("%+v", items)
	}
	lidarr{}.Action(context.Background(), in, "search", map[string]string{"artist": "3"})
	if b := f.body("POST /api/v1/command"); b != `{"artistId":3,"name":"ArtistSearch"}` {
		t.Fatalf("%s", b)
	}
}

func TestBazarr(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /bz/api/badges":          obj{"episodes": 3, "movies": 1, "providers": 0, "status": 0},
		"GET /bz/api/episodes/wanted": obj{"data": []obj{{"seriesTitle": "Severance", "episode_number": "2x5", "missing_subtitles": []obj{{"name": "German"}, {"name": "English"}}, "sonarrSeriesId": 9}}, "total": 1},
		"GET /bz/api/movies/wanted":   obj{"data": []obj{}, "total": 0},
		"GET /bz/api/system/searches": []obj{{"title": "Dune", "year": "2021", "radarrId": 4, "poster": "/bz/images/movies/poster.jpg"}},
		"POST /bz/api/system/tasks":   nil,
	}, func(r *http.Request) bool { return r.Header.Get("X-API-KEY") == "k" })
	in := f.instance(map[string]string{"apiKey": "k"})
	in.BaseURL += "/bz"
	w, err := bazarr{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Items) != 1 || w.Items[0].Subtitle != "German, English" || len(w.Actions) != 1 {
		t.Fatalf("%+v", w)
	}
	items, _ := bazarr{}.Search(context.Background(), in, "dune")
	if len(items) != 1 || items[0].Image != "/api/tiles/7/image?p=%2Fimages%2Fmovies%2Fposter.jpg" {
		t.Fatalf("%+v", items)
	}
	if _, _, err := (bazarr{}).ImageRequest(context.Background(), in, "/api/system/settings"); err == nil {
		t.Fatal("non-image path allowed")
	}
	if err := (bazarr{}).Action(context.Background(), in, "search_wanted", nil); err != nil {
		t.Fatal(err)
	}
}

func TestSpeedtest(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /api/v1/results/latest":  obj{"data": obj{"id": 5, "ping": 12.4, "download": 31250000, "upload": 5000000, "healthy": true, "status": "completed", "created_at": "2026-10-19 08:00:00"}},
		"GET /api/v1/results":         obj{"data": []obj{{"id": 5, "download": 31250000, "upload": 5000000, "status": "completed"}, {"id": 4, "download": 30000000, "upload": 5000000, "status": "failed"}}},
		"GET /api/speedtest/latest":   obj{"data": obj{"id": 5, "ping": 12.4, "download": 250.0, "upload": 40.0}},
		"POST /api/v1/speedtests/run": obj{"message": "ok"},
	}, nil)
	in := f.instance(map[string]string{"apiKey": "tok"})
	w, err := speedtest{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if s := stats(w); s["Download"] != "250 Mbit/s" || s["Upload"] != "40 Mbit/s" || s["Ping"] != "12 ms" || len(w.Items) != 2 || len(w.Actions) != 1 {
		t.Fatalf("%+v", w)
	}
	in.Values["apiKey"] = ""
	w, _ = speedtest{}.Fetch(context.Background(), in)
	if s := stats(w); s["Download"] != "250 Mbit/s" || len(w.Actions) != 0 {
		t.Fatalf("legacy %+v", w)
	}
}

func TestAnubis(t *testing.T) {
	f := newFake(t, map[string]any{"GET /metrics": `# HELP anubis_challenges_issued x
# TYPE anubis_challenges_issued counter
anubis_challenges_issued{method="embedded",asn="",asn_description=""} 40
anubis_challenges_issued{method="api",asn="1",asn_description="A, \"quoted\""} 2
anubis_challenges_validated{method="api",asn="",asn_description=""} 30
anubis_failed_validations{method="api",asn="",asn_description=""} 5
anubis_policy_results{rule="bot/ai-crawlers",action="DENY",asn="",asn_description=""} 120
anubis_policy_results{rule="generic-browser",action="CHALLENGE",asn="",asn_description=""} 42
go_goroutines 12
`}, nil)
	in := f.instance(map[string]string{"metrics": f.URL})
	w, err := anubis{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if s := stats(w); s["Challenges"] != "42" || s["Gelöst"] != "30" || s["Fehlgeschlagen"] != "5" {
		t.Fatalf("%v", s)
	}
	if len(w.Items) != 2 || w.Items[0].Title != "bot/ai-crawlers" || w.Items[0].Subtitle != "Blockieren" {
		t.Fatalf("%+v", w.Items)
	}
	labels := map[string]string{}
	parseLabels(`a="x, y",b="q\"r",c=""`, labels)
	if labels["a"] != "x, y" || labels["b"] != `q"r` || labels["c"] != "" {
		t.Fatalf("labels %v", labels)
	}
}
