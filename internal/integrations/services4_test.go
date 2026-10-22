package integrations

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestOpenMediaVault(t *testing.T) {
	logins := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Service, Method string
			Params          map[string]any
		}
		json.NewDecoder(r.Body).Decode(&req)
		reply := func(v any) { json.NewEncoder(w).Encode(map[string]any{"response": v, "error": nil}) }
		if req.Service == "Session" {
			logins++
			if req.Params["password"] != "pw" {
				w.WriteHeader(400)
				json.NewEncoder(w).Encode(map[string]any{"response": nil, "error": map[string]any{"code": 0, "message": "Incorrect username or password."}})
				return
			}
			reply(map[string]any{"status": "authenticated", "sessionid": "s1"})
			return
		}
		if r.Header.Get("X-OpenMediaVault-SessionID") != "s1" {
			w.WriteHeader(401)
			return
		}
		switch req.Service + "." + req.Method {
		case "System.getInformation":
			reply(map[string]any{"hostname": "nas", "cpuUtilization": 12.5, "memUtilization": "0.41", "uptime": 300000.5,
				"availablePkgUpdates": 3, "rebootRequired": true, "configDirty": false})
		case "FileSystemMgmt.enumerateMountedFilesystems":
			reply([]map[string]any{
				{"devicefile": "/dev/sda1", "mountpoint": "/", "size": "32000000000", "available": "20000000000", "percentage": 37},
				{"devicefile": "/dev/md0", "label": "data", "mountpoint": "/srv/data", "size": "8000000000000", "available": "400000000000", "percentage": 95},
			})
		case "Smart.enumerateDevices":
			reply([]map[string]any{{"devicefile": "/dev/sda", "model": "WD Red", "overallstatus": "GOOD"}, {"devicefile": "/dev/sdb", "model": "Seagate", "overallstatus": "BAD_SECTOR"}})
		default:
			reply(nil)
		}
	}))
	defer srv.Close()
	in := &Instance{BaseURL: srv.URL, Values: map[string]string{"username": "admin", "password": "pw"}, Client: srv.Client(), Lang: "de"}
	w, err := openmediavault{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	if s["CPU"] != "12 %" || s["RAM"] != "41 %" || s["Laufzeit"] != "3 Tage" || s["Updates"] != "3" || s["Platten OK"] != "1/2" {
		t.Fatalf("stats %v", s)
	}
	if len(w.Items) != 2 || w.Items[0].Title != "data" || w.Items[1].Title != "System" || len(w.Alerts) != 3 {
		t.Fatalf("items %+v alerts %v", w.Items, w.Alerts)
	}
	if logins != 1 {
		t.Fatalf("logged in %d times, want once per session", logins)
	}
	bad := &Instance{BaseURL: srv.URL + "/", Values: map[string]string{"username": "x", "password": "no"}, Client: srv.Client(), Lang: "de"}
	if err := (openmediavault{}).Test(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "Passwort falsch") {
		t.Fatalf("wrong password: %v", err)
	}
}

func TestLoginPageInsteadOfAPI(t *testing.T) {
	login := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte("<!doctype html><title>Login</title>"))
	}))
	defer login.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, login.URL+"/auth", http.StatusFound)
	}))
	defer proxy.Close()
	j := &jellyseerr{titles: map[string]string{}}
	for _, c := range []struct{ url, want string }{{login.URL, "Anmeldeseite"}, {proxy.URL, "weitergeleitet"}} {
		in := &Instance{BaseURL: c.url, Values: map[string]string{"apiKey": "k"}, Client: NewClient(false), Lang: "de"}
		_, err := j.Fetch(context.Background(), in)
		if err == nil || !strings.Contains(err.Error(), c.want) || !errors.Is(err, ErrNotJSON) {
			t.Errorf("%s: %v", c.url, err)
		}
	}
}

func TestOmvNum(t *testing.T) {
	for in, want := range map[string]float64{
		`"3.70 GiB"`: 3.70 * (1 << 30), `"512 MiB"`: 512 << 20, `"12 %"`: 12, `"8000000000000"`: 8e12,
		`41.5`: 41.5, `"2,5 TB"`: 2.5e12, `""`: 0, `null`: 0, `"unbekannt"`: 0,
	} {
		var n omvNum
		if err := json.Unmarshal([]byte(in), &n); err != nil || float64(n) != want {
			t.Errorf("%s: got %v, %v; want %v", in, float64(n), err, want)
		}
	}
	var info omvInfo
	if err := json.Unmarshal([]byte(`{"cpuUtilization":"7 %","memUtilization":"3.70 GiB","uptime":12}`), &info); err != nil {
		t.Fatalf("formatted values break the tile: %v", err)
	}
}

