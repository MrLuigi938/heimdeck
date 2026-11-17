package integrations

import (
	"math"
	"strings"
	"time"
)

// WeatherView is the weather for the weather block on the page: now, the next
// 24 hours, the coming days and one sentence about what is coming ("Rain until
// about 17:00"). Temperatures are rounded on the page, not here.
type WeatherView struct {
	Place    string        `json:"place"`
	Unit     string        `json:"unit"`  // °C or °F
	Speed    string        `json:"speed"` // km/h or mph
	Temp     float64       `json:"temp"`
	Feels    float64       `json:"feels"`
	Humidity float64       `json:"humidity"`
	Wind     float64       `json:"wind"`
	Gusts    float64       `json:"gusts"`
	Kind     string        `json:"kind"` // clear, partly, cloudy, fog, drizzle, rain, heavy, snow, storm
	Day      bool          `json:"day"`
	Text     string        `json:"text"`
	High     *float64      `json:"high,omitempty"`
	Low      *float64      `json:"low,omitempty"`
	Chance   *float64      `json:"chance,omitempty"` // chance of rain today, percent
	Sunrise  string        `json:"sunrise,omitempty"`
	Sunset   string        `json:"sunset,omitempty"`
	Say      string        `json:"say,omitempty"`
	Hours    []WeatherHour `json:"hours,omitempty"`
	Days     []WeatherDay  `json:"days,omitempty"`
}

type WeatherHour struct {
	Time   string  `json:"time"` // "15:00", local time of the place
	Temp   float64 `json:"temp"`
	Chance float64 `json:"chance"`
	Precip float64 `json:"precip"`
	Kind   string  `json:"kind"`
	Day    bool    `json:"day"`
}

type WeatherDay struct {
	Name   string  `json:"name"` // "Mi", "Wed"
	Kind   string  `json:"kind"`
	Text   string  `json:"text"`
	High   float64 `json:"high"`
	Low    float64 `json:"low"`
	Chance float64 `json:"chance"`
}

// weatherKind sorts a WMO weather code into the few pictures the block draws.
func weatherKind(code int) string {
	switch {
	case code == 0:
		return "clear"
	case code == 1 || code == 2:
		return "partly"
	case code == 3:
		return "cloudy"
	case code == 45 || code == 48:
		return "fog"
	case code >= 51 && code <= 57:
		return "drizzle"
	case code == 65 || code == 82 || code == 66 || code == 67:
		return "heavy"
	case code >= 61 && code <= 64 || code == 80 || code == 81:
		return "rain"
	case code >= 71 && code <= 77 || code == 85 || code == 86:
		return "snow"
	case code >= 95:
		return "storm"
	}
	return "cloudy"
}

func at[T any](list []T, i int) (T, bool) {
	var zero T
	if i < 0 || i >= len(list) {
		return zero, false
	}
	return list[i], true
}

