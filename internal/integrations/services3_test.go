package integrations

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func inst(srv *httptest.Server, values map[string]string) *Instance {
	return &Instance{BaseURL: srv.URL, PublicURL: "https://svc.example.com", Values: values, Client: srv.Client(), Lang: "de", TileID: 3}
}

func TestQBittorrent(t *testing.T) {
	var stopped bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			r.ParseForm()
			if r.Form.Get("password") != "pw" {
				io.WriteString(w, "Fails.")
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "QBT_SID_8080", Value: "abc"})
			io.WriteString(w, "Ok.")
			return
		}
		if c, err := r.Cookie("QBT_SID_8080"); err != nil || c.Value != "abc" {
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/api/v2/transfer/info":
			json.NewEncoder(w).Encode(obj{"dl_info_speed": 2097152, "up_info_speed": 1024, "connection_status": "connected"})
		case "/api/v2/torrents/info":
			json.NewEncoder(w).Encode([]obj{
				{"hash": "a", "name": "Debian ISO", "progress": 0.5, "dlspeed": 1048576, "eta": 120, "state": "downloading", "size": 4e9},
				{"hash": "b", "name": "Ubuntu", "progress": 1, "state": "uploading", "size": 3e9},
			})
		case "/api/v2/torrents/stop":
			stopped = true
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	in := inst(srv, map[string]string{"username": "admin", "password": "pw"})
	w, err := qbittorrent{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if s := stats(w); s["↓"] != "2,0 MB/s" && s["↓"] != "2.0 MB/s" || s["Lädt"] != "1" || s["Seedet"] != "1" || len(w.Items) != 1 || *w.Items[0].Progress != 50 {
		t.Fatalf("%v %+v", stats(w), w.Items)
	}
	if err := (qbittorrent{}).Action(context.Background(), in, "stop", nil); err != nil || !stopped {
		t.Fatal(err)
	}
	items, _ := qbittorrent{}.Search(context.Background(), in, "ubuntu")
	if len(items) != 1 {
		t.Fatal(items)
	}
	bad := inst(srv, map[string]string{"username": "admin", "password": "x"})
	sessions.Delete(sessionKey(bad, "qbt"))
	if _, err := (qbittorrent{}).Fetch(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "falsch") {
		t.Fatalf("wrong password: %v", err)
	}
}

func TestTransmission(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Transmission-Session-Id") != "sess1" {
			w.Header().Set("X-Transmission-Session-Id", "sess1")
			w.WriteHeader(409)
			return
		}
		var req struct {
			Method string `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		got = append(got, req.Method)
		var args any = obj{}
		switch req.Method {
		case "session-stats":
			args = obj{"downloadSpeed": 1000, "uploadSpeed": 10, "activeTorrentCount": 1, "pausedTorrentCount": 0, "torrentCount": 2}
		case "torrent-get":
			args = obj{"torrents": []obj{{"id": 1, "name": "Film", "percentDone": 0.25, "rateDownload": 1000, "eta": 60, "status": 4, "totalSize": 1e9}, {"id": 2, "name": "Alt", "percentDone": 1, "status": 6}}}
		}
		json.NewEncoder(w).Encode(obj{"result": "success", "arguments": args})
	}))
	defer srv.Close()
	in := inst(srv, map[string]string{})
	w, err := transmission{}.Fetch(context.Background(), in)
	if err != nil || stats(w)["Lädt"] != "1" || len(w.Items) != 1 || w.Items[0].Title != "Film" {
		t.Fatalf("%v %+v", err, w)
	}
	transmission{}.Action(context.Background(), in, "stop", nil)
	if got[len(got)-1] != "torrent-stop" {
		t.Fatal(got)
	}
}

func TestDeluge(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string `json:"method"`
			Params []any  `json:"params"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		authed := false
		if c, err := r.Cookie("_session_id"); err == nil && c.Value == "s1" {
			authed = true
		}
		var result any
		switch {
		case req.Method == "auth.login":
			ok := req.Params[0] == "deluge"
			if ok {
				http.SetCookie(w, &http.Cookie{Name: "_session_id", Value: "s1"})
			}
			result = ok
		case !authed:
			json.NewEncoder(w).Encode(obj{"result": nil, "error": obj{"message": "Not authenticated", "code": 1}, "id": 1})
			return
		case req.Method == "web.connected":
			result = true
		case req.Method == "web.update_ui":
			result = obj{"connected": true, "torrents": obj{"h1": obj{"name": "Linux", "progress": 40.0, "download_payload_rate": 5000, "eta": 30, "state": "Downloading", "total_size": 1e9}},
				"stats": obj{"download_rate": 5000, "upload_rate": 100}}
		default:
			result = nil
		}
		json.NewEncoder(w).Encode(obj{"result": result, "error": nil, "id": 1})
	}))
	defer srv.Close()
	in := inst(srv, map[string]string{"password": "deluge"})
	w, err := deluge{}.Fetch(context.Background(), in)
	if err != nil || len(w.Items) != 1 || *w.Items[0].Progress != 40 {
		t.Fatalf("%v %+v", err, w)
	}
	if err := (deluge{}).Action(context.Background(), in, "stop", nil); err != nil {
		t.Fatal(err)
	}
}

