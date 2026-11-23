package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A hive as Hivornet's web API answers: login with a session cookie, then the
// summary, the top countries and the latest events.
func TestHivornet(t *testing.T) {
	var logins atomic.Int32
	valid := "a" + strings.Repeat("b", 63)
	var expired atomic.Bool
	now := time.Now().UnixMilli()
	hive := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/login" {
			var req struct{ Username, Password string }
			json.NewDecoder(r.Body).Decode(&req)
			if req.Username != "heimdeck" || req.Password != "pw" {
				w.WriteHeader(401)
				io.WriteString(w, `{"error":"Benutzername oder Passwort falsch"}`)
				return
			}
			logins.Add(1)
			http.SetCookie(w, &http.Cookie{Name: "hv_session", Value: valid, HttpOnly: true})
			io.WriteString(w, `{"username":"heimdeck"}`)
			return
		}
		if c, err := r.Cookie("hv_session"); err != nil || c.Value != valid || expired.Load() {
			expired.Store(false)
			w.WriteHeader(401)
			return
		}
		switch r.URL.Path {
		case "/api/me":
			io.WriteString(w, `{"username":"heimdeck","role":"viewer"}`)
		case "/api/stats/summary":
			if r.URL.Query().Get("range") != "7d" {
				w.WriteHeader(400)
				return
			}
			io.WriteString(w, `{"events":5000,"connect":4200,"login":610,"command":95,"download":3,"request":90,"unique_ips":312,"countries":41,"active_tarpits":2}`)
		case "/api/stats/series":
			fmt.Fprintf(w, `{"from":%d,"bucket_hours":6,"series":{"connect":[10,20,30,40],"login":[1,2,3,4],"command":[0,1,0,2],"download":[0,0,1,0],"ips":[5,6,7,8]},"totals":{"connect":4200},"prev":{"connect":3500}}`, now-4*6*3600000)
		case "/api/stats/top":
			io.WriteString(w, `[{"key":"CN","label":"China","n":1200},{"key":"US","label":"USA","n":800}]`)
		case "/api/events":
			if r.URL.Query().Get("hide_noise") != "1" {
				w.WriteHeader(400)
				return
			}
			if q := r.URL.Query().Get("q"); q != "" {
				fmt.Fprintf(w, `[{"ts":%d,"service":"ssh","type":"login","src_ip":%q,"dst_port":22,"username":"root","password":"admin","country":"CN","country_name":"China"}]`, now, q)
				return
			}
			fmt.Fprintf(w, `[
				{"ts":%d,"service":"ssh","type":"command","src_ip":"203.0.113.9","dst_port":22,"data":"{\"input\":\"uname -a\"}","country":"DE","country_name":"Germany"},
				{"ts":%d,"service":"ssh","type":"login","src_ip":"198.51.100.4","dst_port":22,"username":"root","password":"123456","data":{"success":0},"country":"CN","country_name":"China"},
				{"ts":%d,"service":"http","type":"download","src_ip":"192.0.2.77","dst_port":80,"data":{"url":"http://bad.example/x.sh"},"country":""}
			]`, now-60000, now-120000, now-180000)
		default:
			w.WriteHeader(404)
		}
	}))
	defer hive.Close()
	in := &Instance{BaseURL: hive.URL, PublicURL: hive.URL, Values: map[string]string{"username": "heimdeck", "password": "pw", "range": "7d", "showCountries": "true"}, Client: hive.Client(), Lang: "de"}

	if err := (hivornet{}).Test(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	w, err := hivornet{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	for k, v := range map[string]string{"Verbindungen (7 Tage)": "4.200", "Logins": "610", "Befehle": "95", "Downloads": "3", "Angreifer": "312", "Länder": "41", "Im Tarpit": "2"} {
		if s[k] != v {
			t.Errorf("%s = %q, want %q (all: %v)", k, s[k], v, s)
		}
	}
	if !strings.Contains(s["Häufigste Länder"], "CN 1.200") {
		t.Errorf("countries: %q", s["Häufigste Länder"])
	}
	// Charts: attacks stacked per six hours with the comparison, and the countries.
	if len(w.Charts) != 2 {
		t.Fatalf("charts: %+v", w.Charts)
	}
	at := w.Charts[0]
	if at.ID != "attacks" || at.Kind != "stacked" || at.Title != "Angriffe je 6 Stunden" || len(at.Times) != 4 || len(at.Series) != 4 ||
		at.Series[0].Values[3] != 40 || at.Step != 6*3600000 || !strings.Contains(at.Note, "+20 %") {
		t.Errorf("attack chart: %+v", at)
	}
	if co := w.Charts[1]; co.Kind != "bar" || len(co.Labels) != 2 || co.Series[0].Values[0] != 1200 {
		t.Errorf("country chart: %+v", co)
	}
	if len(w.Items) != 3 || w.Items[0].Title != "203.0.113.9 · ssh :22" || !strings.Contains(w.Items[0].Subtitle, "$ uname -a") ||
		!strings.Contains(w.Items[1].Subtitle, "Login root / 123456") || !strings.Contains(w.Items[2].Subtitle, "bad.example") {
		t.Fatalf("items: %+v", w.Items)
	}
	if logins.Load() != 1 {
		t.Fatalf("logged in %d times – the session should be kept", logins.Load())
	}
	// The session ran out: one new login, then it goes on.
	expired.Store(true)
	if _, err := (hivornet{}).Fetch(context.Background(), in); err != nil || logins.Load() != 2 {
		t.Fatalf("after the session ran out: %v, %d logins", err, logins.Load())
	}
	// Search by IP.
	items, err := hivornet{}.Search(context.Background(), in, "198.51.100.4")
	if err != nil || len(items) != 1 || !strings.HasPrefix(items[0].Title, "198.51.100.4") {
		t.Fatalf("search: %v %+v", err, items)
	}
	// A wrong password says so.
	bad := &Instance{BaseURL: hive.URL, Values: map[string]string{"username": "heimdeck", "password": "nope"}, Client: hive.Client(), Lang: "de"}
	if err := (hivornet{}).Test(context.Background(), bad); err == nil || !strings.Contains(err.Error(), "falsch") {
		t.Fatalf("wrong password: %v", err)
	}
}
