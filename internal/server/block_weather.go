package server

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/heimdeck/heimdeck/internal/logging"
	"github.com/heimdeck/heimdeck/internal/store"
)

// handleBlockWeather gives a weather block on the free page its data for a
// place of its own – no weather tile needed. It asks Open-Meteo like the
// weather tile does (same code, same cache: the place is looked up once a day,
// the weather at most every ten minutes, shared with a tile for the same place).
func (s *Server) handleBlockWeather(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	place := strings.TrimSpace(q.Get("place"))
	if place == "" || utf8.RuneCountInString(place) > 120 {
		writeErr(w, 400, errors.New("Bitte einen Ort angeben"))
		return
	}
	units := "°C"
	if q.Get("units") == "°F" {
		units = "°F"
	}
	// The whole week: the block shows as many days as it has room for.
	t := store.Tile{Name: place, Integration: "weather", Config: map[string]string{"place": place, "days": "7", "units": units}}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	data, err := s.safeFetch(ctx, t, langOf(r))
	if err != nil {
		writeJSON(w, map[string]any{"error": tr(r, logging.Public(err.Error()))})
		return
	}
	writeJSON(w, map[string]any{"widget": data})
}
