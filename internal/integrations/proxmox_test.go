package integrations

import (
	"context"
	"net/http"
	"testing"
)

func TestProxmox(t *testing.T) {
	f := newFake(t, map[string]any{
		"GET /api2/json/version": obj{"data": obj{"version": "9.0.3"}},
		"GET /api2/json/cluster/resources": obj{"data": []obj{
			{"type": "node", "node": "pve", "status": "online", "cpu": 0.25, "maxcpu": 8, "mem": 8e9, "maxmem": 32e9},
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
	if s := stats(w); s["CPU"] != "25 %" || s["RAM"] != "25 %" || s["Gäste aktiv"] != "2/3" || len(w.Items) != 1 || w.Items[0].Actions[0].Label != "Starten" {
		t.Fatalf("%v %+v", s, w.Items)
	}
	if err := (proxmox{}).Action(context.Background(), in, "power", w.Items[0].Actions[0].Params); err != nil {
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
