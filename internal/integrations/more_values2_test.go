package integrations

import (
	"context"
	"strings"
	"testing"
	"time"
)

// More live data for the remaining integrations (1.4).

func TestPaperlessMoreValues(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /api/statistics/": obj{"documents_total": 812, "documents_inbox": 3, "inbox_tag": 4, "tag_count": 20, "correspondent_count": 15, "document_type_count": 6},
		"GET /api/tasks/":      []obj{{"status": "FAILURE", "task_file_name": "kaputt.pdf", "acknowledged": false}, {"status": "FAILURE", "task_file_name": "alt.pdf", "acknowledged": true}},
		"GET /api/documents/":  obj{"results": []obj{{"id": 9, "title": "Rechnung", "added": "2026-10-20T10:00:00Z"}}},
	}, nil)
	w, err := paperless{}.Fetch(context.Background(), f.instance(map[string]string{"apiKey": "k"}))
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	if s["Korrespondenten"] != "15" || s["Import-Fehler"] != "1" || len(w.Alerts) != 1 || w.ItemsTitle != "Posteingang" ||
		w.Items[0].Image != "/api/tiles/7/image?p=%2Fapi%2Fdocuments%2F9%2Fthumb%2F" {
		t.Fatalf("%v %v %+v", s, w.Alerts, w.Items)
	}
	if _, _, err := (paperless{}).ImageRequest(context.Background(), f.instance(nil), "/api/documents/9/download/"); err == nil {
		t.Fatal("only thumbnails may pass the image proxy")
	}
}

func TestPiholeMoreValues(t *testing.T) {
	f := newFake(t, map[string]any{
		"POST /api/auth":             obj{"session": obj{"valid": true, "sid": "s1"}},
		"GET /api/stats/summary":     obj{"queries": obj{"total": 1000, "blocked": 100, "percent_blocked": 10.0, "forwarded": 600, "cached": 300, "unique_domains": 250}, "clients": obj{"active": 5}, "gravity": obj{"domains_being_blocked": 150000, "last_update": time.Now().Add(-20 * 24 * time.Hour).Unix()}},
		"GET /api/dns/blocking":      obj{"blocking": "enabled"},
		"GET /api/info/version":      obj{"version": obj{"core": obj{"local": obj{"version": "v6.0"}, "remote": obj{"version": "v6.1"}}, "web": obj{"local": obj{"version": "v6.0"}, "remote": obj{"version": "v6.0"}}}},
		"GET /api/stats/top_domains": obj{"domains": []obj{{"domain": "ads.example.com", "count": 42}}},
	}, nil)
	w, err := pihole{}.Fetch(context.Background(), f.instance(map[string]string{"password": "pw"}))
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	if s["Weitergeleitet"] != "600" || s["Domains auf der Sperrliste"] != "150.000" || len(w.Alerts) != 1 || !strings.Contains(w.Alerts[0], "Core v6.1") ||
		w.Items[0].Badge != "42" || w.Stats[len(w.Stats)-1].Tone != "warn" {
		t.Fatalf("%v %v %+v", s, w.Alerts, w.Items)
	}
}

func TestQbittorrentMoreValues(t *testing.T) {
	done := time.Now().Add(-time.Hour).Unix()
	f := newFake(t, map[string]any{
		"GET /api/v2/transfer/info": obj{"dl_info_speed": 0, "up_info_speed": 0, "connection_status": "firewalled"},
		"GET /api/v2/torrents/info": []obj{{"hash": "a", "name": "Fertig.iso", "progress": 1.0, "state": "stalledUP", "size": 1e9, "completion_on": done}},
		"GET /api/v2/sync/maindata": obj{"server_state": obj{"alltime_dl": 5e12, "alltime_ul": 2e12, "global_ratio": "0.40", "free_space_on_disk": 1e10, "use_alt_speed_limits": true}},
	}, nil)
	w, err := qbittorrent{}.Fetch(context.Background(), f.instance(nil))
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	if s["Verhältnis"] != "0.40" || s["Tempolimit"] != "aktiv" || s["Torrents"] != "1" || w.ItemsTitle != "Zuletzt fertig" || len(w.Alerts) != 1 {
		t.Fatalf("%v %v %+v", s, w.Alerts, w.Items)
	}
}

func TestProxmoxTasksAndUpdates(t *testing.T) {
	recent := float64(time.Now().Add(-time.Hour).Unix())
	f := newFake(t, map[string]any{
		"GET /api2/json/cluster/resources": obj{"data": []obj{
			{"type": "node", "node": "pve", "status": "online", "cpu": 0.1, "maxcpu": 4, "mem": 4e9, "maxmem": 16e9, "uptime": 90000},
			{"type": "qemu", "node": "pve", "vmid": 100, "name": "vm", "status": "running", "maxmem": 2e9, "mem": 1e9},
			{"type": "lxc", "node": "pve", "vmid": 101, "name": "ct", "status": "stopped"},
		}},
		"GET /api2/json/version":              obj{"data": obj{"version": "9.0.3"}},
		"GET /api2/json/nodes/pve/apt/update": obj{"data": []obj{{"Package": "a"}, {"Package": "b"}}},
		"GET /api2/json/cluster/tasks":        obj{"data": []obj{{"type": "vzdump", "id": "100", "node": "pve", "status": "job errors", "endtime": recent}, {"type": "vzdump", "node": "pve", "status": "OK", "endtime": recent}}},
	}, nil)
	w, err := proxmox{}.Fetch(context.Background(), f.instance(map[string]string{"token": "root@pam!h=x"}))
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	if s["VMs"] != "1" || s["Container"] != "1" || s["Updates"] != "2" || s["Version"] != "9.0.3" || s["Fehlgeschlagene Aufgaben (24 h)"] != "1" || len(w.Alerts) != 1 {
		t.Fatalf("%v %v", s, w.Alerts)
	}
}

func TestHomeAssistantOverview(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /api/states": []obj{
			{"entity_id": "person.luis", "state": "home"},
			{"entity_id": "person.anna", "state": "not_home"},
			{"entity_id": "binary_sensor.tuer", "state": "on", "attributes": obj{"device_class": "door"}},
			{"entity_id": "sensor.fenster_batterie", "state": "12", "attributes": obj{"device_class": "battery", "unit_of_measurement": "%", "friendly_name": "Fenster"}},
			{"entity_id": "automation.licht", "state": "on"},
		},
		"GET /api/config": obj{"version": "2026.10.1"},
	}, nil)
	w, err := homeAssistant{}.Fetch(context.Background(), f.instance(map[string]string{"token": "t"}))
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	if s["Zuhause"] != "1/2" || s["Türen und Fenster offen"] != "1" || s["Batterie schwach"] != "1" || s["Automationen an"] != "1" || s["Version"] != "2026.10.1" {
		t.Fatalf("%v %v", s, w.Alerts)
	}
}