func TestPhotoPrism(t *testing.T) {
	hash := strings.Repeat("ab", 20)
	f := newFake(t, map[string]any{
		"GET /api/v1/config": obj{"previewToken": "pt123", "count": obj{"photos": 1200, "videos": 40, "albums": 12, "people": 7, "review": 2}},
		"GET /api/v1/photos": []obj{{"UID": "pq1", "Type": "image", "Title": "Strand", "TakenAt": "2025-07-01T10:00:00Z", "PlaceLabel": "Sylt, Deutschland", "Hash": hash}},
	}, func(r *http.Request) bool { return r.Header.Get("Authorization") == "Bearer tok" })
	in := f.instance(map[string]string{"token": "tok"})
	w, err := photoprism{}.Fetch(context.Background(), in)
	if err != nil || stats(w)["Fotos"] != "1.200" || stats(w)["Zu prüfen"] != "2" || len(w.Items) != 1 || !strings.Contains(w.Items[0].Subtitle, "Sylt") {
		t.Fatalf("%v %+v", err, w)
	}
	path, h, err := photoprism{}.ImageRequest(context.Background(), in, "/api/v1/t/"+hash+"/tile_224")
	if err != nil || path != "/api/v1/t/"+hash+"/pt123/tile_224" || h["Authorization"] != "Bearer tok" {
		t.Fatalf("image %q %v %v", path, h, err)
	}
	if _, _, err := (photoprism{}).ImageRequest(context.Background(), in, "/api/v1/config"); err == nil {
		t.Fatal("non-thumbnail path accepted")
	}
	items, err := photoprism{}.Search(context.Background(), in, "strand")
	if err != nil || len(items) != 1 || items[0].Image == "" {
		t.Fatalf("search %v %+v", err, items)
	}
}

func TestWLED(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /json": obj{"state": obj{"on": true, "bri": 128, "ps": 2, "seg": []obj{{"fx": 1, "on": true}}},
			"info":    obj{"name": "Regal", "ver": "0.15.0", "leds": obj{"count": 120, "pwr": 850}},
			"effects": []string{"Solid", "Blink"}},
		"GET /presets.json": obj{"0": obj{}, "1": obj{"n": "Gemütlich"}, "2": obj{"n": "Weihnachten"}},
		"POST /json/state":  obj{"success": true},
	}, nil)
	in := f.instance(map[string]string{})
	w, err := wled{}.Fetch(context.Background(), in)
	if err != nil || stats(w)["Helligkeit"] != "50 %" || stats(w)["Effekt"] != "Blink" || len(w.Items) != 2 || w.Items[1].Badge != "Aktiv" || w.Actions[0].Label != "Ausschalten" {
		t.Fatalf("%v %+v", err, w)
	}
	items, _ := wled{}.Search(context.Background(), in, "weih")
	if len(items) != 1 || items[0].Actions[0].Params["id"] != "2" {
		t.Fatalf("search %+v", items)
	}
	if err := (wled{}).Action(context.Background(), in, "bri", map[string]string{"step": "+"}); err != nil || !strings.Contains(f.body("POST /json/state"), `"bri":"~26"`) {
		t.Fatalf("bri %v %s", err, f.body("POST /json/state"))
	}
	if err := (wled{}).Action(context.Background(), in, "preset", map[string]string{"id": "999"}); err == nil {
		t.Fatal("bad preset accepted")
	}
}

func TestUnifiProtect(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /proxy/protect/integration/v1/cameras": []obj{
			{"id": "65f1a2b3c4d5e6f708192a3b", "name": "Einfahrt", "state": "CONNECTED"},
			{"id": "65f1a2b3c4d5e6f708192a3c", "name": "Garten", "state": "DISCONNECTED"},
		},
	}, func(r *http.Request) bool { return r.Header.Get("X-API-KEY") == "k" })
	in := f.instance(map[string]string{"apiKey": "k"})
	w, err := unifiProtect{}.Fetch(context.Background(), in)
	if err != nil || stats(w)["Kameras online"] != "1/2" || len(w.Alerts) != 1 || w.Items[0].Title != "Garten" || w.Items[1].Image == "" {
		t.Fatalf("%v %+v", err, w)
	}
	p := w.Items[1].Image[strings.Index(w.Items[1].Image, "p=")+2:]
	path, _ := urlUnescape(p)
	if p, h, err := (unifiProtect{}).ImageRequest(context.Background(), in, path); err != nil || h["X-API-KEY"] != "k" || strings.Contains(p, "?") {
		t.Fatalf("snapshot %q → %q %v", path, p, err)
	}
	if _, _, err := (unifiProtect{}).ImageRequest(context.Background(), in, "/proxy/protect/integration/v1/cameras"); err == nil {
		t.Fatal("non-snapshot path accepted")
	}
}

func urlUnescape(s string) (string, error) { return url.QueryUnescape(s) }
