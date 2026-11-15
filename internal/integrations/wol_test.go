package integrations

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWakeOnLAN(t *testing.T) {
	// MAC addresses in the usual spellings.
	for _, s := range []string{"3C:7C:3F:12:34:56", "3c-7c-3f-12-34-56", "3c7c.3f12.3456", "3c7c3f123456"} {
		if hw, err := parseMAC(s); err != nil || hw.String() != "3c:7c:3f:12:34:56" {
			t.Fatalf("%s: %v %v", s, hw, err)
		}
	}
	if _, err := parseMAC("3c:7c:3f"); err == nil {
		t.Fatal("a short MAC was accepted")
	}
	hw, _ := parseMAC("3c:7c:3f:12:34:56")
	p := magicPacket(hw)
	if len(p) != 102 || !bytes.Equal(p[:6], bytes.Repeat([]byte{0xff}, 6)) || !bytes.Equal(p[96:], hw) {
		t.Fatalf("magic packet: %x", p)
	}

	// The packet itself, to a local address standing in for the broadcast.
	l, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Skip("no UDP here")
	}
	defer l.Close()
	port := l.LocalAddr().(*net.UDPAddr).Port
	conn, _ := net.ListenPacket("udp4", ":0")
	addr := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: port}
	conn.WriteTo(magicPacket(hw), addr)
	conn.Close()
	buf := make([]byte, 200)
	l.SetReadDeadline(time.Now().Add(2 * time.Second))
	if n, _, err := l.ReadFrom(buf); err != nil || !bytes.Equal(buf[:n], p) {
		t.Fatalf("received: %v %x", err, buf[:n])
	}
	if err := sendMagicPacket("not-an-ip", hw); err == nil {
		t.Fatal("a bad broadcast address was accepted")
	}

	// Through the FRITZ!Box: status from its host list, waking by its TR-064 call.
	var woke string
	box := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		action := r.Header.Get("SoapAction")
		switch {
		case strings.HasSuffix(action, "#GetSpecificHostEntry"):
			if !strings.Contains(string(body), "3C:7C:3F:12:34:56") {
				w.WriteHeader(500)
				io.WriteString(w, `<s:Envelope><s:Body><s:Fault><detail><UPnPError><errorCode>714</errorCode></UPnPError></detail></s:Fault></s:Body></s:Envelope>`)
				return
			}
			io.WriteString(w, `<s:Envelope><s:Body><u:R><NewActive>0</NewActive><NewHostName>gaming-pc</NewHostName><NewIPAddress>192.168.178.40</NewIPAddress></u:R></s:Body></s:Envelope>`)
		case strings.HasSuffix(action, "#X_AVM-DE_WakeOnLANByMACAddress"):
			woke = string(body)
			io.WriteString(w, `<s:Envelope><s:Body><u:R></u:R></s:Body></s:Envelope>`)
		default:
			w.WriteHeader(500)
		}
	}))
	defer box.Close()
	client := box.Client()
	client.Transport = rewritePort{box.Listener.Addr().String(), http.DefaultTransport}
	fb := &Instance{BaseURL: strings.Replace(box.URL, "127.0.0.1", "localhost", 1), Values: map[string]string{"username": "u", "password": "p"}, Client: client, Lang: "de"}
	in := &Instance{Values: map[string]string{"mac": "3c-7c-3f-12-34-56", "via": "auto"}, Lang: "de",
		Others: func(kind string) []*Instance {
			if kind == "fritzbox" {
				return []*Instance{fb}
			}
			return nil
		}}
	wd, err := wol{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	s := stats(wd)
	if s["Status"] != "aus" || s["Name"] != "gaming-pc" || s["IP"] != "192.168.178.40" || s["Über"] != "FRITZ!Box" || len(wd.Actions) != 1 {
		t.Fatalf("widget: %v %+v", s, wd)
	}
	if err := (wol{}).Action(context.Background(), in, "wake", nil); err != nil || !strings.Contains(woke, "<NewMACAddress>3C:7C:3F:12:34:56</NewMACAddress>") {
		t.Fatalf("wake through the box: %v %q", err, woke)
	}
	// An unknown device: a hint, no error for the whole tile.
	in.Values["mac"] = "aa:bb:cc:dd:ee:ff"
	if wd, err := (wol{}).Fetch(context.Background(), in); err != nil || len(wd.Alerts) == 0 {
		t.Fatalf("unknown device: %v %+v", err, wd)
	}
	// An external account may not send packets into the home network.
	ext := &Instance{Values: map[string]string{"mac": "3c:7c:3f:12:34:56", "via": "packet"}, Lang: "de", PublicOnly: true}
	if err := (wol{}).Action(context.Background(), ext, "wake", nil); err == nil {
		t.Fatal("an external account woke a computer")
	}
}
