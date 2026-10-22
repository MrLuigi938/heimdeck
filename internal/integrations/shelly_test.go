package integrations

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

func TestShellyGen1(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /shelly": obj{"type": "SHSW-PM", "mac": "AABB", "auth": true},
		"GET /status": obj{
			"relays": []obj{{"ison": true}}, "meters": []obj{{"power": 12.5, "total": 6000}},
			"temperature": 41.2, "wifi_sta": obj{"rssi": -61}, "update": obj{"has_update": true, "new_version": "1.14.0"},
		},
		"GET /settings": obj{"relays": []obj{{"name": "Heizungsraum"}}},
		"GET /relay/0":  obj{"ison": false},
		"GET /roller/0": obj{},
	}, func(r *http.Request) bool {
		if r.URL.Path == "/shelly" {
			return true
		}
		u, p, ok := r.BasicAuth()
		return ok && u == "admin" && p == "pw"
	})
	in := f.instance(map[string]string{"password": "pw"})
	in.BaseURL = f.URL + "/" // the cache is per address
	w, err := shelly{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	if s["Leistung"] != "12,5 W" || s["Energie"] != "0,10 kWh" || s["Temperatur"] != "41,2 °C" || s["WLAN"] != "-61 dBm" {
		t.Fatalf("stats %v", s)
	}
	if len(w.Items) != 1 || w.Items[0].Title != "Heizungsraum" || w.Items[0].Badge != "An" || len(w.Alerts) != 1 {
		t.Fatalf("items %+v alerts %v", w.Items, w.Alerts)
	}
	if err := (shelly{}).Action(context.Background(), in, "set", w.Items[0].Actions[0].Params); err != nil {
		t.Fatal(err)
	}
	if err := (shelly{}).Action(context.Background(), in, "set", map[string]string{"kind": "switch", "id": "0", "op": "delete"}); err == nil {
		t.Fatal("unknown operation accepted")
	}
	noPw := f.instance(map[string]string{})
	noPw.BaseURL = f.URL + "/"
	if _, err := (shelly{}).Fetch(context.Background(), noPw); err == nil || !strings.Contains(err.Error(), "Passwort") {
		t.Fatalf("missing password: %v", err)
	}
}

func TestShellyGen2Digest(t *testing.T) {
	const pass = "geheim"
	var calls []string
	realm, nonce := "shellyplus1pm-a8032ab12345", "1700000000"
	h := func(s string) string { sum := sha256.Sum256([]byte(s)); return hex.EncodeToString(sum[:]) }
	param := regexp.MustCompile(`(\w+)="?([^",]*)"?`)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/shelly" {
			json.NewEncoder(w).Encode(obj{"id": "shellyplus1pm-a8032ab12345", "gen": 2, "auth_en": true})
			return
		}
		p := map[string]string{}
		for _, m := range param.FindAllStringSubmatch(r.Header.Get("Authorization"), -1) {
			p[m[1]] = m[2]
		}
		want := h(h("admin:"+realm+":"+pass) + ":" + nonce + ":" + p["nc"] + ":" + p["cnonce"] + ":auth:" + h(r.Method+":"+r.URL.RequestURI()))
		if p["response"] != want || p["username"] != "admin" {
			w.Header().Set("WWW-Authenticate", `Digest qop="auth", realm="`+realm+`", nonce="`+nonce+`", algorithm=SHA-256`)
			w.WriteHeader(401)
			return
		}
		calls = append(calls, r.URL.RequestURI())
		switch r.URL.Path {
		case "/rpc/Shelly.GetStatus":
			json.NewEncoder(w).Encode(obj{
				"switch:0": obj{"output": false, "apower": 0, "aenergy": obj{"total": 2500.0}, "temperature": obj{"tC": 38.0}},
				"cover:0":  obj{"state": "stopped", "current_pos": 40, "apower": 3.5},
				"wifi":     obj{"rssi": -84},
				"sys":      obj{"available_updates": obj{}},
			})
		case "/rpc/Shelly.GetConfig":
			json.NewEncoder(w).Encode(obj{"switch:0": obj{"name": nil}, "cover:0": obj{"name": "Wohnzimmer"}})
		default:
			json.NewEncoder(w).Encode(obj{})
		}
	}))
	defer srv.Close()
	in := &Instance{BaseURL: srv.URL, Values: map[string]string{"password": pass}, Client: srv.Client(), Lang: "de"}
	w, err := shelly{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	if s["Leistung"] != "3,5 W" || s["Energie"] != "2,50 kWh" || s["WLAN"] != "-84 dBm" || len(w.Alerts) != 0 {
		t.Fatalf("stats %v alerts %v", s, w.Alerts)
	}
	if len(w.Items) != 2 || w.Items[0].Title != "Rollladen" && w.Items[0].Title != "Wohnzimmer" {
		t.Fatalf("items %+v", w.Items)
	}
	var cover Item
	for _, it := range w.Items {
		if it.Title == "Wohnzimmer" {
			cover = it
		}
	}
	if cover.Badge != "40 %" || len(cover.Actions) != 3 {
		t.Fatalf("cover %+v", cover)
	}
	if err := (shelly{}).Action(context.Background(), in, "set", cover.Actions[2].Params); err != nil {
		t.Fatal(err)
	}
	if last := calls[len(calls)-1]; last != "/rpc/Cover.Close?id=0" {
		t.Fatalf("action call %s", last)
	}
	in.Values["password"] = "falsch"
	if _, err := (shelly{}).Fetch(context.Background(), in); err == nil || !strings.Contains(err.Error(), "falsch") {
		t.Fatalf("wrong password: %v", err)
	}
}
