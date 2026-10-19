package integrations

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestEvcc(t *testing.T) {
	lp := []obj{{"title": "Garage", "mode": "pv", "charging": true, "connected": true, "chargePower": 7400, "vehicleSoc": 55, "vehicleTitle": "ID.3", "chargedEnergy": 3200}}
	f := newFake(t, map[string]any{
		"GET /api/state":                  obj{"pvPower": 8200, "homePower": 650, "grid": obj{"power": -1200}, "battery": obj{"power": -500, "soc": 80}, "loadpoints": lp},
		"POST /api/loadpoints/1/mode/now": "now",
	}, nil)
	in := f.instance(nil)
	w, err := evcc{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	if s["PV"] != "8,2 kW" || s["Haus"] != "650 W" || s["Einspeisung"] != "1,2 kW" || s["Batterie"] != "80 %" || s["Batterie lädt"] != "500 W" {
		t.Fatalf("%v", s)
	}
	if len(w.Items) != 1 || w.Items[0].Badge != "PV" || len(w.Items[0].Actions) != 3 || !strings.Contains(w.Items[0].Subtitle, "ID.3") {
		t.Fatalf("%+v", w.Items)
	}
	if err := (evcc{}).Action(context.Background(), in, "mode", map[string]string{"loadpoint": "1", "mode": "now"}); err != nil {
		t.Fatal(err)
	}
	if err := (evcc{}).Action(context.Background(), in, "mode", map[string]string{"loadpoint": "1", "mode": "../x"}); err == nil {
		t.Fatal("bad mode accepted")
	}
	// Older versions wrap everything in "result" and use flat keys.
	old := newFake(t, map[string]any{"GET /api/state": obj{"result": obj{"pvPower": 0, "gridPower": 1500, "batterySoc": 20, "loadpoints": []obj{}}}}, nil)
	w, err = evcc{}.Fetch(context.Background(), old.instance(nil))
	if s := stats(w); err != nil || s["Netzbezug"] != "1,5 kW" || s["Batterie"] != "20 %" {
		t.Fatalf("old: %v %v", err, s)
	}
}

func TestUniFi(t *testing.T) {
	site := "88f7af54-98f8-306a-a1c7-c9349722b1f6"
	dev := "4de4adb9-21ee-3d4c-9b9f-1c8b9b8e7f11"
	f := newFake(t, map[string]any{
		"GET /proxy/network/integration/v1/sites":                                           obj{"data": []obj{{"id": site, "name": "Default", "internalReference": "default"}}},
		"GET /proxy/network/integration/v1/sites/" + site + "/clients":                      obj{"totalCount": 42, "data": []obj{{"name": "Max iPhone", "type": "WIRELESS", "ipAddress": "192.168.1.20", "macAddress": "aa:bb"}}},
		"GET /proxy/network/integration/v1/sites/" + site + "/devices":                      obj{"totalCount": 2, "data": []obj{{"id": dev, "name": "AP Flur", "model": "U6 Lite", "state": "ONLINE", "firmwareUpdatable": true}, {"id": dev, "name": "Switch Keller", "model": "USW Lite 8", "state": "OFFLINE"}}},
		"POST /proxy/network/integration/v1/sites/" + site + "/devices/" + dev + "/actions": obj{},
	}, func(r *http.Request) bool { return r.Header.Get("X-API-KEY") == "k" })
	in := f.instance(map[string]string{"apiKey": "k"})
	w, err := unifi{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if s := stats(w); s["Clients"] != "42" || s["Geräte online"] != "1/2" || s["Updates"] != "1" || len(w.Items) != 1 || w.Items[0].Title != "Switch Keller" {
		t.Fatalf("%v %+v", s, w.Items)
	}
	items, err := unifi{}.Search(context.Background(), in, "iphone")
	if err != nil || len(items) != 1 || !strings.HasPrefix(items[0].Subtitle, "WLAN · 192.168.1.20") {
		t.Fatalf("%v %+v", err, items)
	}
	items, _ = unifi{}.Search(context.Background(), in, "flur")
	if len(items) != 1 || items[0].Actions[0].ID != "restart" {
		t.Fatalf("%+v", items)
	}
	if err := (unifi{}).Action(context.Background(), in, "restart", items[0].Actions[0].Params); err != nil {
		t.Fatal(err)
	}
	if b := f.body("POST /proxy/network/integration/v1/sites/" + site + "/devices/" + dev + "/actions"); b != `{"action":"RESTART"}` {
		t.Fatal(b)
	}
	// Self-hosted controllers have no /proxy/network prefix.
	self := newFake(t, map[string]any{"GET /integration/v1/info": obj{"applicationVersion": "9.1"}}, nil)
	if err := (unifi{}).Test(context.Background(), self.instance(map[string]string{"apiKey": "k"})); err != nil {
		t.Fatal(err)
	}
}

func TestImmich(t *testing.T) {
	id := "0d3f2e7c-1111-4c5e-9a6b-123456789abc"
	f := newFake(t, map[string]any{
		"GET /api/server/statistics": obj{"photos": 1200, "videos": 30, "usage": 5e9, "usageByUser": []obj{{"userName": "Max", "photos": 1000}, {"userName": "Anna", "photos": 200}}},
		"GET /api/search/person":     []obj{{"id": id, "name": "Anna"}},
		"POST /api/search/smart":     obj{"assets": obj{"items": []obj{{"id": id, "originalFileName": "IMG_1.jpg", "localDateTime": "2026-07-01T10:00:00Z", "type": "IMAGE"}}}},
	}, func(r *http.Request) bool { return r.Header.Get("x-api-key") == "k" })
	in := f.instance(map[string]string{"apiKey": "k"})
	w, err := immich{}.Fetch(context.Background(), in)
	if err != nil || stats(w)["Fotos"] != "1.200" || len(w.Items) != 2 {
		t.Fatalf("%v %+v", err, w)
	}
	in.Values["smartSearch"] = "true"
	items, err := immich{}.Search(context.Background(), in, "anna")
	if err != nil || len(items) != 2 || items[1].URL != "https://svc.example.com/photos/"+id {
		t.Fatalf("%v %+v", err, items)
	}
	p, _, err := immich{}.ImageRequest(context.Background(), in, "/api/assets/"+id+"/thumbnail?size=thumbnail")
	if err != nil || p == "" {
		t.Fatal(err)
	}
	if _, _, err := (immich{}).ImageRequest(context.Background(), in, "/api/users/me"); err == nil {
		t.Fatal("non-image path allowed")
	}
}

func TestAudiobookshelf(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /api/libraries":             obj{"libraries": []obj{{"id": "lib1", "name": "Bücher", "mediaType": "book", "stats": obj{"totalItems": 120, "totalSize": 2e10, "totalDuration": 360000}}}},
		"GET /api/me/items-in-progress":  obj{"libraryItems": []obj{{"id": "it1", "media": obj{"metadata": obj{"title": "Der Hobbit", "authorName": "Tolkien"}}}}},
		"GET /api/libraries/lib1/search": obj{"book": []obj{{"libraryItem": obj{"id": "it1", "media": obj{"metadata": obj{"title": "Der Hobbit", "authorName": "Tolkien"}}}}}, "authors": []obj{{"id": "a1", "name": "J. R. R. Tolkien", "numBooks": 3}}},
	}, func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer k" })
	in := f.instance(map[string]string{"apiKey": "k"})
	w, err := audiobookshelf{}.Fetch(context.Background(), in)
	if s := stats(w); err != nil || s["Hörbücher"] != "120" || s["Stunden"] != "100" || len(w.Items) != 1 {
		t.Fatalf("%v %+v", err, w)
	}
	items, err := audiobookshelf{}.Search(context.Background(), in, "hobbit")
	if err != nil || len(items) != 2 || items[0].Image == "" || items[1].URL != "https://svc.example.com/author/a1" {
		t.Fatalf("%v %+v", err, items)
	}
}

func TestKavita(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /api/Stats/server/stats":   obj{"seriesCount": 50, "volumeCount": 300, "totalFiles": 320, "totalSize": 1e10},
		"POST /api/Series/on-deck":      []obj{{"id": 4, "name": "One Piece", "libraryId": 2, "libraryName": "Manga", "pages": 200, "pagesRead": 50}},
		"GET /api/Search/search":        obj{"series": []obj{{"seriesId": 4, "name": "One Piece", "libraryId": 2, "libraryName": "Manga"}}},
		"POST /api/Plugin/authenticate": obj{"token": "jwt"},
	}, func(r *http.Request) bool {
		// Behaves like Kavita before 0.8.9: only the JWT is accepted.
		return r.URL.Path == "/api/Plugin/authenticate" || r.Header.Get("Authorization") == "Bearer jwt"
	})
	in := f.instance(map[string]string{"apiKey": "k"})
	w, err := kavita{}.Fetch(context.Background(), in)
	if err != nil || stats(w)["Serien"] != "50" || len(w.Items) != 1 || *w.Items[0].Progress != 25 {
		t.Fatalf("%v %+v", err, w)
	}
	items, err := kavita{}.Search(context.Background(), in, "one")
	if err != nil || len(items) != 1 || items[0].URL != "https://svc.example.com/library/2/series/4" {
		t.Fatalf("%v %+v", err, items)
	}
	p, _, err := kavita{}.ImageRequest(context.Background(), in, "/api/image/series-cover?seriesId=4")
	if err != nil || p != "/api/image/series-cover?seriesId=4&apiKey=k" {
		t.Fatal(p, err)
	}
}

