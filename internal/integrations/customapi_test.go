package integrations

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	var data any
	json.Unmarshal([]byte(`{"data":{"temp":21.456,"items":[{"name":"a","state":"up","size":10},{"name":"b","state":"down","size":30},{"name":"c","state":"down","size":5}]},"ok":true,"tags":["x","y"]}`), &data)
	cases := map[string]string{
		"data.temp":                       "21.456",
		"data.items[0].name":              "a",
		"data.items.1.name":               "b",
		"data.items[-1].name":             "c",
		"data.items.#":                    "3",
		"data.items[state=down].#":        "2",
		"data.items[size>8].#":            "2",
		"data.items[state!=down][0].name": "a",
		"tags.#":                          "2",
		"ok":                              "true",
	}
	for p, want := range cases {
		v, ok := lookup(data, p)
		if !ok {
			t.Errorf("%s: not found", p)
			continue
		}
		got := strings.TrimSuffix(strings.TrimSuffix(jsonString(v), "\n"), "")
		if got != want {
			t.Errorf("%s: got %s, want %s", p, got, want)
		}
	}
	if _, ok := lookup(data, "data.nope.x"); ok {
		t.Error("missing path found")
	}
}

func jsonString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func TestCustomAPI(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /api/status": obj{"cpu": 0.423, "uptime": 93784, "disk": obj{"used": 5.5e11}, "status": "up", "online": true,
			"devices": []obj{{"name": "NAS", "online": true, "ip": "10.0.0.2", "load": 42}, {"name": "Pi", "online": false, "ip": "10.0.0.3", "load": 0}}},
		"POST /api/restart": obj{"ok": true},
		"GET /api/find":     obj{"results": []obj{{"name": "Treffer 1", "path": "/x/1"}}},
		"GET /version":      "1.2.3",
	}, func(r *http.Request) bool { return r.Header.Get("X-Api-Key") == "geheim" || r.URL.Path == "/version" })
	in := f.instance(map[string]string{
		"path":    "/api/status",
		"headers": "X-Api-Key: geheim\n",
		"stats": `CPU = cpu | anteil
Laufzeit = uptime | dauer
Belegt = disk.used | bytes
Status = status | text ok=up bad=down
Geräte online = devices[online=true].#
# Kommentar
kaputt`,
		"list":    "devices",
		"item":    "title = name\nsubtitle = ip + \"Last\"\nbadge = load | prozent\nprogress = load\nurl = \"/devices\"",
		"buttons": "Neu starten = POST /api/restart {\"force\":true}",
		"search":  "/api/find?q={q}", "searchList": "results",
	})
	w, err := customAPI{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	if s["CPU"] != "42 %" || s["Laufzeit"] != "26:03 h" || s["Status"] != "up" || s["Geräte online"] != "1" || !strings.Contains(s["Belegt"], "GB") {
		t.Fatalf("stats %v", s)
	}
	if w.Stats[3].Tone != "ok" || len(w.Alerts) != 1 {
		t.Fatalf("tone/alerts %+v %v", w.Stats[3], w.Alerts)
	}
	if len(w.Items) != 2 || w.Items[0].Subtitle != "10.0.0.2 · Last" || w.Items[0].Badge != "42 %" || *w.Items[0].Progress != 42 {
		t.Fatalf("items %+v", w.Items[0])
	}
	if len(w.Actions) != 1 {
		t.Fatal("buttons")
	}
	if err := (customAPI{}).Action(context.Background(), in, "button", w.Actions[0].Params); err != nil {
		t.Fatal(err)
	}
	if b := f.body("POST /api/restart"); b != `{"force":true}` {
		t.Fatalf("body %q", b)
	}
	items, err := customAPI{}.Search(context.Background(), in, "tr")
	if err != nil || len(items) != 1 || items[0].Title != "Treffer 1" {
		t.Fatalf("search %v %+v", err, items)
	}
	// Plain text answer and nothing configured: show a preview.
	plain := f.instance(map[string]string{"path": "/version"})
	w, err = customAPI{}.Fetch(context.Background(), plain)
	if err != nil || len(w.Alerts) != 1 || !strings.Contains(w.Alerts[0], "1.2.3") {
		t.Fatalf("plain %v %+v", err, w)
	}
	plain.Values["stats"] = "Version = ."
	w, _ = customAPI{}.Fetch(context.Background(), plain)
	if stats(w)["Version"] != "1.2.3" {
		t.Fatalf("plain stat %+v", w)
	}
	if _, err := (customAPI{}).Search(context.Background(), plain, "x"); err != ErrNoSearch {
		t.Fatal("search without config")
	}
}
