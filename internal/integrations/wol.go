package integrations

import (
	"context"
	"encoding/hex"
	"errors"
	"net"
	"strings"
)

// wol switches a computer on with Wake-on-LAN. The FRITZ!Box can do it for
// Heimdeck (TR-064, with the login of a FRITZ!Box tile) – that also works
// when Heimdeck runs in Docker, whose network usually does not let a
// broadcast out. Without a FRITZ!Box tile Heimdeck sends the magic packet
// itself. Through the FRITZ!Box the tile also shows whether the computer is on.
type wol struct{}

func init() { register(wol{}) }

func (wol) Def() Definition {
	return Definition{
		ID: "wol", Name: "PC einschalten (Wake-on-LAN)", Icon: "⏻", Category: "Netzwerk",
		Description: "Ein Knopf, der einen Rechner im Heimnetz einschaltet – über die FRITZ!Box oder ein Magic Packet; mit FRITZ!Box auch, ob er an ist",
		Setup: []string{
			"Am Rechner Wake-on-LAN einschalten: im BIOS/UEFI und bei der Netzwerkkarte (Windows: Geräte-Manager → Netzwerkadapter → Eigenschaften → Energieverwaltung und Erweitert → „Wake on Magic Packet“).",
			"Die MAC-Adresse steht z. B. in der FRITZ!Box unter Heimnetz → Netzwerk beim Gerät, oder am Rechner mit „ipconfig /all“ (Windows) bzw. „ip link“ (Linux).",
			"Mit einer FRITZ!Box-Kachel auf deinem Dashboard weckt Heimdeck den Rechner über die FRITZ!Box – das klappt auch, wenn Heimdeck in Docker läuft. Sonst schickt Heimdeck selbst ein Magic Packet; in Docker braucht das „network_mode: host“.",
		},
		Fields: []Field{
			{Key: "mac", Label: "MAC-Adresse", Type: "text", Required: true, Help: "z. B. 3C:7C:3F:12:34:56"},
			{Key: "via", Label: "Wecken über", Type: "select", Options: []string{"auto", "fritzbox", "packet"},
				Help: "auto: über die FRITZ!Box, wenn es eine FRITZ!Box-Kachel gibt, sonst Magic Packet · fritzbox · packet: Magic Packet von Heimdeck"},
			{Key: "broadcast", Label: "Broadcast-Adresse (Magic Packet)", Type: "text", Help: "Leer: 255.255.255.255. Sonst die des Netzes, z. B. 192.168.178.255"},
		},
		HasWidget: true, NoURL: true, Refresh: 30,
	}
}

// parseMAC accepts 3C:7C:3F:12:34:56, 3c-7c-…, 3c7c.3f12.3456 and 3c7c3f123456.
func parseMAC(s string) (net.HardwareAddr, error) {
	s = strings.TrimSpace(s)
	if hw, err := net.ParseMAC(s); err == nil && len(hw) == 6 {
		return hw, nil
	}
	if b, err := hex.DecodeString(strings.NewReplacer(":", "", "-", "", ".", "", " ", "").Replace(s)); err == nil && len(b) == 6 {
		return net.HardwareAddr(b), nil
	}
	return nil, errors.New("bad MAC")
}

// magicPacket is 6 × 0xFF and 16 × the MAC address.
func magicPacket(hw net.HardwareAddr) []byte {
	p := make([]byte, 0, 102)
	for i := 0; i < 6; i++ {
		p = append(p, 0xff)
	}
	for i := 0; i < 16; i++ {
		p = append(p, hw...)
	}
	return p
}

// sendMagicPacket sends the packet to the broadcast address on ports 9 and 7.
func sendMagicPacket(broadcast string, hw net.HardwareAddr) error {
	host := strings.TrimSpace(broadcast)
	if host == "" {
		host = "255.255.255.255"
	}
	if ip := net.ParseIP(host); ip == nil || ip.To4() == nil {
		return errors.New("bad broadcast address")
	}
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return err
	}
	defer conn.Close()
	pkt := magicPacket(hw)
	var last error
	sent := false
	for _, port := range []string{"9", "7"} {
		addr, err := net.ResolveUDPAddr("udp4", net.JoinHostPort(host, port))
		if err != nil {
			return err
		}
		if _, err := conn.WriteTo(pkt, addr); err != nil {
			last = err
		} else {
			sent = true
		}
	}
	if !sent {
		return last
	}
	return nil
}

// fritz is the owner's first FRITZ!Box tile, if any.
func (w wol) fritz(in *Instance) *Instance {
	if in.Others == nil || in.V("via") == "packet" {
		return nil
	}
	if list := in.Others("fritzbox"); len(list) > 0 {
		return list[0]
	}
	return nil
}

