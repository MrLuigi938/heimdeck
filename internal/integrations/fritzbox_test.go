package integrations

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestFritzBox(t *testing.T) {
	const user, pass, realm, nonce = "heimdeck", "pw", "F!Box SOAP-Auth", "ABCDEF0123"
	h := func(s string) string { sum := md5.Sum([]byte(s)); return hex.EncodeToString(sum[:]) }
	param := regexp.MustCompile(`(\w+)="?([^",]*)"?`)
	recent := time.Now().Add(-time.Hour).Format("02.01.06 15:04")
	var switched string
	answers := map[string]string{
		"DeviceInfo:1#GetInfo":                               "<NewModelName>FRITZ!Box 7590</NewModelName><NewSoftwareVersion>154.08.01</NewSoftwareVersion><NewUpTime>200000</NewUpTime>",
		"WANCommonInterfaceConfig:1#GetCommonLinkProperties": "<NewPhysicalLinkStatus>Up</NewPhysicalLinkStatus><NewLayer1DownstreamMaxBitRate>250000000</NewLayer1DownstreamMaxBitRate><NewLayer1UpstreamMaxBitRate>40000000</NewLayer1UpstreamMaxBitRate>",
		"WANCommonInterfaceConfig:1#GetAddonInfos":           "<NewByteSendRate>125000</NewByteSendRate><NewByteReceiveRate>2500000</NewByteReceiveRate>",
		"WANPPPConnection:1#GetExternalIPAddress":            "<NewExternalIPAddress>203.0.113.7</NewExternalIPAddress>",
		"Hosts:1#X_AVM-DE_GetHostListPath":                   "<NewX_AVM-DE_HostListPath>/devicehostlist.lua?sid=1</NewX_AVM-DE_HostListPath>",
		"WLANConfiguration:1#GetInfo":                        "<NewEnable>1</NewEnable><NewSSID>Zuhause</NewSSID><NewStandard>ax</NewStandard>",
		"WLANConfiguration:1#GetTotalAssociations":           "<NewTotalAssociations>7</NewTotalAssociations>",
		"X_AVM-DE_Homeauto:1#GetGenericDeviceInfos/0":        "<NewAIN>11657 0240192</NewAIN><NewDeviceName>Kaffeemaschine</NewDeviceName><NewPresent>CONNECTED</NewPresent><NewSwitchIsEnabled>ENABLED</NewSwitchIsEnabled><NewSwitchState>ON</NewSwitchState><NewMultimeterIsEnabled>ENABLED</NewMultimeterIsEnabled><NewMultimeterPower>123450</NewMultimeterPower><NewTemperatureIsEnabled>ENABLED</NewTemperatureIsEnabled><NewTemperatureCelsius>215</NewTemperatureCelsius>",
		"X_AVM-DE_OnTel:1#GetCallList":                       "<NewCallListURL>http://192.168.178.1:49000/calllist.lua?sid=1</NewCallListURL>",
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/devicehostlist.lua":
			io.WriteString(w, `<List><Item><Active>1</Active></Item><Item><Active>0</Active></Item><Item><Active>1</Active></Item></List>`)
			return
		case "/calllist.lua":
			if r.URL.Query().Get("max") != "20" {
				t.Errorf("call list without max: %s", r.URL.RawQuery)
			}
			fmt.Fprintf(w, `<root><Call><Type>2</Type><Caller>0301234</Caller><Name>Mama</Name><Date>%s</Date><Duration>0:00</Duration></Call>`+
				`<Call><Type>3</Type><Called>0800111</Called><Date>01.10.26 10:00</Date><Duration>0:05</Duration></Call></root>`, recent)
			return
		}
		p := map[string]string{}
		for _, m := range param.FindAllStringSubmatch(r.Header.Get("Authorization"), -1) {
			p[m[1]] = m[2]
		}
		want := h(h(user+":"+realm+":"+pass) + ":" + nonce + ":" + p["nc"] + ":" + p["cnonce"] + ":auth:" + h("POST:"+r.URL.Path))
		if p["response"] != want {
			w.Header().Set("WWW-Authenticate", `Digest realm="`+realm+`", nonce="`+nonce+`", algorithm=MD5, qop="auth"`)
			w.WriteHeader(401)
			return
		}
		body, _ := io.ReadAll(r.Body)
		key := strings.TrimPrefix(r.Header.Get("SoapAction"), "urn:dslforum-org:service:")
		if strings.HasSuffix(key, "#GetGenericDeviceInfos") {
			idx := regexp.MustCompile(`<NewIndex>(\d+)</NewIndex>`).FindSubmatch(body)
			key += "/" + string(idx[1])
		}
		if strings.HasSuffix(key, "#SetSwitch") {
			switched = string(body)
			io.WriteString(w, `<s:Envelope><s:Body></s:Body></s:Envelope>`)
			return
		}
		a, ok := answers[key]
		if !ok {
			w.WriteHeader(500)
			io.WriteString(w, `<s:Envelope><s:Body><s:Fault><detail><UPnPError><errorCode>713</errorCode></UPnPError></detail></s:Fault></s:Body></s:Envelope>`)
			return
		}
		io.WriteString(w, `<?xml version="1.0"?><s:Envelope><s:Body><u:Resp>`+a+`</u:Resp></s:Body></s:Envelope>`)
	}))
	defer srv.Close()
	// The test server runs on its own port, which tr064Base keeps only for 49000/49443:
	// point the box at the test server via a custom client that rewrites the port.
	base := strings.Replace(srv.URL, "127.0.0.1", "localhost", 1)
	client := srv.Client()
	client.Transport = rewritePort{srv.Listener.Addr().String(), http.DefaultTransport}
	in := &Instance{BaseURL: base, Values: map[string]string{"username": user, "password": pass}, Client: client, Lang: "de"}

	w, err := fritzbox{}.Fetch(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	s := stats(w)
	for k, v := range map[string]string{"Internet": "Verbunden", "↓ Download": "20,0 Mbit/s", "↑ Upload": "1,0 Mbit/s", "Leitung": "250 / 40 Mbit/s",
		"Externe IP": "203.0.113.7", "Geräte online": "2/3", "WLAN-Geräte": "7", "Verpasst (24 Std.)": "1"} {
		if s[k] != v {
			t.Errorf("%s = %q, want %q (all %v)", k, s[k], v, s)
		}
	}
	titles := []string{}
	for _, it := range w.Items {
		titles = append(titles, it.Title+"|"+it.Subtitle+"|"+it.Badge)
	}
	all := strings.Join(titles, "\n")
	for _, want := range []string{"✗ Mama|", "↗ 0800111|", "📶 Zuhause|ax|7 Geräte", "🏠 Kaffeemaschine|1234,5 W · 21,5 °C|An"} {
		if !strings.Contains(all, want) {
			t.Errorf("missing %q in\n%s", want, all)
		}
	}
	var socket Item
	for _, it := range w.Items {
		if strings.Contains(it.Title, "Kaffeemaschine") {
			socket = it
		}
	}
	if err := (fritzbox{}).Action(context.Background(), in, "switch", socket.Actions[0].Params); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(switched, "<NewAIN>11657 0240192</NewAIN>") || !strings.Contains(switched, "<NewSwitchState>TOGGLE</NewSwitchState>") {
		t.Fatalf("switch body %s", switched)
	}

	// Only the call list chosen
	in.Values["showCalls"] = "true"
	w, _ = fritzbox{}.Fetch(context.Background(), in)
	if _, ok := stats(w)["Internet"]; ok || len(w.Items) != 2 {
		t.Fatalf("only calls: %v %+v", stats(w), w.Items)
	}

	in.Values["password"] = "falsch"
	if _, err := (fritzbox{}).Fetch(context.Background(), in); err == nil || !strings.Contains(err.Error(), "falsch") {
		t.Fatalf("wrong password: %v", err)
	}
	if got := tr064Base("http://192.168.178.1"); got != "http://192.168.178.1:49000" {
		t.Errorf("tr064Base %s", got)
	}
	if got := tr064Base("https://fritz.box:49443/"); got != "https://fritz.box:49443" {
		t.Errorf("tr064Base %s", got)
	}
}

// rewritePort sends every request to the test server, whatever port it names.
type rewritePort struct {
	addr string
	next http.RoundTripper
}

func (r rewritePort) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Host = r.addr
	return r.next.RoundTrip(req)
}
