package integrations

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// FRITZ!Box: AVM's TR-064 interface (avm.de/service/schnittstellen), SOAP on
// port 49000 with Digest auth. One tile can show internet, devices, the call
// list, smart home (DECT) and Wi-Fi; which parts is up to the user.

type fritzbox struct{}

func init() { register(fritzbox{}) }

func (fritzbox) Def() Definition {
	return Definition{
		ID: "fritzbox", Name: "FRITZ!Box", Icon: "fritzbox", Category: "Netzwerk",
		Description: "Internet (Verbindung, Datenrate, externe IP), Geräte im Heimnetz, Anrufliste, Smart-Home-Geräte (Steckdosen schalten, Thermostate) und WLAN",
		Setup: []string{
			"In der FRITZ!Box die Schnittstelle einschalten: Heimnetz → Netzwerk → Netzwerkeinstellungen → „Zugriff für Anwendungen zulassen“ (unter „Heimnetzfreigaben“).",
			"Einen eigenen Benutzer für Heimdeck anlegen: System → FRITZ!Box-Benutzer → Benutzer hinzufügen, mit den Rechten „FRITZ!Box Einstellungen“, „Sprachnachrichten, Faxnachrichten, FRITZ!App Fon und Anrufliste“ und „Smart Home“ – je nachdem, was die Kachel zeigen soll.",
			"Als lokale Adresse die Adresse der FRITZ!Box eintragen, meist http://192.168.178.1 oder http://fritz.box. Heimdeck spricht die Schnittstelle dann über Port 49000 an.",
			"Unten auswählen, was die Kachel zeigt – ohne Auswahl zeigt sie alles, wofür der Benutzer Rechte hat.",
		},
		Fields: []Field{
			{Key: "username", Label: "Benutzername", Type: "text", Help: "Der FRITZ!Box-Benutzer für Heimdeck. Ohne eigene Benutzer: der angezeigte Name unter System → FRITZ!Box-Benutzer (z. B. fritz1234)"},
			{Key: "password", Label: "Passwort", Type: "password", Required: true, Secret: true},
			{Key: "showInternet", Label: "Internet: Verbindung, Datenrate, externe IP", Type: "checkbox", Display: true},
			{Key: "showHosts", Label: "Geräte im Heimnetz", Type: "checkbox", Display: true},
			{Key: "showCalls", Label: "Anrufliste", Type: "checkbox", Display: true},
			{Key: "showSmartHome", Label: "Smart Home (DECT-Steckdosen, Thermostate)", Type: "checkbox", Display: true},
			{Key: "showWifi", Label: "WLAN-Netze", Type: "checkbox", Display: true},
		},
		HasWidget: true, Images: []string{}, Port: 80,
		Refresh: 30,
	}
}

// tr064Base is the TR-064 address for the box: port 49000 on the same host,
// unless the address already names a TR-064 port.
func tr064Base(base string) string {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return base
	}
	if p := u.Port(); p == "49000" || p == "49443" {
		return strings.TrimRight(base, "/")
	}
	return "http://" + net.JoinHostPort(u.Hostname(), "49000")
}

type fritzService struct{ control, typ string }

var (
	fbDeviceInfo = fritzService{"/upnp/control/deviceinfo", "urn:dslforum-org:service:DeviceInfo:1"}
	fbWANCommon  = fritzService{"/upnp/control/wancommonifconfig1", "urn:dslforum-org:service:WANCommonInterfaceConfig:1"}
	fbWANPPP     = fritzService{"/upnp/control/wanpppconn1", "urn:dslforum-org:service:WANPPPConnection:1"}
	fbWANIP      = fritzService{"/upnp/control/wanipconnection1", "urn:dslforum-org:service:WANIPConnection:1"}
	fbHosts      = fritzService{"/upnp/control/hosts", "urn:dslforum-org:service:Hosts:1"}
	fbOnTel      = fritzService{"/upnp/control/x_contact", "urn:dslforum-org:service:X_AVM-DE_OnTel:1"}
	fbHomeauto   = fritzService{"/upnp/control/x_homeauto", "urn:dslforum-org:service:X_AVM-DE_Homeauto:1"}
)

func fbWLAN(n int) fritzService {
	return fritzService{fmt.Sprintf("/upnp/control/wlanconfig%d", n), fmt.Sprintf("urn:dslforum-org:service:WLANConfiguration:%d", n)}
}

// fritzFault is a SOAP error, e.g. 606 (no permission) or 713 (index too high).
type fritzFault struct{ code int }

func (f fritzFault) Error() string { return "TR-064 Fehler " + strconv.Itoa(f.code) }

