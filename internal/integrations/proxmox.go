package integrations

import (
	"context"
	"errors"
	"fmt"
	"net"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/heimdeck/heimdeck/internal/logging"
)

// Proxmox VE: REST API below /api2/json with an API token
// (pve.proxmox.com/wiki/Proxmox_VE_API). Besides the tile it lists the
// guests with their IP addresses for the automatic discovery.

type proxmox struct{}

func init() { register(proxmox{}) }

func (proxmox) Def() Definition {
	return Definition{
		ID: "proxmox", Name: "Proxmox VE", Icon: "proxmox", Category: "System",
		Description: "Nodes, CPU und RAM, laufende VMs und Container, Gäste starten, herunterfahren und neu starten, Dienste in VMs und LXCs automatisch finden",
		Fields: []Field{{Key: "token", Label: "API-Token", Type: "password", Required: true, Secret: true,
			Help: "Rechenzentrum → Berechtigungen → API-Token → Hinzufügen, eingeben als benutzer@pam!name=geheimnis. Rolle PVEAuditor genügt zum Anzeigen, für Start/Stopp zusätzlich PVEVMUser. Bei selbstsigniertem Zertifikat unter „Weitere Optionen“ „Zertifikat nicht prüfen“ aktivieren."}},
		HasWidget: true, HasSearch: true, Port: 8006,
	}
}

func (proxmox) call(ctx context.Context, in *Instance, method, path string, out any) error {
	tok := strings.TrimPrefix(in.V("token"), "PVEAPIToken=")
	if !strings.Contains(tok, "!") || !strings.Contains(tok, "=") {
		return in.Errorf("Der API-Token hat das Format benutzer@pam!name=geheimnis")
	}
	err := in.do(ctx, request{method: method, path: "/api2/json" + path,
		headers: map[string]string{"Authorization": "PVEAPIToken=" + tok}}, out)
	var he *HTTPError
	if errors.As(err, &he) && he.Status == 403 && method != "GET" {
		return in.Errorf("Dem API-Token fehlt das Recht dafür (Rolle PVEVMUser bzw. VM.PowerMgmt)")
	}
	return err
}

type pveResource struct {
	ID       string  `json:"id"`
	Type     string  `json:"type"` // node | qemu | lxc | storage | …
	Node     string  `json:"node"`
	Name     string  `json:"name"`
	VMID     int     `json:"vmid"`
	Status   string  `json:"status"`
	CPU      float64 `json:"cpu"`
	MaxCPU   float64 `json:"maxcpu"`
	Mem      float64 `json:"mem"`
	MaxMem   float64 `json:"maxmem"`
	Tags     string  `json:"tags"`
	Template int     `json:"template"`
}

func (p proxmox) resources(ctx context.Context, in *Instance) ([]pveResource, error) {
	var res struct {
		Data []pveResource `json:"data"`
	}
	err := p.call(ctx, in, "GET", "/cluster/resources", &res)
	return res.Data, err
}