func TestKomga(t *testing.T) {
	f := newFake(t, map[string]any{
		"POST /api/v1/series/list": obj{"totalElements": 12, "content": []obj{{"id": "S1", "booksCount": 5, "metadata": obj{"title": "Saga"}}}},
		"POST /api/v1/books/list":  obj{"totalElements": 80, "content": []obj{{"id": "B1", "seriesTitle": "Saga", "metadata": obj{"title": "Band 1"}, "media": obj{"pagesCount": 100}, "readProgress": obj{"page": 40}}}},
	}, func(r *http.Request) bool { return r.Header.Get("X-API-Key") == "k" })
	in := f.instance(map[string]string{"apiKey": "k"})
	w, err := komga{}.Fetch(context.Background(), in)
	if s := stats(w); err != nil || s["Serien"] != "12" || s["Bücher"] != "80" || *w.Items[0].Progress != 40 {
		t.Fatalf("%v %+v", err, w)
	}
	items, _ := komga{}.Search(context.Background(), in, "saga")
	if len(items) != 2 || items[0].Image != "/api/tiles/7/image?p=%2Fapi%2Fv1%2Fseries%2FS1%2Fthumbnail" {
		t.Fatalf("%+v", items)
	}
}

func TestNextcloud(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /ocs/v2.php/apps/serverinfo/api/v1/info":   obj{"ocs": obj{"data": obj{"nextcloud": obj{"system": obj{"freespace": 5e11}, "storage": obj{"num_users": 3, "num_files": 54321}, "shares": obj{"num_shares": 7}}, "activeUsers": obj{"last24hours": 2}}}},
		"GET /ocs/v2.php/search/providers":              obj{"ocs": obj{"data": []obj{{"id": "files", "name": "Dateien"}, {"id": "settings", "name": "Einstellungen"}}}},
		"GET /ocs/v2.php/search/providers/files/search": obj{"ocs": obj{"data": obj{"entries": []obj{{"title": "Urlaub.jpg", "subline": "/Fotos", "resourceUrl": "https://cloud.example.com/index.php/f/12", "thumbnailUrl": "https://cloud.example.com/index.php/core/preview?x=32&y=32&fileId=12"}}}}},
	}, func(r *http.Request) bool {
		u, p, _ := r.BasicAuth()
		return u == "max" && p == "app" && r.Header.Get("OCS-APIRequest") == "true"
	})
	in := f.instance(map[string]string{"username": "max", "password": "app"})
	w, err := nextcloud{}.Fetch(context.Background(), in)
	if s := stats(w); err != nil || s["Dateien"] != "54.321" || s["Aktiv (24 h)"] != "2" {
		t.Fatalf("%v %+v", err, w)
	}
	items, err := nextcloud{}.Search(context.Background(), in, "urlaub")
	if err != nil || len(items) != 1 || items[0].URL != "https://svc.example.com/index.php/f/12" || !strings.Contains(items[0].Image, "core%2Fpreview") {
		t.Fatalf("%v %+v", err, items)
	}
}