// call runs a TR-064 action and returns its "New…" output arguments.
func (fritzbox) call(ctx context.Context, in *Instance, svc fritzService, action string, args map[string]string) (map[string]string, error) {
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="utf-8"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body>`)
	fmt.Fprintf(&body, `<u:%s xmlns:u="%s">`, action, svc.typ)
	keys := make([]string, 0, len(args))
	for k := range args {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Fprintf(&body, "<%s>%s</%s>", k, html.EscapeString(args[k]), k)
	}
	fmt.Fprintf(&body, `</u:%s></s:Body></s:Envelope>`, action)

	u := tr064Base(in.BaseURL) + svc.control
	send := func(auth string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(body.String()))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
		req.Header.Set("SoapAction", svc.typ+"#"+action)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		return in.Client.Do(req)
	}
	resp, err := send("")
	if err != nil {
		// The connection itself failed (refused, no answer) – checked on the error
		// type, as the message text depends on the system language.
		var op *net.OpError
		if errors.As(err, &op) && op.Op == "dial" {
			return nil, in.Errorf("Die FRITZ!Box antwortet nicht auf Port 49000 – ist unter Heimnetz → Netzwerk → Netzwerkeinstellungen „Zugriff für Anwendungen zulassen“ eingeschaltet?")
		}
		return nil, friendlyNetErr(in.Lang, err)
	}
	if resp.StatusCode == http.StatusUnauthorized {
		challenge := resp.Header.Get("WWW-Authenticate")
		resp.Body.Close()
		uri := strings.TrimPrefix(u, tr064Base(in.BaseURL))
		if resp, err = send(digestAuth(challenge, in.V("username"), in.Values["password"], http.MethodPost, uri)); err != nil {
			return nil, friendlyNetErr(in.Lang, err)
		}
		if resp.StatusCode == http.StatusUnauthorized {
			resp.Body.Close()
			return nil, in.Errorf("Benutzername oder Passwort falsch")
		}
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusInternalServerError {
		if m := regexp.MustCompile(`<errorCode>(\d+)</errorCode>`).FindSubmatch(raw); m != nil {
			code, _ := strconv.Atoi(string(m[1]))
			return nil, fritzFault{code}
		}
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &HTTPError{Status: resp.StatusCode, Lang: in.Lang}
	}
	return soapValues(raw), nil
}

// soapValues collects the text of all elements in the answer, by local name.
func soapValues(raw []byte) map[string]string {
	out := map[string]string{}
	dec := xml.NewDecoder(bytes.NewReader(raw))
	var name string
	for {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name = t.Name.Local
		case xml.CharData:
			if name != "" {
				out[name] += string(t)
			}
		case xml.EndElement:
			name = ""
		}
	}
}

// fetchXML loads a list the box links to (call list, host list) from the TR-064 port.
func (fritzbox) fetchXML(ctx context.Context, in *Instance, link string, out any) error {
	u, err := url.Parse(link)
	if err != nil {
		return err
	}
	base, _ := url.Parse(tr064Base(in.BaseURL))
	// Lists are served by the box itself; only take path and query from the link.
	target := base.ResolveReference(&url.URL{Path: u.Path, RawQuery: u.RawQuery})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return err
	}
	resp, err := in.Client.Do(req)
	if err != nil {
		return friendlyNetErr(in.Lang, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return &HTTPError{Status: resp.StatusCode, Lang: in.Lang}
	}
	return xml.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(out)
}

func (f fritzbox) Test(ctx context.Context, in *Instance) error {
	_, err := f.call(ctx, in, fbDeviceInfo, "GetInfo", nil)
	return err
}

// show reports whether a part was chosen; nothing chosen means everything.
func (fritzbox) show(in *Instance, key string) bool {
	chosen := false
	for _, k := range []string{"showInternet", "showHosts", "showCalls", "showSmartHome", "showWifi"} {
		chosen = chosen || in.V(k) == "true"
	}
	return !chosen || in.V(key) == "true"
}

func (f fritzbox) Fetch(ctx context.Context, in *Instance) (*Widget, error) {
	info, err := f.call(ctx, in, fbDeviceInfo, "GetInfo", nil)
	if err != nil {
		if fe, ok := err.(fritzFault); ok && fe.code == 606 {
			return nil, in.Errorf("Dem Benutzer fehlen Rechte – unter System → FRITZ!Box-Benutzer „FRITZ!Box Einstellungen“ erlauben")
		}
		return nil, err
	}
	w := &Widget{}
	num := func(s string) float64 { v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64); return v }
	dec := func(v float64, d int) string {
		s := strconv.FormatFloat(v, 'f', d, 64)
		if in.Lang != "en" {
			s = strings.Replace(s, ".", ",", 1)
		}
		return s
	}

	if f.show(in, "showInternet") {
		link, err := f.call(ctx, in, fbWANCommon, "GetCommonLinkProperties", nil)
		if err == nil {
			up := link["NewPhysicalLinkStatus"] == "Up"
			w.Stats = append(w.Stats, Stat{Label: in.T("Internet"), Value: in.T(map[bool]string{true: "Verbunden", false: "Getrennt"}[up]), Tone: toneIf(!up, "bad")})
			if !up {
				w.Alerts = append(w.Alerts, in.T("Keine Internetverbindung"))
			}
			if rates, err := f.call(ctx, in, fbWANCommon, "GetAddonInfos", nil); err == nil {
				w.Stats = append(w.Stats,
					Stat{Label: "↓ " + in.T("Download"), Value: dec(num(rates["NewByteReceiveRate"])*8/1e6, 1) + " Mbit/s"},
					Stat{Label: "↑ " + in.T("Upload"), Value: dec(num(rates["NewByteSendRate"])*8/1e6, 1) + " Mbit/s"})
			}
			if d, u := num(link["NewLayer1DownstreamMaxBitRate"]), num(link["NewLayer1UpstreamMaxBitRate"]); d > 0 {
				w.Stats = append(w.Stats, Stat{Label: in.T("Leitung"), Value: dec(d/1e6, 0) + " / " + dec(u/1e6, 0) + " Mbit/s"})
			}
		}
		for _, svc := range []fritzService{fbWANPPP, fbWANIP} {
			if ip, err := f.call(ctx, in, svc, "GetExternalIPAddress", nil); err == nil && ip["NewExternalIPAddress"] != "" && ip["NewExternalIPAddress"] != "0.0.0.0" {
				w.Stats = append(w.Stats, Stat{Label: in.T("Externe IP"), Value: ip["NewExternalIPAddress"]})
				break
			}
		}
	}

	if f.show(in, "showHosts") {
		if path, err := f.call(ctx, in, fbHosts, "X_AVM-DE_GetHostListPath", nil); err == nil {
			var list struct {
				Items []struct {
					Active string `xml:"Active"`
				} `xml:"Item"`
			}
			if f.fetchXML(ctx, in, path["NewX_AVM-DE_HostListPath"], &list) == nil {
				active := 0
				for _, it := range list.Items {
					if it.Active == "1" {
						active++
					}
				}
				w.Stats = append(w.Stats, Stat{Label: in.T("Geräte online"), Value: fmt.Sprintf("%d/%d", active, len(list.Items))})
			}
		}
	}

	if f.show(in, "showWifi") {
		clients := 0
		for n := 1; n <= 4; n++ {
			wl, err := f.call(ctx, in, fbWLAN(n), "GetInfo", nil)
			if err != nil {
				break
			}
			assoc, _ := f.call(ctx, in, fbWLAN(n), "GetTotalAssociations", nil)
			c := int(num(assoc["NewTotalAssociations"]))
			clients += c
			on := wl["NewEnable"] == "1"
			badge := in.T("Aus")
			if on {
				badge = fmt.Sprintf("%d %s", c, in.T("Geräte"))
			}
			w.Items = append(w.Items, Item{Title: "📶 " + firstNonEmpty(wl["NewSSID"], fmt.Sprintf("WLAN %d", n)), Subtitle: wl["NewStandard"], Badge: badge})
		}
		if clients > 0 {
			w.Stats = append(w.Stats, Stat{Label: in.T("WLAN-Geräte"), Value: in.Num(clients)})
		}
	}

	if f.show(in, "showSmartHome") {
		for i := 0; i < 20; i++ {
			d, err := f.call(ctx, in, fbHomeauto, "GetGenericDeviceInfos", map[string]string{"NewIndex": strconv.Itoa(i)})
			if err != nil {
				break // 713: no more devices, 606: no smart home permission
			}
			name := firstNonEmpty(d["NewDeviceName"], d["NewAIN"])
			it := Item{Title: "🏠 " + name}
			if d["NewPresent"] != "CONNECTED" {
				it.Badge = in.T("Nicht verbunden")
				w.Items = append(w.Items, it)
				continue
			}
			var parts []string
			if d["NewMultimeterIsEnabled"] == "ENABLED" {
				parts = append(parts, dec(num(d["NewMultimeterPower"])/100, 1)+" W")
			}
			if d["NewTemperatureIsEnabled"] == "ENABLED" {
				parts = append(parts, dec(num(d["NewTemperatureCelsius"])/10, 1)+" °C")
			}
			if d["NewHkrIsEnabled"] == "ENABLED" && d["NewHkrSetTemperature"] != "" {
				parts = append(parts, in.T("Soll %s °C", dec(num(d["NewHkrSetTemperature"])/10, 1)))
			}
			it.Subtitle = strings.Join(parts, " · ")
			if d["NewSwitchIsEnabled"] == "ENABLED" {
				it.Badge = in.T(map[bool]string{true: "An", false: "Aus"}[d["NewSwitchState"] == "ON"])
				it.Actions = []Action{{ID: "switch", Label: in.T("Umschalten"), Params: map[string]string{"ain": d["NewAIN"]}}}
			}
			w.Items = append(w.Items, it)
		}
	}

	if f.show(in, "showCalls") {
		f.addCalls(ctx, in, w)
	}

	if w.Stats == nil && w.Items == nil {
		w.Stats = []Stat{{Label: in.T("Modell"), Value: info["NewModelName"]}, {Label: "FRITZ!OS", Value: info["NewSoftwareVersion"]}}
	}
	if up := num(info["NewUpTime"]); up > 0 && len(w.Stats) < 6 {
		w.Stats = append(w.Stats, Stat{Label: in.T("Laufzeit"), Value: uptimeText(in, up)})
	}
	return w, nil
}

var fbCallDate = regexp.MustCompile(`^(\d\d)\.(\d\d)\.(\d\d) (\d\d):(\d\d)$`)

// addCalls lists the latest calls and counts missed ones of the last day.
func (f fritzbox) addCalls(ctx context.Context, in *Instance, w *Widget) {
	res, err := f.call(ctx, in, fbOnTel, "GetCallList", nil)
	if err != nil || res["NewCallListURL"] == "" {
		return
	}
	link := res["NewCallListURL"]
	if strings.Contains(link, "?") {
		link += "&max=20"
	}
	var list struct {
		Calls []struct {
			Type     string `xml:"Type"`
			Caller   string `xml:"Caller"`
			Called   string `xml:"Called"`
			Name     string `xml:"Name"`
			Date     string `xml:"Date"`
			Duration string `xml:"Duration"`
		} `xml:"Call"`
	}
	if f.fetchXML(ctx, in, link, &list) != nil {
		return
	}
	missed := 0
	var items []Item
	for _, c := range list.Calls {
		var when time.Time
		if m := fbCallDate.FindStringSubmatch(c.Date); m != nil {
			when, _ = time.ParseInLocation("02.01.06 15:04", c.Date, time.Local)
		}
		icon, other := "📞", c.Caller
		switch c.Type {
		case "1": // incoming
			icon = "↙"
		case "2", "10": // missed, rejected
			icon = "✗"
			if !when.IsZero() && time.Since(when) < 24*time.Hour {
				missed++
			}
		case "3": // outgoing
			icon, other = "↗", c.Called
		default:
			continue // active calls and the like
		}
		if len(items) < 5 {
			sub := c.Date
			if !when.IsZero() {
				sub = in.Since(when)
			}
			if c.Duration != "" && c.Duration != "0:00" && c.Type != "2" {
				sub += " · " + c.Duration
			}
			items = append(items, Item{Title: icon + " " + firstNonEmpty(c.Name, other, in.T("Unbekannt")), Subtitle: sub})
		}
	}
	w.Stats = append(w.Stats, Stat{Label: in.T("Verpasst (24 Std.)"), Value: in.Num(missed), Tone: toneIf(missed > 0, "warn")})
	if len(items) > 0 {
		// Calls first: they are what you look at most.
		w.Items = append(items, w.Items...)
		if w.ItemsTitle == "" {
			w.ItemsTitle = in.T("Letzte Anrufe")
		}
	}
}

// Action switches a DECT socket.
func (f fritzbox) Action(ctx context.Context, in *Instance, action string, p map[string]string) error {
	ain := p["ain"]
	if action != "switch" || !regexp.MustCompile(`^[0-9A-Fa-f: -]{5,30}$`).MatchString(ain) {
		return in.Errorf("unbekannte Aktion %q", action)
	}
	_, err := f.call(ctx, in, fbHomeauto, "SetSwitch", map[string]string{"NewAIN": ain, "NewSwitchState": "TOGGLE"})
	if fe, ok := err.(fritzFault); ok && fe.code == 606 {
		return in.Errorf("Dem Benutzer fehlt das Recht „Smart Home“")
	}
	return err
}
