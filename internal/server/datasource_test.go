package server

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/heimdeck/heimdeck/internal/store"
)

// Values sent to a data source tile are kept and shown as numbers and a chart.
func TestDataSource(t *testing.T) {
	s := testServer(t)
	g, _ := s.store.CreateGroup("A", 0)
	const key = "Dat123_-Dat123_-Dat123_-"
	tile := store.Tile{GroupID: g.ID, Name: "Garten", Integration: "datasource", Config: map[string]string{"hookKey": key, "unit": "temperatur=°C, luftfeuchte=%, boden=°C", "range": "1 Stunde"}}
	if err := s.store.SaveTile(&tile); err != nil {
		t.Fatal(err)
	}
	h := s.Handler()
	send := func(method, path, body string) (int, string) {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}
	// Older values straight into the store, then three ways of sending.
	now := time.Now()
	s.store.AddPoints(tile.ID, []store.Point{{Name: "temperatur", T: now.Add(-40 * time.Minute).UnixMilli(), Value: 18}, {Name: "temperatur", T: now.Add(-20 * time.Minute).UnixMilli(), Value: 19}})
	if c, b := send("POST", "/hook/"+key, `{"temperatur": 21.4, "luftfeuchte": 58, "text": "nein"}`); c != 200 || !strings.Contains(b, `"values":2`) {
		t.Fatalf("json: %d %s", c, b)
	}
	if c, b := send("POST", "/hook/"+key, "boden,beet=1 feuchte=31i\nboden value=12.5"); c != 200 || !strings.Contains(b, `"values":2`) {
		t.Fatalf("influx: %d %s", c, b)
	}
	if c, _ := send("GET", "/hook/"+key+"?wind=3,5", ""); c != 200 {
		t.Fatalf("get: %d", c)
	}
	if c, _ := send("POST", "/hook/"+key, `{"nur":"text"}`); c != 400 {
		t.Fatalf("no numbers: %d", c)
	}
	if c, _ := send("POST", "/hook/"+key, `{"<script>": 1}`); c != 400 {
		t.Fatalf("bad name: %d", c)
	}

	tile, _ = s.store.Tile(tile.ID)
	w, err := s.safeFetch(t.Context(), tile, "de")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, st := range w.Stats {
		got[st.Label] = st.Value
	}
	for k, v := range map[string]string{"temperatur": "21,4 °C", "luftfeuchte": "58 %", "boden.feuchte": "31", "boden": "12,5 °C", "wind": "3,5"} {
		if got[k] != v {
			t.Errorf("%s = %q, want %q (all %v)", k, got[k], v, got)
		}
	}
	if len(w.Charts) != 1 || w.Charts[0].Kind != "line" || len(w.Charts[0].Series) != 5 {
		t.Fatalf("chart %+v", w.Charts)
	}
	// The temperature: from 40 minutes ago on, one point a minute, the newest 21.4.
	var temp []float64
	for _, sr := range w.Charts[0].Series {
		if sr.Name == "temperatur" {
			temp = sr.Values
		}
	}
	if len(temp) < 40 || temp[0] != 18 || temp[len(temp)-1] != 21.4 {
		t.Fatalf("temperature %v", temp)
	}
	// Only chosen values, and the oldest go when a value comes again within ten seconds.
	tile.Config["names"] = "temperatur"
	if w, _ := s.safeFetch(t.Context(), tile, "de"); len(w.Stats) != 2 || len(w.Charts[0].Series) != 1 || w.Charts[0].Unit != "°C" {
		t.Fatalf("names: %+v", w)
	}
	send("POST", "/hook/"+key, `{"temperatur": 22}`)
	if pts, _ := s.store.Points(tile.ID, now.Add(-time.Minute)); len(pts["temperatur"]) != 1 || pts["temperatur"][0].Value != 22 {
		t.Fatalf("within ten seconds: %+v", pts["temperatur"])
	}
}