func TestNZBGet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, _ := r.BasicAuth()
		if u != "nzb" || p != "pw" {
			w.WriteHeader(401)
			return
		}
		var req struct {
			Method string `json:"method"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		var result any = true
		switch req.Method {
		case "status":
			result = obj{"DownloadRate": 3145728, "DownloadRateLo": 3145728, "DownloadRateHi": 0, "RemainingSizeLo": 1073741824, "RemainingSizeHi": 1, "DownloadPaused": false}
		case "listgroups":
			result = []obj{{"NZBID": 1, "NZBName": "Serie.S01E01", "Status": "DOWNLOADING", "FileSizeMB": 1000, "RemainingSizeMB": 250, "Health": 1000}}
		}
		json.NewEncoder(w).Encode(obj{"version": "1.1", "id": 1, "result": result})
	}))
	defer srv.Close()
	in := inst(srv, map[string]string{"username": "nzb", "password": "pw"})
	w, err := nzbget{}.Fetch(context.Background(), in)
	if err != nil || !strings.HasPrefix(stats(w)["Verbleibend"], "5") || len(w.Items) != 1 || *w.Items[0].Progress != 75 || w.Actions[0].ID != "pause" {
		t.Fatalf("%v %v %+v", err, stats(w), w)
	}
}

func TestPlexEmbyNavidrome(t *testing.T) {
	plexSrv := newFake(t, map[string]any{
		"GET /status/sessions":        obj{"MediaContainer": obj{"size": 1, "Metadata": []obj{{"ratingKey": "5", "type": "episode", "title": "Pilot", "grandparentTitle": "Severance", "parentIndex": 1, "index": 1, "viewOffset": 600000, "duration": 3000000, "User": obj{"title": "max"}, "Player": obj{"title": "TV", "state": "playing"}}}}},
		"GET /library/sections":       obj{"MediaContainer": obj{"Directory": []obj{{"key": "1", "type": "movie"}, {"key": "2", "type": "show"}}}},
		"GET /library/sections/1/all": obj{"MediaContainer": obj{"totalSize": 812}},
		"GET /library/sections/2/all": obj{"MediaContainer": obj{"totalSize": 143}},
		"GET /identity":               obj{"MediaContainer": obj{"machineIdentifier": "m1"}},
		"GET /hubs/search":            obj{"MediaContainer": obj{"Hub": []obj{{"type": "movie", "Metadata": []obj{{"ratingKey": "9", "type": "movie", "title": "Dune", "year": 2021, "thumb": "/library/metadata/9/thumb/1700000000"}}}}}},
	}, func(r *http.Request) bool { return r.Header.Get("X-Plex-Token") == "tok" || r.URL.Path == "/identity" })
	in := plexSrv.instance(map[string]string{"token": "tok"})
	w, err := plex{}.Fetch(context.Background(), in)
	if s := stats(w); err != nil || s["Filme"] != "812" || s["Streams"] != "1" || *w.Items[0].Progress != 20 {
		t.Fatalf("%v %+v", err, w)
	}
	items, _ := plex{}.Search(context.Background(), in, "dune")
	if len(items) != 1 || !strings.Contains(items[0].URL, "/server/m1/details?key=%2Flibrary%2Fmetadata%2F9") || items[0].Image == "" {
		t.Fatalf("%+v", items)
	}

	emby := newFake(t, map[string]any{
		"GET /emby/Sessions":           []obj{},
		"GET /emby/Items/Counts":       obj{"MovieCount": 5},
		"GET /emby/Items":              obj{"Items": []obj{{"Id": "x1", "Name": "Dune", "Type": "Movie", "ProductionYear": 2021}}},
		"GET /emby/System/Info/Public": obj{"Id": "srv9"},
	}, func(r *http.Request) bool {
		return r.Header.Get("X-Emby-Token") == "k" || strings.HasSuffix(r.URL.Path, "Public")
	})
	ein := emby.instance(map[string]string{"apiKey": "k"})
	if w, err := (jellyfin{emby: true}).Fetch(context.Background(), ein); err != nil || stats(w)["Filme"] != "5" {
		t.Fatal(err)
	}
	items, _ = jellyfin{emby: true}.Search(context.Background(), ein, "dune")
	if len(items) != 1 || items[0].URL != "https://svc.example.com/web/index.html#!/item?id=x1&serverId=srv9" {
		t.Fatalf("%+v", items)
	}

	navi := newFake(t, map[string]any{
		"GET /rest/getScanStatus": obj{"subsonic-response": obj{"status": "ok", "scanStatus": obj{"count": 4321}}},
		"GET /rest/getNowPlaying": obj{"subsonic-response": obj{"status": "ok", "nowPlaying": obj{"entry": []obj{{"title": "Song", "artist": "Band", "username": "max", "albumId": "al1"}}}}},
		"GET /rest/search3":       obj{"subsonic-response": obj{"status": "ok", "searchResult3": obj{"album": []obj{{"id": "al1", "name": "Album", "artist": "Band", "coverArt": "al-1", "year": 2020}}}}},
	}, func(r *http.Request) bool { return r.URL.Query().Get("u") == "max" && r.URL.Query().Get("t") != "" })
	nin := navi.instance(map[string]string{"username": "max", "password": "pw"})
	w, err = navidrome{}.Fetch(context.Background(), nin)
	if err != nil || stats(w)["Titel"] != "4.321" || len(w.Items) != 1 {
		t.Fatalf("%v %+v", err, w)
	}
	items, _ = navidrome{}.Search(context.Background(), nin, "alb")
	if len(items) != 1 || items[0].URL != "https://svc.example.com/app/#/album/al1/show" {
		t.Fatalf("%+v", items)
	}
	p, _, err := navidrome{}.ImageRequest(context.Background(), nin, "/rest/getCoverArt?id=al-1&size=120")
	if err != nil || !strings.Contains(p, "&u=max") {
		t.Fatal(p, err)
	}
}

func TestPiholeOPNsenseTdarrReadarr(t *testing.T) {
	ph := newFake(t, map[string]any{
		"POST /api/auth":                obj{"session": obj{"valid": true, "sid": "sid1"}},
		"GET /api/stats/summary":        obj{"queries": obj{"total": 1000, "blocked": 250, "percent_blocked": 25.0}, "clients": obj{"active": 7}},
		"GET /api/dns/blocking":         obj{"blocking": "enabled", "timer": nil},
		"GET /api/queries":              obj{"queries": []obj{{"domain": "ads.example", "status": "GRAVITY", "time": 1760000000, "client": obj{"ip": "10.0.0.5"}}}},
		"POST /api/dns/blocking":        obj{"blocking": "disabled"},
		"POST /api/domains/allow/exact": obj{},
	}, func(r *http.Request) bool { return r.URL.Path == "/api/auth" || r.Header.Get("X-FTL-SID") == "sid1" })
	pin := ph.instance(map[string]string{"password": "app"})
	w, err := pihole{}.Fetch(context.Background(), pin)
	if s := stats(w); err != nil || s["Quote"] != "25,0 %" || s["Schutz"] != "An" {
		t.Fatalf("%v %+v", err, w)
	}
	items, _ := pihole{}.Search(context.Background(), pin, "ads")
	if len(items) != 1 || items[0].Actions[0].ID != "allow" {
		t.Fatalf("%+v", items)
	}
	if err := (pihole{}).Action(context.Background(), pin, "disable", map[string]string{"minutes": "5"}); err != nil || ph.body("POST /api/dns/blocking") != `{"blocking":false,"timer":300}` {
		t.Fatal(err, ph.body("POST /api/dns/blocking"))
	}
	// Pi-hole 5
	v5 := newFake(t, map[string]any{"GET /admin/api.php": obj{"status": "disabled", "dns_queries_today": 10, "ads_blocked_today": 1, "ads_percentage_today": 10.0}}, nil)
	w, err = pihole{}.Fetch(context.Background(), v5.instance(map[string]string{"password": "token"}))
	if err != nil || stats(w)["Schutz"] != "Aus" {
		t.Fatalf("v5 %v %+v", err, w)
	}

	opn := newFake(t, map[string]any{
		"GET /api/core/firmware/status":                obj{"status": "update", "product": obj{"product_version": "25.1", "product_latest": "25.1.5"}},
		"GET /api/diagnostics/traffic/interface":       obj{"interfaces": obj{"wan": obj{"bytes received": "1000000", "bytes transmitted": "500000"}}, "time": 1000.0},
		"GET /api/diagnostics/system/system_resources": obj{"memory": obj{"total": "8000000000", "used": 2000000000}},
		"GET /api/diagnostics/system/system_time":      obj{"uptime": "3 days, 04:12:00", "loadavg": "0.21, 0.30, 0.25"},
		"GET /api/routes/gateway/status":               obj{"items": []obj{{"name": "WAN_DHCP", "status": "none", "delay": "8.1 ms", "loss": "0.0 %"}}},
	}, func(r *http.Request) bool { u, p, _ := r.BasicAuth(); return u == "key" && p == "sec" })
	w, err = opnsense{}.Fetch(context.Background(), opn.instance(map[string]string{"key": "key", "secret": "sec"}))
	if s := stats(w); err != nil || s["RAM"] != "25 %" || s["Last"] != "0.21" || s["Updates"] != "25.1.5" || len(w.Items) != 1 {
		t.Fatalf("%v %v %+v", err, stats(w), w)
	}

	td := newFake(t, map[string]any{
		"POST /api/v2/stats/get-pies": obj{"pieStats": obj{"totalFiles": 500, "totalTranscodeCount": 120, "sizeDiff": 42.5, "status": obj{"transcode": []obj{{"name": "Queued", "value": 30}, {"name": "Transcode error", "value": 2}}}}},
		"GET /api/v2/get-nodes":       obj{"n1": obj{"nodeName": "Node1", "workers": obj{"w1": obj{"file": "/media/film.mkv", "percentage": 55.0, "fps": 120.0, "ETA": "00:10:00"}}}},
	}, nil)
	w, err = tdarr{}.Fetch(context.Background(), td.instance(nil))
	if s := stats(w); err != nil || s["Wartet"] != "30" || s["Fehler"] != "2" || s["Worker aktiv"] != "1" || w.Items[0].Title != "film.mkv" {
		t.Fatalf("%v %v %+v", err, stats(w), w)
	}

	ra := newFake(t, map[string]any{
		"GET /api/v1/queue":          obj{"totalRecords": 0, "records": []obj{}},
		"GET /api/v1/wanted/missing": obj{"totalRecords": 3},
		"GET /api/v1/health":         []obj{},
		"GET /api/v1/calendar":       []obj{{"title": "Neues Buch", "releaseDate": "2026-11-01T00:00:00Z", "author": obj{"authorName": "Autorin"}}},
		"GET /api/v1/author":         []obj{{"id": 4, "authorName": "Autorin", "titleSlug": "autorin", "monitored": true, "statistics": obj{"bookCount": 5, "bookFileCount": 2}}},
	}, nil)
	rin := ra.instance(map[string]string{"apiKey": "k"})
	w, err = readarr{}.Fetch(context.Background(), rin)
	if err != nil || stats(w)["Fehlende Bücher"] != "3" || len(w.Items) != 1 {
		t.Fatalf("%v %+v", err, w)
	}
	items, _ = readarr{}.Search(context.Background(), rin, "auto")
	if len(items) != 1 || len(items[0].Actions) != 1 || items[0].URL != "https://svc.example.com/author/autorin" {
		t.Fatalf("%+v", items)
	}
}
