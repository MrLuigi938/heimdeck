package integrations

import (
	"context"
	"testing"
)

// The weather block gets the hours from now on, the days after today and one
// sentence about the rain to come.
func TestWeatherView(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /v1/forecast": obj{
			"current": obj{"time": "2026-11-17T14:45", "temperature_2m": 8.2, "apparent_temperature": 5.1, "weather_code": 3, "is_day": 1, "precipitation": 0},
			"daily": obj{"time": []string{"2026-11-17", "2026-11-18"}, "weather_code": []int{3, 63},
				"temperature_2m_max": []float64{11, 9}, "temperature_2m_min": []float64{3, 2}, "precipitation_probability_max": []float64{60, 90},
				"sunrise": []string{"2026-11-17T07:31"}, "sunset": []string{"2026-11-17T16:05"}},
			"hourly": obj{"time": []string{"2026-11-17T13:00", "2026-11-17T14:00", "2026-11-17T15:00", "2026-11-17T16:00"},
				"temperature_2m": []float64{8, 8.2, 7.9, 7}, "precipitation_probability": []float64{0, 10, 20, 80},
				"precipitation": []float64{0, 0, 0, 1.2}, "weather_code": []int{3, 3, 3, 63}, "is_day": []int{1, 1, 1, 0}},
			"minutely_15": obj{"time": []string{"2026-11-17T14:45", "2026-11-17T15:00", "2026-11-17T15:15", "2026-11-17T15:30"},
				"precipitation": []float64{0, 0, 0.2, 0.4}},
		},
	}, nil)
	weatherGeoAPI, weatherAPI = f.URL, f.URL
	defer func() {
		weatherGeoAPI, weatherAPI = "https://geocoding-api.open-meteo.com", "https://api.open-meteo.com"
	}()
	w, err := weather{}.Fetch(context.Background(), f.instance(map[string]string{"place": "52.5, 13.4"}))
	if err != nil {
		t.Fatal(err)
	}
	v := w.Weather
	if v == nil || v.Kind != "cloudy" || !v.Day || v.Sunset != "16:05" || *v.High != 11 {
		t.Fatalf("now: %+v", v)
	}
	if len(v.Hours) != 3 || v.Hours[0].Time != "14:00" || v.Hours[2].Kind != "rain" || v.Hours[2].Day {
		t.Fatalf("hours: %+v", v.Hours)
	}
	if len(v.Days) != 1 || v.Days[0].Name != "Mi" || v.Days[0].Kind != "rain" || v.Days[0].Chance != 90 {
		t.Fatalf("days: %+v", v.Days)
	}
	if v.Say != "Regen ab etwa 15:15" {
		t.Fatalf("say: %q", v.Say)
	}
}
