package integrations

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/heimdeck/heimdeck/internal/sysinfo"
)

// system shows the load of the machine Heimdeck runs on. It needs no
// address; only admins can add it (it shows the whole host).

type system struct{}

func init() { register(system{}) }

func (system) Def() Definition {
	return Definition{
		ID: "system", Name: "System", Icon: "🖥️", Category: "System",
		Description: "Auslastung des Rechners, auf dem Heimdeck läuft: CPU, Arbeitsspeicher, Speicherplatz, Netzwerk, Lese- und Schreibgeschwindigkeit, Temperatur",
		NoURL:       true, HasWidget: true, Refresh: 5,
		Setup: []string{
			"Im Docker-Container sind CPU, Arbeitsspeicher, Datenträger-Geschwindigkeit und Temperatur die des Hosts. Das Netzwerk ist das des Containers – für das des Hosts den Container mit network_mode: host starten.",
			"Weitere Laufwerke (z. B. /mnt/media) erst in den Container einbinden, z. B. „- /mnt/media:/mnt/media:ro“ in der docker-compose.yml, und dann unten eintragen.",
			"In einem Proxmox-LXC-Container zeigt die Kachel die Werte des Containers, nicht des ganzen Servers – dafür die Proxmox-Kachel nehmen.",
		},
		Fields: []Field{
			{Key: "paths", Label: "Laufwerke", Type: "text",
				Help: "Weitere Einhängepunkte, mit Komma getrennt, z. B. /mnt/media, /srv/backup. Das System-Laufwerk und Heimdecks Datenordner sind immer dabei."},
		},
	}
}

func (system) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	paths := []string{"/"}
	if d := os.Getenv("HEIMDECK_DATA"); d != "" {
		paths = append(paths, d)
	}
	for _, p := range strings.Split(in.V("paths"), ",") {
		if p = strings.TrimSpace(p); strings.HasPrefix(p, "/") {
			paths = append(paths, p)
		}
	}
	s, err := sysinfo.Read(paths)
	if err != nil {
		return nil, in.Errorf("Systemwerte sind %s", in.T(err.Error()))
	}
	w := &Widget{}
	add := func(label, value, tone string) {
		w.Stats = append(w.Stats, Stat{Label: in.T(label), Value: value, Tone: tone})
	}
	level := func(pct, warn, bad float64) string {
		switch {
		case pct >= bad:
			return "bad"
		case pct >= warn:
			return "warn"
		}
		return ""
	}
	add("CPU", fmt.Sprintf("%.0f %%", s.CPU), level(s.CPU, 80, 95))
	if s.MemTotal > 0 {
		pct := 100 * float64(s.MemUsed) / float64(s.MemTotal)
		add("Arbeitsspeicher", fmt.Sprintf("%.0f %%", pct), level(pct, 85, 95))
		add("RAM belegt", in.Bytes(float64(s.MemUsed))+" / "+in.Bytes(float64(s.MemTotal)), "")
	}
	if s.SwapTotal > 0 && s.SwapUsed > 0 {
		pct := 100 * float64(s.SwapUsed) / float64(s.SwapTotal)
		add("Swap", fmt.Sprintf("%.0f %%", pct), level(pct, 50, 90))
	}
	if s.Load[0] > 0 || s.Load[1] > 0 {
		add("Last (1/5/15 Min.)", fmt.Sprintf("%.2f · %.2f · %.2f", s.Load[0], s.Load[1], s.Load[2]), toneIf(s.Cores > 0 && s.Load[1] > float64(s.Cores), "warn"))
	}
	if s.Temp > 0 {
		add("Temperatur", fmt.Sprintf("%.0f °C", s.Temp), level(s.Temp, 70, 85))
	}
	if s.Rates {
		add("Netz ↓", rate(in, s.NetRx), "")
		add("Netz ↑", rate(in, s.NetTx), "")
		add("Lesen", rate(in, s.DiskRead), "")
		add("Schreiben", rate(in, s.DiskWrite), "")
	}
	if s.Uptime > 0 {
		add("Laufzeit", uptimeText(in, s.Uptime.Seconds()), "")
	}
	for _, d := range s.Disks {
		pct := 100 * float64(d.Used) / float64(d.Total)
		w.Items = append(w.Items, Item{Title: d.Path, Subtitle: in.T("%s von %s belegt", in.Bytes(float64(d.Used)), in.Bytes(float64(d.Total))),
			Badge: fmt.Sprintf("%.0f %%", pct), Progress: ptr(pct)})
		if pct >= 90 {
			w.Alerts = append(w.Alerts, in.T("Laufwerk %s ist zu %.0f %% voll", d.Path, pct))
		}
	}
	if len(w.Items) > 0 {
		w.ItemsTitle = in.T("Speicherplatz")
	}
	return w, nil
}

func rate(in *Instance, bytesPerSec float64) string { return in.Bytes(bytesPerSec) + "/s" }

func ptr(v float64) *float64 { return &v }
