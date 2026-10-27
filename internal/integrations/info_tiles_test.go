package integrations

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// Info tiles without a service of their own (1.4): weather, feeds, calendar.

func TestWeather(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /v1/search": obj{"results": []obj{{"name": "Freiburg im Breisgau", "latitude": 47.99, "longitude": 7.84}}},
		"GET /v1/forecast": obj{
			"current": obj{"temperature_2m": 12.4, "apparent_temperature": 10.1, "relative_humidity_2m": 81, "weather_code": 61, "wind_speed_10m": 14, "wind_gusts_10m": 80, "precipitation": 0.4, "is_day": 1},
			"daily": obj{"time": []string{"2026-10-27", "2026-10-28", "2026-10-29"}, "weather_code": []int{61, 3, 0},
				"temperature_2m_max": []float64{13, 11, 9}, "temperature_2m_min": []float64{6, 4, -0.4}, "precipitation_probability_max": []float64{70, 30, 0},
				"sunrise": []string{"2026-10-27T07:31"}, "sunset": []string{"2026-10-27T17:24"}, "uv_index_max": []float64{1}},
		},
	}, nil)
	weatherGeoAPI, weatherAPI = f.URL, f.URL
	defer func() {
		weatherGeoAPI, weatherAPI = "https://geocoding-api.open-meteo.com", "https://api.open-meteo.com"
	}()
	w, err := weather{}.Fetch(context.Background(), f.instance(map[string]string{"place": "Freiburg", "days": "3"}))
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	if s["Leichter Regen"] != "🌦️ 12°C" || s["Sonne"] != "07:31 – 17:24" || s["Heute"] != "13° / 6°" || len(w.Items) != 2 ||
		w.Items[1].Badge != "9° / 0°" || !strings.Contains(w.ItemsTitle, "Freiburg") || len(w.Alerts) != 1 {
		t.Fatalf("%v %+v %v", s, w.Items, w.Alerts)
	}
}

func TestFeed(t *testing.T) {
	rss := `<?xml version="1.0" encoding="ISO-8859-1"?><rss version="2.0"><channel><title>Nachrichten</title>
<item><title>Alt &amp; gut</title><link>https://example.com/alt</link><pubDate>Mon, 02 Jan 2006 15:04:05 +0000</pubDate></item>
<item><title><![CDATA[<b>Neu</b> heute]]></title><guid>https://example.com/neu</guid><pubDate>` + time.Now().Add(-time.Hour).Format(time.RFC1123Z) + `</pubDate></item>
</channel></rss>`
	atom := `<?xml version="1.0" encoding="utf-8"?><feed xmlns="http://www.w3.org/2005/Atom"><title>Heimdeck releases</title>
<entry><title>v1.4.0</title><link rel="alternate" href="https://github.com/x/releases/v1.4.0"/><updated>` + time.Now().Add(-2*time.Hour).Format(time.RFC3339) + `</updated></entry></feed>`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/atom" {
			w.Write([]byte(atom))
			return
		}
		w.Write([]byte(rss))
	}))
	defer srv.Close()
	in := &Instance{Values: map[string]string{"feeds": srv.URL + "/rss\n" + srv.URL + "/atom", "count": "5"}, Client: srv.Client(), Lang: "de"}
	w, err := feed{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(w.Items) != 3 || w.Items[0].Title != "Neu heute" || w.Items[0].URL != "https://example.com/neu" || w.Items[1].Title != "v1.4.0" ||
		w.Items[2].Title != "Alt & gut" || stats(w)["Neu (24 Std.)"] != "2" || !strings.HasPrefix(w.Items[1].Subtitle, "Heimdeck releases") {
		t.Fatalf("%+v %v", w.Items, w.Stats)
	}
}

func TestCalendar(t *testing.T) {
	now := time.Now()
	day := func(d int) time.Time { return time.Date(now.Year(), now.Month(), now.Day()+d, 0, 0, 0, 0, time.Local) }
	ics := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\n" +
		// a birthday years ago, repeating yearly – falls on day+2
		"BEGIN:VEVENT\r\nSUMMARY:Geburtstag Anna\r\nDTSTART;VALUE=DATE:" + day(2).AddDate(-30, 0, 0).Format("20060102") + "\r\nRRULE:FREQ=YEARLY\r\nEND:VEVENT\r\n" +
		// tomorrow 18:00 Berlin, with a folded summary and an alarm
		"BEGIN:VEVENT\r\nSUMMARY:Eltern\r\n abend\r\nLOCATION:Schule\\, Raum 3\r\nDTSTART;TZID=Europe/Berlin:" + day(1).Format("20060102") + "T180000\r\n" +
		"BEGIN:VALARM\r\nSUMMARY:Erinnerung\r\nEND:VALARM\r\nEND:VEVENT\r\n" +
		// weekly, but this week's one is excluded
		"BEGIN:VEVENT\r\nSUMMARY:Training\r\nDTSTART:" + day(-14).Format("20060102") + "T170000\r\nRRULE:FREQ=WEEKLY\r\nEXDATE:" + day(0).Format("20060102") + "T170000\r\nEND:VEVENT\r\n" +
		// long gone
		"BEGIN:VEVENT\r\nSUMMARY:Vorbei\r\nDTSTART:20200101T100000Z\r\nEND:VEVENT\r\n" +
		"END:VCALENDAR\r\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(ics)) }))
	defer srv.Close()
	sonarr := newFake(t, map[string]any{
		"GET /api/v3/calendar": []obj{{"title": "Pilot", "seasonNumber": 1, "episodeNumber": 1, "airDateUtc": day(3).Add(20 * time.Hour).UTC().Format(time.RFC3339), "series": obj{"title": "Severance"}}},
	}, nil)
	in := &Instance{Values: map[string]string{"ical": "Familie = " + strings.Replace(srv.URL, "http://", "http://", 1), "releases": "true", "days": "7", "count": "20"},
		Client: srv.Client(), Lang: "de",
		Others: func(kind string) []*Instance {
			if kind == "sonarr" {
				return []*Instance{sonarr.instance(map[string]string{"apiKey": "k"})}
			}
			return nil
		}}
	w, err := calendar{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	titles := []string{}
	for _, it := range w.Items {
		titles = append(titles, it.Title)
	}
	got := strings.Join(titles, " | ")
	if got != "Elternabend | Geburtstag Anna | 📺 Severance · S01E01 | Training" {
		t.Fatalf("got %q %v", got, w.Alerts)
	}
	if w.Items[0].Badge != "Morgen" || !strings.Contains(w.Items[0].Subtitle, "Schule, Raum 3 · Familie") {
		t.Fatalf("%+v", w.Items[0])
	}
}