func (w wol) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	hw, err := parseMAC(in.V("mac"))
	if err != nil {
		return nil, in.Errorf("Die MAC-Adresse ist ungültig – z. B. 3C:7C:3F:12:34:56")
	}
	wd := &Widget{Actions: []Action{{ID: "wake", Label: "⏻ " + in.T("Einschalten"), Tone: "primary"}}}
	fb := w.fritz(in)
	switch {
	case fb != nil:
		res, err := fritzbox{}.call(ctx, fb, fbHosts, "GetSpecificHostEntry", map[string]string{"NewMACAddress": strings.ToUpper(hw.String())})
		var fault fritzFault
		switch {
		case errors.As(err, &fault) && fault.code == 714:
			wd.Alerts = append(wd.Alerts, in.T("Die FRITZ!Box kennt dieses Gerät nicht – stimmt die MAC-Adresse?"))
		case err != nil:
			wd.Alerts = append(wd.Alerts, fbHint(in, err, "Ob der Rechner an ist, verrät die FRITZ!Box nicht").Error())
		default:
			on := res["NewActive"] == "1"
			state, tone := in.T("aus"), ""
			if on {
				state, tone = in.T("an"), "ok"
			}
			wd.Stats = append(wd.Stats, Stat{Label: in.T("Status"), Value: state, Tone: tone})
			if name := res["NewHostName"]; name != "" {
				wd.Stats = append(wd.Stats, Stat{Label: in.T("Name"), Value: name})
			}
			if ip := res["NewIPAddress"]; ip != "" {
				wd.Stats = append(wd.Stats, Stat{Label: "IP", Value: ip})
			}
			if on {
				wd.Actions[0].Label = "⏻ " + in.T("Nochmal wecken")
				wd.Actions[0].Tone = ""
			}
		}
		wd.Stats = append(wd.Stats, Stat{Label: in.T("Über"), Value: "FRITZ!Box"})
	default:
		if in.V("via") == "fritzbox" {
			wd.Alerts = append(wd.Alerts, in.T("Keine FRITZ!Box-Kachel gefunden – bitte eine anlegen oder „Wecken über“ auf auto stellen"))
		}
		wd.Stats = append(wd.Stats, Stat{Label: "MAC", Value: strings.ToUpper(hw.String())}, Stat{Label: in.T("Über"), Value: "Magic Packet"})
	}
	return wd, nil
}

func (w wol) Test(ctx context.Context, in *Instance) error {
	if _, err := parseMAC(in.V("mac")); err != nil {
		return in.Errorf("Die MAC-Adresse ist ungültig – z. B. 3C:7C:3F:12:34:56")
	}
	return nil
}

func (w wol) Action(ctx context.Context, in *Instance, action string, _ map[string]string) error {
	if action != "wake" {
		return in.Errorf("unbekannte Aktion")
	}
	hw, err := parseMAC(in.V("mac"))
	if err != nil {
		return in.Errorf("Die MAC-Adresse ist ungültig – z. B. 3C:7C:3F:12:34:56")
	}
	if fb := w.fritz(in); fb != nil {
		if _, err := (fritzbox{}).call(ctx, fb, fbHosts, "X_AVM-DE_WakeOnLANByMACAddress", map[string]string{"NewMACAddress": strings.ToUpper(hw.String())}); err != nil {
			return fbHint(in, err, "Die FRITZ!Box konnte den Rechner nicht wecken")
		}
		return nil
	}
	if in.V("via") == "fritzbox" {
		return in.Errorf("Keine FRITZ!Box-Kachel gefunden – bitte eine anlegen oder „Wecken über“ auf auto stellen")
	}
	if in.PublicOnly {
		return in.Errorf("Dieses Konto darf nichts im Heimnetz einschalten")
	}
	if err := sendMagicPacket(in.V("broadcast"), hw); err != nil {
		return in.Errorf("Das Magic Packet ließ sich nicht senden: %v", err)
	}
	return nil
}

// fbHint turns a TR-064 error into a sentence: a missing right of the
// FRITZ!Box user, or what did not work with the error number.
func fbHint(in *Instance, err error, what string) error {
	var fault fritzFault
	if errors.As(err, &fault) {
		if fault.code == 606 || fault.code == 401 {
			return in.Errorf("Der FRITZ!Box-Benutzer darf das nicht – er braucht das Recht „FRITZ!Box Einstellungen“")
		}
		return in.Errorf("%s (Fehler %d)", in.T(what), fault.code)
	}
	return err
}
