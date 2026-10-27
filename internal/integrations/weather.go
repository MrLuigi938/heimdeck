package integrations

import (
	"context"
	"fmt"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// weather shows the current weather and a short forecast from Open-Meteo
// (open-meteo.com): free, without an account or key. The place is looked up
// by name once; the weather is fetched at most every ten minutes.

type weather struct{}

// The Open-Meteo addresses; variables so tests can point them elsewhere.
var (
	weatherGeoAPI = "https://geocoding-api.open-meteo.com"
	weatherAPI    = "https://api.open-meteo.com"
)

func init() { register(weather{}) }

func (weather) Def() Definition {
	return Definition{
		ID: "weather", Name: "Wetter", Icon: "🌤️", Category: "Allgemein",
		Description: "Aktuelles Wetter und Vorhersage für einen Ort – kostenlos über Open-Meteo, ohne Konto",
		NoURL:       true, HasWidget: true,
		Fields: []Field{
			{Key: "place", Label: "Ort", Type: "text", Required: true,
				Help: "Name des Orts, z. B. „Berlin“ oder „Freiburg im Breisgau“ – oder Koordinaten wie „52.52, 13.40“."},
			{Key: "days", Label: "Vorhersage", Type: "select", Options: []string{"3", "5", "7", "0"}, Display: true,
				Help: "Wie viele Tage die Kachel zeigt (0 = keine Vorhersage)."},
			{Key: "units", Label: "Einheiten", Type: "select", Options: []string{"°C", "°F"}, Display: true,
				Help: "°F zeigt auch Wind in mph und Regen in Zoll."},
		},
	}
}

type weatherPlace struct {
	Name     string
	Lat, Lon float64
}

var coordsRe = strings.NewReplacer(" ", "")

// place finds the coordinates of the configured place (cached for a day).
func (weather) place(ctx context.Context, in *Instance) (weatherPlace, error) {
	q := in.V("place")
	if lat, lon, ok := strings.Cut(coordsRe.Replace(q), ","); ok {
		la, err1 := strconv.ParseFloat(lat, 64)
		lo, err2 := strconv.ParseFloat(lon, 64)
		if err1 == nil && err2 == nil && math.Abs(la) <= 90 && math.Abs(lo) <= 180 {
			return weatherPlace{Name: q, Lat: la, Lon: lo}, nil
		}
	}
	return remember(in, "weather-place\x00"+in.Lang+"\x00"+strings.ToLower(q), 24*time.Hour, func() (weatherPlace, error) {
		geo := *in
		geo.BaseURL = weatherGeoAPI
		var res struct {
			Results []struct {
				Name      string  `json:"name"`
				Admin1    string  `json:"admin1"`
				Country   string  `json:"country"`
				Latitude  float64 `json:"latitude"`
				Longitude float64 `json:"longitude"`
			} `json:"results"`
		}
		if err := geo.do(ctx, request{path: "/v1/search", query: url.Values{"name": {q}, "count": {"1"}, "language": {in.Lang}}}, &res); err != nil {
			return weatherPlace{}, err
		}
		if len(res.Results) == 0 {
			return weatherPlace{}, in.Errorf("Ort „%s“ nicht gefunden – anders schreiben oder Koordinaten angeben", q)
		}
		r := res.Results[0]
		return weatherPlace{Name: r.Name, Lat: r.Latitude, Lon: r.Longitude}, nil
	})
}

type weatherData struct {
	Current struct {
		Temp     float64 `json:"temperature_2m"`
		Feels    float64 `json:"apparent_temperature"`
		Humidity float64 `json:"relative_humidity_2m"`
		Code     int     `json:"weather_code"`
		Wind     float64 `json:"wind_speed_10m"`
		Gusts    float64 `json:"wind_gusts_10m"`
		Precip   float64 `json:"precipitation"`
		IsDay    int     `json:"is_day"`
	} `json:"current"`
	Daily struct {
		Time    []string  `json:"time"`
		Code    []int     `json:"weather_code"`
		Max     []float64 `json:"temperature_2m_max"`
		Min     []float64 `json:"temperature_2m_min"`
		Rain    []float64 `json:"precipitation_probability_max"`
		Sunrise []string  `json:"sunrise"`
		Sunset  []string  `json:"sunset"`
		UV      []float64 `json:"uv_index_max"`
	} `json:"daily"`
}

func (w weather) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	p, err := w.place(ctx, in)
	if err != nil {
		return nil, err
	}
	imperial := in.V("units") == "°F"
	data, err := remember(in, fmt.Sprintf("weather\x00%.3f,%.3f,%v", p.Lat, p.Lon, imperial), 10*time.Minute, func() (weatherData, error) {
		api := *in
		api.BaseURL = weatherAPI
		q := url.Values{
			"latitude":      {strconv.FormatFloat(p.Lat, 'f', 4, 64)},
			"longitude":     {strconv.FormatFloat(p.Lon, 'f', 4, 64)},
			"current":       {"temperature_2m,apparent_temperature,relative_humidity_2m,weather_code,wind_speed_10m,wind_gusts_10m,precipitation,is_day"},
			"daily":         {"weather_code,temperature_2m_max,temperature_2m_min,precipitation_probability_max,sunrise,sunset,uv_index_max"},
			"timezone":      {"auto"},
			"forecast_days": {"8"},
		}
		if imperial {
			q.Set("temperature_unit", "fahrenheit")
			q.Set("wind_speed_unit", "mph")
			q.Set("precipitation_unit", "inch")
		}
		var d weatherData
		err := api.do(ctx, request{path: "/v1/forecast", query: q}, &d)
		return d, err
	})
	if err != nil {
		return nil, err
	}
	deg, speed, rain := "°C", "km/h", "mm"
	if imperial {
		deg, speed, rain = "°F", "mph", "in"
	}
	c := data.Current
	icon, text := weatherCode(in, c.Code, c.IsDay == 1)
	out := &Widget{Stats: []Stat{
		{Label: in.T(text), Value: icon + " " + temp(c.Temp, deg)},
		{Label: in.T("Gefühlt"), Value: temp(c.Feels, deg)},
		{Label: in.T("Wind"), Value: fmt.Sprintf("%.0f %s", c.Wind, speed), Tone: toneIf(c.Gusts >= 60 && !imperial || c.Gusts >= 37 && imperial, "warn")},
		{Label: in.T("Luftfeuchte"), Value: fmt.Sprintf("%.0f %%", c.Humidity)},
	}}
	if c.Precip > 0 {
		out.Stats = append(out.Stats, Stat{Label: in.T("Niederschlag"), Value: strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.1f", c.Precip), "0"), ".") + " " + rain})
	}
	d := data.Daily
	if len(d.Time) > 0 {
		if len(d.Rain) > 0 {
			out.Stats = append(out.Stats, Stat{Label: in.T("Regenrisiko heute"), Value: fmt.Sprintf("%.0f %%", d.Rain[0])})
		}
		if len(d.Max) > 0 && len(d.Min) > 0 {
			out.Stats = append(out.Stats, Stat{Label: in.T("Heute"), Value: temp(d.Max[0], "°") + " / " + temp(d.Min[0], "°")})
		}
		if len(d.Sunrise) > 0 && len(d.Sunset) > 0 {
			out.Stats = append(out.Stats, Stat{Label: in.T("Sonne"), Value: clock(d.Sunrise[0]) + " – " + clock(d.Sunset[0])})
		}
		if len(d.UV) > 0 && d.UV[0] >= 6 {
			out.Alerts = append(out.Alerts, in.T("Hoher UV-Index heute (%.0f) – Sonnenschutz nicht vergessen", d.UV[0]))
		}
	}
	if c.Gusts >= 75 && !imperial || c.Gusts >= 47 && imperial {
		out.Alerts = append(out.Alerts, in.T("Sturmböen bis %.0f %s", c.Gusts, speed))
	}
	days, _ := strconv.Atoi(in.V("days"))
	if in.V("days") == "" {
		days = 3
	}
	for i := 1; i <= days && i < len(d.Time); i++ {
		day, err := time.Parse("2006-01-02", d.Time[i])
		if err != nil || i >= len(d.Code) || i >= len(d.Max) || i >= len(d.Min) {
			continue
		}
		icon, text := weatherCode(in, d.Code[i], true)
		sub := icon + " " + in.T(text)
		if i < len(d.Rain) && d.Rain[i] >= 20 {
			sub += " · " + in.T("%.0f %% Regen", d.Rain[i])
		}
		out.Items = append(out.Items, Item{Title: weekday(in, day), Subtitle: sub, Badge: temp(d.Max[i], "°") + " / " + temp(d.Min[i], "°")})
	}
	if len(out.Items) > 0 {
		out.ItemsTitle = in.T("Vorhersage für %s", p.Name)
	}
	return out, nil
}

func temp(v float64, unit string) string {
	r := math.Round(v)
	if r == 0 {
		r = 0 // no "-0"
	}
	return strconv.Itoa(int(r)) + unit
}

// clock turns Open-Meteo's local "2026-10-27T07:31" into "07:31".
func clock(iso string) string {
	if _, t, ok := strings.Cut(iso, "T"); ok {
		return t
	}
	return iso
}

func weekday(in *Instance, d time.Time) string {
	de := []string{"So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"}
	en := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	if in.Lang == "en" {
		return en[d.Weekday()] + " " + d.Format("Jan 2")
	}
	return de[d.Weekday()] + " " + d.Format("02.01.")
}

// weatherCode turns a WMO weather code into a symbol and a German text.
func weatherCode(in *Instance, code int, day bool) (string, string) {
	sun, partly := "☀️", "⛅"
	if !day {
		sun, partly = "🌙", "☁️"
	}
	switch {
	case code == 0:
		return sun, "Klar"
	case code == 1:
		return partly, "Überwiegend klar"
	case code == 2:
		return partly, "Teils bewölkt"
	case code == 3:
		return "☁️", "Bewölkt"
	case code == 45 || code == 48:
		return "🌫️", "Nebel"
	case code >= 51 && code <= 57:
		return "🌦️", "Nieselregen"
	case code == 61 || code == 80:
		return "🌦️", "Leichter Regen"
	case code == 63 || code == 81:
		return "🌧️", "Regen"
	case code == 65 || code == 82:
		return "🌧️", "Starker Regen"
	case code == 66 || code == 67:
		return "🌧️", "Gefrierender Regen"
	case code >= 71 && code <= 77 || code == 85 || code == 86:
		return "🌨️", "Schnee"
	case code >= 95:
		return "⛈️", "Gewitter"
	}
	return "🌡️", "Wetter"
}
