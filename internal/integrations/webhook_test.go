package integrations

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWebhookButtons(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = append(got, r.Method+" "+r.URL.RequestURI()+" "+string(b)+" "+r.Header.Get("Content-Type"))
		if r.URL.Path == "/broken" {
			w.WriteHeader(500)
		}
	}))
	defer srv.Close()
	actions := "Licht an = " + srv.URL + "/relay/0?turn=on\n" +
		"Szene = POST " + srv.URL + "/hook | {\"scene\":\"kino\"}\n" +
		"kaputt\n" +
		"Fehler = " + srv.URL + "/broken\n" +
		"Datei = file:///etc/passwd"
	in := &Instance{Values: map[string]string{"actions": actions, "hookKey": "k0123456789abcdefXYZ"}, Client: srv.Client(), Lang: "de"}

	ReceiveHook("k0123456789abcdefXYZ", HookMessage{At: time.Now().Add(-time.Minute), Title: "Backup", Text: "fertig", Tone: "ok"})
	ReceiveHook("k0123456789abcdefXYZ", HookMessage{At: time.Now(), Title: "Server down", Tone: "bad"})
	w, err := webhook{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Actions) != 3 || w.Actions[0].Label != "Licht an" || w.Actions[2].Label != "Fehler" {
		t.Fatalf("buttons %+v", w.Actions)
	}
	if len(w.Items) != 2 || w.Items[0].Title != "Server down" || stats(w)["Meldungen (24 Std.)"] != "2" || len(w.Alerts) != 1 {
		t.Fatalf("messages %+v", w)
	}
	for n := range 2 {
		if err := (webhook{}).Action(context.Background(), in, "call", map[string]string{"n": string(rune('0' + n))}); err != nil {
			t.Fatal(err)
		}
	}
	if len(got) != 2 || got[0] != "GET /relay/0?turn=on  " || got[1] != `POST /hook {"scene":"kino"} application/json` {
		t.Fatalf("calls %q", got)
	}
	if err := (webhook{}).Action(context.Background(), in, "call", map[string]string{"n": "2"}); err == nil {
		t.Fatal("status 500 reported as success")
	}
	if err := (webhook{}).Action(context.Background(), in, "call", map[string]string{"n": "9"}); err == nil {
		t.Fatal("unknown button accepted")
	}
	if HookKeyMatches("short", "short") || !HookKeyMatches("k0123456789abcdefXYZ", "k0123456789abcdefXYZ") || HookKeyMatches("k0123456789abcdefXYZ", "k0123456789abcdefXYz") {
		t.Fatal("key check")
	}
}
