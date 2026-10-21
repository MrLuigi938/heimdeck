package integrations

import "testing"

func TestParseEvccState(t *testing.T) {
	// older layout: gridPower, battery as a list of devices
	st, err := parseEvccState([]byte(`{"result":{"pvPower":3000,"homePower":800,"gridPower":-500,
		"battery":[{"power":-1000,"soc":40,"capacity":10},{"power":200,"soc":80,"capacity":5}],
		"loadpoints":[{"title":"Carport","mode":"pv","charging":false,"connected":true,"chargePower":0}]}}`))
	if err != nil {
		t.Fatal(err)
	}
	if *st.Grid != -500 || *st.BatPower != -800 || int(*st.BatSoc) != 53 || st.Smart {
		t.Fatalf("old layout: grid %v, battery %v / %v, smart %v", *st.Grid, *st.BatPower, *st.BatSoc, st.Smart)
	}

	// current layout: grid and battery objects, pv list, smart mode
	st, err = parseEvccState([]byte(`{"pv":[{"power":1200},{"power":800}],"homePower":500,"grid":{"power":300},
		"battery":{"power":400,"soc":71},"loadpoints":[{"title":"Heat pump","mode":"smart","charging":true,"chargePower":2100}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if *st.PV != 2000 || *st.Grid != 300 || *st.BatSoc != 71 || !st.Smart {
		t.Fatalf("new layout: pv %v, grid %v, soc %v, smart %v", *st.PV, *st.Grid, *st.BatSoc, st.Smart)
	}

	if _, err := parseEvccState([]byte(`{"version":"1.0"}`)); err == nil {
		t.Fatal("other service accepted as evcc")
	}
}

func TestEnergy(t *testing.T) {
	de, en := &Instance{Lang: "de"}, &Instance{Lang: "en"}
	for _, c := range []struct {
		in   *Instance
		wh   float64
		want string
	}{{de, 140, "140 Wh"}, {de, 8700, "8,7 kWh"}, {en, 8700, "8.7 kWh"}} {
		if got := energy(c.in, c.wh); got != c.want {
			t.Errorf("energy(%v) = %q, want %q", c.wh, got, c.want)
		}
	}
}