func weatherView(in *Instance, p weatherPlace, d *weatherData, deg, speed string) *WeatherView {
	c := d.Current
	_, text := weatherCode(in, c.Code, c.IsDay == 1)
	v := &WeatherView{Place: p.Name, Unit: deg, Speed: speed, Temp: c.Temp, Feels: c.Feels, Humidity: c.Humidity, Wind: c.Wind, Gusts: c.Gusts,
		Kind: weatherKind(c.Code), Day: c.IsDay == 1, Text: in.T(text)}
	if x, ok := at(d.Daily.Max, 0); ok {
		v.High = &x
	}
	if x, ok := at(d.Daily.Min, 0); ok {
		v.Low = &x
	}
	if x, ok := at(d.Daily.Rain, 0); ok {
		v.Chance = &x
	}
	if x, ok := at(d.Daily.Sunrise, 0); ok {
		v.Sunrise = clock(x)
	}
	if x, ok := at(d.Daily.Sunset, 0); ok {
		v.Sunset = clock(x)
	}
	// Hours from the current one on (Open-Meteo starts with the current hour).
	h := d.Hourly
	now := c.Time
	if len(now) >= 13 {
		now = now[:13] + ":00"
	}
	for i, tm := range h.Time {
		if tm < now || len(v.Hours) >= 25 {
			continue
		}
		temp, ok1 := at(h.Temp, i)
		code, ok2 := at(h.Code, i)
		if !ok1 || !ok2 {
			continue
		}
		chance, _ := at(h.Chance, i)
		precip, _ := at(h.Precip, i)
		day, _ := at(h.IsDay, i)
		v.Hours = append(v.Hours, WeatherHour{Time: clock(tm), Temp: temp, Chance: chance, Precip: precip, Kind: weatherKind(code), Day: day == 1})
	}
	for i := 1; i < len(d.Daily.Time) && len(v.Days) < 7; i++ {
		day, err := time.Parse("2006-01-02", d.Daily.Time[i])
		code, ok1 := at(d.Daily.Code, i)
		hi, ok2 := at(d.Daily.Max, i)
		lo, ok3 := at(d.Daily.Min, i)
		if err != nil || !ok1 || !ok2 || !ok3 {
			continue
		}
		chance, _ := at(d.Daily.Rain, i)
		_, text := weatherCode(in, code, true)
		name, _, _ := strings.Cut(weekday(in, day), " ")
		v.Days = append(v.Days, WeatherDay{Name: name, Kind: weatherKind(code), Text: in.T(text), High: hi, Low: lo, Chance: chance})
	}
	v.Say = weatherSay(in, d, v)
	return v
}

// weatherSay finds the one thing worth saying about the next hours: when rain
// starts or ends, frost tonight, storm gusts. Empty when nothing stands out.
func weatherSay(in *Instance, d *weatherData, v *WeatherView) string {
	wet := func(mm float64) bool { return mm >= 0.1 }
	m := d.Minutely
	rainingNow := wet(d.Current.Precip)
	if len(m.Precip) > 0 {
		rainingNow = rainingNow || wet(m.Precip[0])
	}
	if rainingNow {
		// When does it stop: first dry quarter hour, then first dry hour.
		for i := 1; i < len(m.Precip) && i < len(m.Time); i++ {
			if !wet(m.Precip[i]) && (i+1 >= len(m.Precip) || !wet(m.Precip[i+1])) {
				return in.T("Regen hört gegen %s auf", clock(m.Time[i]))
			}
		}
		for i := 1; i < len(v.Hours) && i <= 12; i++ {
			if !wet(v.Hours[i].Precip) && v.Hours[i].Chance < 40 {
				return in.T("Regen bis etwa %s", v.Hours[i].Time)
			}
		}
		return in.T("Regen in den nächsten Stunden")
	}
	for i := 1; i < len(m.Precip) && i < len(m.Time); i++ {
		if wet(m.Precip[i]) {
			return in.T("Regen ab etwa %s", clock(m.Time[i]))
		}
	}
	for i := 1; i < len(v.Hours) && i <= 12; i++ {
		if wet(v.Hours[i].Precip) && v.Hours[i].Chance >= 50 {
			if v.Hours[i].Kind == "snow" {
				return in.T("Schnee ab etwa %s", v.Hours[i].Time)
			}
			return in.T("Regen ab etwa %s", v.Hours[i].Time)
		}
	}
	freezing := 0.0
	if v.Unit == "°F" {
		freezing = 32
	}
	if v.Temp > freezing {
		low := math.Inf(1)
		for i := 1; i < len(v.Hours) && i <= 15; i++ {
			low = math.Min(low, v.Hours[i].Temp)
		}
		if low <= freezing {
			return in.T("Frost in den nächsten Stunden (bis %s)", temp(low, "°"))
		}
	}
	if v.Gusts >= 60 && v.Unit == "°C" || v.Gusts >= 37 && v.Unit == "°F" {
		return in.T("Sturmböen bis %.0f %s", v.Gusts, v.Speed)
	}
	if v.Chance != nil && *v.Chance < 20 && (v.Kind == "clear" || v.Kind == "partly") {
		return in.T("Bleibt trocken")
	}
	return ""
}
