package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/heimdeck/heimdeck/internal/auth"
	"github.com/heimdeck/heimdeck/internal/integrations"
	"github.com/heimdeck/heimdeck/internal/secret"
	"github.com/heimdeck/heimdeck/internal/store"
)

// A weather block with a place of its own gets the weather without any
// weather tile; without a place or without a login there is nothing.
func TestBlockWeather(t *testing.T) {
	asked := 0
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked++
		if !strings.HasPrefix(r.URL.Path, "/v1/forecast") {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"current":{"time":"2026-11-26T14:45","temperature_2m":-1.2,"apparent_temperature":-4,"weather_code":3,"is_day":1,"precipitation":0},` +
			`"daily":{"time":["2026-11-26","2026-11-27"],"weather_code":[3,71],"temperature_2m_max":[3,1],"temperature_2m_min":[-3,-4],"precipitation_probability_max":[10,70],"sunrise":["2026-11-26T07:50"],"sunset":["2026-11-26T16:05"]},` +
			`"hourly":{"time":["2026-11-26T14:00","2026-11-26T15:00"],"temperature_2m":[-1,-2],"precipitation_probability":[0,10],"precipitation":[0,0],"weather_code":[3,3],"is_day":[1,1]}}`))
	}))
	t.Cleanup(fake.Close)
	t.Cleanup(integrations.UseWeatherAPIs(fake.URL, fake.URL))

	dir := t.TempDir()
	box, _ := secret.Open(dir)
	st, err := store.Open(filepath.Join(dir, "test.db"), box)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := httptest.NewServer(New(st, auth.New(st, false), nil, nil).Handler())
	t.Cleanup(srv.Close)

	stranger := newTestClient(t, srv.URL)
	if code, _ := stranger.call("GET", "/api/blocks/weather?place=52.5,13.4", nil); code != 401 {
		t.Fatalf("without a login: %d", code)
	}

	luis := newTestClient(t, srv.URL)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "en"})
	if code, _ := luis.call("GET", "/api/blocks/weather?place=", nil); code != 400 {
		t.Fatalf("without a place: %d", code)
	}
	code, body := luis.call("GET", "/api/blocks/weather?place="+url.QueryEscape("52.5, 13.4"), nil)
	if code != 200 {
		t.Fatalf("weather: %d %v", code, body)
	}
	wd, _ := body["widget"].(map[string]any)
	weather, _ := wd["weather"].(map[string]any)
	if weather == nil || weather["kind"] != "cloudy" {
		t.Fatalf("no weather for the block: %v", body)
	}
	// The same place again comes from the cache (at most every ten minutes).
	before := asked
	luis.call("GET", "/api/blocks/weather?place="+url.QueryEscape("52.5, 13.4"), nil)
	if asked != before {
		t.Fatalf("asked Open-Meteo again within ten minutes")
	}
}