func (p proxmox) Test(ctx context.Context, in *Instance) error {
	var v struct {
		Data struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	return p.call(ctx, in, "GET", "/version", &v)
}

func isGuest(r pveResource) bool { return (r.Type == "qemu" || r.Type == "lxc") && r.Template == 0 }

func (p proxmox) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	res, err := p.resources(ctx, in)
	if err != nil {
		return nil, err
	}
	var nodes []pveResource
	var stopped []pveResource
	nodesOnline, guests, running := 0, 0, 0
	var cpu, cpus, mem, maxMem float64
	for _, r := range res {
		switch {
		case r.Type == "node":
			nodes = append(nodes, r)
			if r.Status == "online" {
				nodesOnline++
				cpu += r.CPU * r.MaxCPU
				cpus += r.MaxCPU
				mem += r.Mem
				maxMem += r.MaxMem
			}
		case isGuest(r):
			guests++
			if r.Status == "running" {
				running++
			} else {
				stopped = append(stopped, r)
			}
		}
	}
	w := &Widget{}
	if len(nodes) > 1 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Nodes online"), Value: fmt.Sprintf("%d/%d", nodesOnline, len(nodes)), Tone: toneIf(nodesOnline < len(nodes), "bad")})
	}
	if cpus > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("CPU"), Value: fmt.Sprintf("%.0f %%", cpu/cpus*100), Tone: toneIf(cpu/cpus > 0.9, "warn")})
	}
	if maxMem > 0 {
		w.Stats = append(w.Stats, Stat{Label: in.T("RAM"), Value: fmt.Sprintf("%.0f %%", mem/maxMem*100), Tone: toneIf(mem/maxMem > 0.9, "warn")})
	}
	w.Stats = append(w.Stats, Stat{Label: in.T("Gäste aktiv"), Value: fmt.Sprintf("%d/%d", running, guests)})

	if len(nodes) > 1 {
		w.ItemsTitle = in.T("Nodes")
		sort.Slice(nodes, func(i, j int) bool { return nodes[i].Node < nodes[j].Node })
		for _, n := range nodes {
			it := Item{Title: n.Node, Badge: in.T(map[bool]string{true: "Online", false: "Offline"}[n.Status == "online"]), URL: in.Link("/")}
			if n.MaxMem > 0 && n.Status == "online" {
				it.Subtitle = fmt.Sprintf("CPU %.0f %% · RAM %s / %s", n.CPU*100, in.Bytes(n.Mem), in.Bytes(n.MaxMem))
				it.Progress = pct(n.Mem / n.MaxMem * 100)
			}
			w.Items = append(w.Items, it)
		}
	} else if len(stopped) > 0 {
		w.ItemsTitle = in.T("Gestoppt")
		sort.Slice(stopped, func(i, j int) bool { return stopped[i].VMID < stopped[j].VMID })
		for i, r := range stopped {
			if i == 5 {
				break
			}
			w.Items = append(w.Items, p.guestItem(in, r))
		}
	}
	return w, nil
}

func (p proxmox) guestItem(in *Instance, r pveResource) Item {
	kind := map[string]string{"qemu": "VM", "lxc": "LXC"}[r.Type]
	sub := []string{fmt.Sprintf("%s %d", kind, r.VMID), r.Node}
	if r.Status == "running" && r.MaxMem > 0 {
		sub = append(sub, fmt.Sprintf("CPU %.0f %%", r.CPU*100), "RAM "+in.Bytes(r.Mem))
	}
	it := Item{Title: r.Name, Subtitle: strings.Join(sub, " · "), URL: in.Link("/")}
	params := map[string]string{"node": r.Node, "type": r.Type, "vmid": strconv.Itoa(r.VMID)}
	if r.Status == "running" {
		it.Badge = in.T("Läuft")
		withAction := func(action string) map[string]string {
			m := map[string]string{"action": action}
			for k, v := range params {
				m[k] = v
			}
			return m
		}
		it.Actions = []Action{
			{ID: "power", Label: in.T("Neu starten"), Params: withAction("reboot"), Confirm: in.T("„%s“ neu starten?", r.Name)},
			{ID: "power", Label: in.T("Herunterfahren"), Tone: "danger", Params: withAction("shutdown"), Confirm: in.T("„%s“ herunterfahren?", r.Name)},
		}
	} else {
		it.Badge = in.T("Gestoppt")
		params["action"] = "start"
		it.Actions = []Action{{ID: "power", Label: in.T("Starten"), Tone: "primary", Params: params}}
	}
	return it
}

func (p proxmox) Search(ctx context.Context, in *Instance, query string) ([]Item, error) {
	res, err := p.resources(ctx, in)
	if err != nil {
		return nil, err
	}
	q := strings.ToLower(query)
	var hits []pveResource
	for _, r := range res {
		if isGuest(r) && (strings.Contains(strings.ToLower(r.Name), q) || strconv.Itoa(r.VMID) == q || strings.Contains(strings.ToLower(r.Tags), q)) {
			hits = append(hits, r)
		}
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].VMID < hits[j].VMID })
	out := []Item{}
	for _, r := range hits {
		out = append(out, p.guestItem(in, r))
		if len(out) == 8 {
			break
		}
	}
	return out, nil
}

var pveNodeRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,62}$`)

func (p proxmox) Action(ctx context.Context, in *Instance, action string, params map[string]string) error {
	vmid, err := strconv.Atoi(params["vmid"])
	kind, op := params["type"], params["action"]
	if action != "power" || err != nil || vmid < 100 || !pveNodeRe.MatchString(params["node"]) ||
		(kind != "qemu" && kind != "lxc") || (op != "start" && op != "shutdown" && op != "reboot") {
		return in.Errorf("unbekannte Aktion %q", action)
	}
	return p.call(ctx, in, "POST", fmt.Sprintf("/nodes/%s/%s/%d/status/%s", params["node"], kind, vmid, op), nil)
}

// Guest is a VM or container found on a Proxmox host, for the discovery.
type Guest struct {
	Name   string
	Type   string // qemu | lxc
	VMID   int
	Node   string
	Status string
	Tags   []string
	IP     string
}

// ProxmoxGuests lists all guests with their first IPv4 address. Containers
// report it directly; VMs only with the QEMU guest agent installed.
func ProxmoxGuests(ctx context.Context, in *Instance) ([]Guest, error) {
	p := proxmox{}
	res, err := p.resources(ctx, in)
	if err != nil {
		return nil, err
	}
	var guests []Guest
	for _, r := range res {
		if !isGuest(r) {
			continue
		}
		var tags []string
		for _, t := range strings.FieldsFunc(r.Tags, func(c rune) bool { return c == ';' || c == ',' || c == ' ' }) {
			tags = append(tags, t)
		}
		guests = append(guests, Guest{Name: r.Name, Type: r.Type, VMID: r.VMID, Node: r.Node, Status: r.Status, Tags: tags})
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for i := range guests {
		if guests[i].Status != "running" {
			continue
		}
		wg.Add(1)
		go func(g *Guest) {
			defer wg.Done()
			defer logging.Recover("Proxmox-Gast")
			sem <- struct{}{}
			defer func() { <-sem }()
			g.IP = p.guestIP(ctx, in, g)
		}(&guests[i])
	}
	wg.Wait()
	sort.Slice(guests, func(i, j int) bool { return guests[i].VMID < guests[j].VMID })
	return guests, nil
}

func (p proxmox) guestIP(ctx context.Context, in *Instance, g *Guest) string {
	base := fmt.Sprintf("/nodes/%s/%s/%d", g.Node, g.Type, g.VMID)
	if g.Type == "lxc" {
		var ifs struct {
			Data []struct {
				Name string `json:"name"`
				Inet string `json:"inet"`
			} `json:"data"`
		}
		if p.call(ctx, in, "GET", base+"/interfaces", &ifs) == nil {
			for _, i := range ifs.Data {
				if ip := usableIP(i.Inet); ip != "" && i.Name != "lo" {
					return ip
				}
			}
		}
		// Older versions: the static address from the configuration.
		var cfg struct {
			Data map[string]any `json:"data"`
		}
		if p.call(ctx, in, "GET", base+"/config", &cfg) == nil {
			for k, v := range cfg.Data {
				s, _ := v.(string)
				if !strings.HasPrefix(k, "net") {
					continue
				}
				for _, part := range strings.Split(s, ",") {
					if ip, ok := strings.CutPrefix(part, "ip="); ok {
						if ip := usableIP(ip); ip != "" {
							return ip
						}
					}
				}
			}
		}
		return ""
	}
	var agent struct {
		Data struct {
			Result []struct {
				Name string `json:"name"`
				IPs  []struct {
					Type string `json:"ip-address-type"`
					IP   string `json:"ip-address"`
				} `json:"ip-addresses"`
			} `json:"result"`
		} `json:"data"`
	}
	if p.call(ctx, in, "GET", base+"/agent/network-get-interfaces", &agent) == nil {
		for _, i := range agent.Data.Result {
			if i.Name == "lo" || strings.HasPrefix(i.Name, "docker") || strings.HasPrefix(i.Name, "br-") || strings.HasPrefix(i.Name, "veth") {
				continue
			}
			for _, a := range i.IPs {
				if a.Type == "ipv4" {
					if ip := usableIP(a.IP); ip != "" {
						return ip
					}
				}
			}
		}
	}
	return ""
}

// usableIP strips a /prefix and drops loopback, link-local and "dhcp".
func usableIP(s string) string {
	s, _, _ = strings.Cut(s, "/")
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return ""
	}
	return ip.String()
}
