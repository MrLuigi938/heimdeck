package integrations

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

func TestProxmox(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /api2/json/version": obj{"data": obj{"version": "9.0.3"}},
		"GET /api2/json/cluster/resources": obj{"data": []obj{
			{"type": "node", "node": "pve", "status": "online", "cpu": 0.25, "maxcpu": 8, "mem": 8e9, "maxmem": 32e9, "uptime": 400000},
			{"type": "storage", "node": "pve", "id": "storage/pve/local-lvm", "storage": "local-lvm", "status": "available", "disk": 95e9, "maxdisk": 100e9},
			{"type": "storage", "node": "pve", "id": "storage/pve/nas", "storage": "nas", "status": "available", "shared": 1, "disk": 5e9, "maxdisk": 100e9},
			{"type": "storage", "node": "pve2", "id": "storage/pve2/nas", "storage": "nas", "status": "available", "shared": 1, "disk": 5e9, "maxdisk": 100e9},
			{"type": "lxc", "node": "pve", "vmid": 101, "name": "jellyfin", "status": "running", "cpu": 0.1, "maxmem": 2e9, "mem": 1e9},
			{"type": "lxc", "node": "pve", "vmid": 102, "name": "adguard-home", "status": "stopped"},
			{"type": "qemu", "node": "pve", "vmid": 200, "name": "haos16.0", "status": "running", "tags": "smarthome"},
			{"type": "qemu", "node": "pve", "vmid": 9000, "name": "debian-template", "status": "stopped", "template": 1},
			{"type": "storage", "node": "pve", "id": "storage/pve/local"},
		}},
		"GET /api2/json/nodes/pve/lxc/101/interfaces":                    obj{"data": []obj{{"name": "lo", "inet": "127.0.0.1/8"}, {"name": "eth0", "inet": "192.168.1.51/24"}}},
		"GET /api2/json/nodes/pve/qemu/200/agent/network-get-interfaces": obj{"data": obj{"result": []obj{{"name": "lo", "ip-addresses": []obj{{"ip-address-type": "ipv4", "ip-address": "127.0.0.1"}}}, {"name": "enp0s18", "ip-addresses": []obj{{"ip-address-type": "ipv6", "ip-address": "fe80::1"}, {"ip-address-type": "ipv4", "ip-address": "192.168.1.60"}}}}}},
		"POST /api2/json/nodes/pve/lxc/102/status/start":                 obj{"data": "UPID:pve:..."},
	}, func(r *http.Request) bool {
		return r.Header.Get("Authorization") == "PVEAPIToken=root@pam!heimdeck=abc-123"
	})
	in := f.instance(map[string]string{"token": "root@pam!heimdeck=abc-123"})
	if err := (proxmox{}).Test(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	w, err := proxmox{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	if s["CPU"] != "25 %" || s["RAM"] != "25 %" || s["VMs und Container an"] != "2/3" || s["Speicher"] != "50 %" || s["Laufzeit"] != "4 Tage" {
		t.Fatalf("stats %v", s)
	}
	// running guests first (busiest on top), then the stopped one; local-lvm is nearly full
	if len(w.Items) != 3 || w.Items[0].Title != "jellyfin" || w.Items[2].Title != "adguard-home" || w.Items[2].Actions[0].Label != "Starten" || len(w.Alerts) != 1 {
		t.Fatalf("items %+v alerts %v", w.Items, w.Alerts)
	}
	if err := (proxmox{}).Action(context.Background(), in, "power", w.Items[2].Actions[0].Params); err != nil {
		t.Fatal(err)
	}
	if err := (proxmox{}).Action(context.Background(), in, "power", map[string]string{"node": "pve/../x", "type": "lxc", "vmid": "102", "action": "start"}); err == nil {
		t.Fatal("bad node accepted")
	}
	guests, err := ProxmoxGuests(context.Background(), in)
	if err != nil || len(guests) != 3 || guests[0].IP != "192.168.1.51" || guests[2].IP != "192.168.1.60" || guests[1].IP != "" {
		t.Fatalf("%v %+v", err, guests)
	}
	in.Values["token"] = "nur-ein-geheimnis"
	if err := (proxmox{}).Test(context.Background(), in); err == nil {
		t.Fatal("token without user accepted")
	}
}

// A token created with privilege separation and no rights of its own sees nothing.
func TestProxmoxTokenWithoutRights(t *testing.T) {
	f := newFake(t, map[string]any{"GET /api2/json/cluster/resources": obj{"data": []obj{}}}, nil)
	_, err := proxmox{}.Fetch(context.Background(), f.instance(map[string]string{"token": "root@pam!heimdeck=abc"}))
	if err == nil || !strings.Contains(err.Error(), "Privilegtrennung") {
		t.Fatalf("got %v", err)
	}
}

func TestMatchName(t *testing.T) {
	for name, want := range map[string]string{
		"jellyfin": "jellyfin", "adguard": "adguard", "adguard-home": "adguard", "paperless-ngx": "paperless",
		"haos16.0": "home-assistant", "homeassistant": "home-assistant", "speedtest-tracker": "speedtest-tracker",
		"overseerr": "jellyseerr", "nextcloudpi": "nextcloud", "pve-backup": "", "docker01": "",
	} {
		in, ok := MatchName(name)
		got := ""
		if ok {
			got = in.Def().ID
		}
		if got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}
