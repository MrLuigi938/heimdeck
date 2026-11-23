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

// qBittorrent 5.2: login answers 204 without text, wrong credentials 401; an
// API key (Bearer) needs no login at all.
func TestQBittorrent52(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v2/auth/login" {
			r.ParseForm()
			if r.Form.Get("password") != "pw" {
				w.WriteHeader(401)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "QBT_SID_8080", Value: "abc"})
			w.WriteHeader(204)
			return
		}
		c, err := r.Cookie("QBT_SID_8080")
		if r.Header.Get("Authorization") != "Bearer qbt_key" && (err != nil || c.Value != "abc") {
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/v2/transfer/info":
			json.NewEncoder(w).Encode(obj{"dl_info_speed": 0, "up_info_speed": 0, "connection_status": "connected"})
		case "/api/v2/torrents/info":
			json.NewEncoder(w).Encode([]obj{})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	in := inst(srv, map[string]string{"username": "admin52", "password": "pw"})
	sessions.Delete(sessionKey(in, "qbt"))
	if _, err := (qbittorrent{}).Fetch(context.Background(), in); err != nil {
		t.Fatalf("login with 204: %v", err)
	}
	bad := inst(srv, map[string]string{"username": "admin52b", "password": "x"})
	sessions.Delete(sessionKey(bad, "qbt"))
	if _, err := (qbittorrent{}).Fetch(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "falsch") {
		t.Fatalf("wrong password: %v", err)
	}
	key := inst(srv, map[string]string{"apiKey": "qbt_key"})
	if _, err := (qbittorrent{}).Fetch(context.Background(), key); err != nil {
		t.Fatalf("api key: %v", err)
	}
	key.Values["apiKey"] = "qbt_wrong"
	if _, err := (qbittorrent{}).Fetch(context.Background(), key); err == nil || !strings.Contains(err.Error(), "API-Key") {
		t.Fatalf("wrong api key: %v", err)
	}
}

// Paperless-ngx 3.x: Heimdeck asks for API version 9; should a server answer
// with version 10 anyway (paged, lower case, file name in input_data), it still counts.
func TestPaperlessTasksVersions(t *testing.T) {
	var accept string
	tasks := any([]obj{{"status": "FAILURE", "task_file_name": "kaputt.pdf", "acknowledged": false}, {"status": "FAILURE", "task_file_name": "alt.pdf", "acknowledged": true}})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/statistics/":
			json.NewEncoder(w).Encode(obj{"documents_total": 1})
		case "/api/tasks/":
			accept = r.Header.Get("Accept")
			json.NewEncoder(w).Encode(tasks)
		case "/api/documents/":
			json.NewEncoder(w).Encode(obj{"results": []obj{}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	in := inst(srv, map[string]string{"apiKey": "k"})
	w, err := paperless{}.Fetch(context.Background(), in)
	if err != nil || stats(w)["Import-Fehler"] != "1" || len(w.Alerts) != 1 || !strings.Contains(w.Alerts[0], "kaputt.pdf") {
		t.Fatalf("v9: %v %+v", err, w)
	}
	if accept != "application/json; version=9" {
		t.Errorf("Accept %q", accept)
	}
	tasks = obj{"count": 1, "results": []obj{{"status": "failure", "input_data": obj{"filename": "neu.pdf"}, "acknowledged": false}}}
	w, err = paperless{}.Fetch(context.Background(), in)
	if err != nil || stats(w)["Import-Fehler"] != "1" || !strings.Contains(strings.Join(w.Alerts, ""), "neu.pdf") {
		t.Fatalf("v10: %v %+v", err, w)
	}
}

// Jellyfin 12 no longer accepts X-Emby-Token; posters use the Authorization header.
func TestJellyfinImageAuth(t *testing.T) {
	in := &Instance{Values: map[string]string{"apiKey": "k"}, Lang: "de"}
	_, h, err := jellyfin{}.ImageRequest(context.Background(), in, "/Items/abc/Images/Primary?maxHeight=120")
	if err != nil || h["Authorization"] != `MediaBrowser Client="Heimdeck", Token="k"` || h["X-Emby-Token"] != "" {
		t.Fatalf("jellyfin %v %v", h, err)
	}
	_, h, err = jellyfin{emby: true}.ImageRequest(context.Background(), in, "/emby/Items/abc/Images/Primary")
	if err != nil || h["X-Emby-Token"] != "k" {
		t.Fatalf("emby %v %v", h, err)
	}
}

// Immich: background jobs from /queues (2.4+) or /jobs (older); searches ask
// for the timeline only and drop that field for versions that refuse it.
func TestImmichQueuesAndVisibility(t *testing.T) {
	var bodies []string
	newQueues := true
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/queues":
			if !newQueues {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode([]obj{{"name": "thumbnailGeneration", "statistics": obj{"active": 1, "waiting": 4, "failed": 2}}})
		case "/api/jobs":
			json.NewEncoder(w).Encode(obj{"thumbnailGeneration": obj{"jobCounts": obj{"active": 0, "waiting": 3, "failed": 0}}})
		case "/api/search/metadata":
			b, _ := io.ReadAll(r.Body)
			bodies = append(bodies, string(b))
			if strings.Contains(string(b), "visibility") && !newQueues {
				w.WriteHeader(400)
				return
			}
			json.NewEncoder(w).Encode(obj{"assets": obj{"items": []obj{}}})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	in := inst(srv, map[string]string{"apiKey": "k"})
	open, failed, ok := immich{}.jobs(context.Background(), in)
	if !ok || open != 5 || failed != 2 {
		t.Fatalf("queues %d %d %v", open, failed, ok)
	}
	var res immichAssets
	if err := (immich{}).search(context.Background(), in, "/search/metadata", map[string]any{"size": 6}, &res); err != nil || !strings.Contains(bodies[0], `"visibility":"timeline"`) {
		t.Fatalf("%v %v", err, bodies)
	}
	newQueues = false
	open, _, ok = immich{}.jobs(context.Background(), in)
	if !ok || open != 3 {
		t.Fatalf("jobs %d %v", open, ok)
	}
	bodies = nil
	if err := (immich{}).search(context.Background(), in, "/search/metadata", map[string]any{"size": 6}, &res); err != nil || len(bodies) != 2 || strings.Contains(bodies[1], "visibility") {
		t.Fatalf("old immich: %v %v", err, bodies)
	}
}