func TestIoBroker(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /v1/state/hm-rpc.0.ABC.1.STATE,javascript.0.temp": []obj{
			{"id": "hm-rpc.0.ABC.1.STATE", "val": true, "common": obj{"name": obj{"de": "Stehlampe", "en": "Lamp"}, "type": "boolean", "write": true}},
			{"id": "javascript.0.temp", "val": 21.456, "common": obj{"name": "Temperatur", "unit": "°C", "type": "number"}},
		},
		"GET /v1/objects": obj{"system.adapter.admin.0": obj{"common": obj{"enabled": true}}, "system.adapter.hm-rpc.0": obj{"common": obj{"enabled": true}}, "system.adapter.old.0": obj{"common": obj{"enabled": false}}},
		"GET /v1/states":  obj{"system.adapter.admin.0.alive": obj{"val": true}, "system.adapter.hm-rpc.0.alive": obj{"val": false}},
		"GET /v1/state/hm-rpc.0.ABC.1.STATE/toggle": obj{},
	}, nil)
	in := f.instance(map[string]string{"states": "hm-rpc.0.ABC.1.STATE, javascript.0.temp"})
	w, err := iobroker{}.Fetch(context.Background(), in)
	if err != nil || stats(w)["Temperatur"] != "21,46 °C" || len(w.Items) != 1 || w.Items[0].Title != "Stehlampe" || w.Items[0].Badge != "An" {
		t.Fatalf("%v %+v", err, w)
	}
	if err := (iobroker{}).Action(context.Background(), in, "toggle", map[string]string{"id": "hm-rpc.0.ABC.1.STATE"}); err != nil {
		t.Fatal(err)
	}
	if err := (iobroker{}).Action(context.Background(), in, "toggle", map[string]string{"id": "a/../b"}); err == nil {
		t.Fatal("bad id accepted")
	}
	in.Values["states"] = ""
	w, err = iobroker{}.Fetch(context.Background(), in)
	if err != nil || stats(w)["Adapter aktiv"] != "1/2" || len(w.Items) != 1 || w.Items[0].Title != "hm-rpc.0" {
		t.Fatalf("%v %+v", err, w)
	}
	// simple-api only
	simple := newFake(t, map[string]any{"GET /get/javascript.0.temp": obj{"_id": "javascript.0.temp", "val": 5, "common": obj{"name": "T", "type": "number"}}}, nil)
	w, err = iobroker{}.Fetch(context.Background(), simple.instance(map[string]string{"states": "javascript.0.temp"}))
	if err != nil || stats(w)["T"] != "5" {
		t.Fatalf("simple: %v %+v", err, w)
	}
}
