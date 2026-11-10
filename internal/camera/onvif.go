package camera

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// A small ONVIF client (www.onvif.org, Profile S/T): it finds the stream and
// snapshot addresses and pulls the camera's events (PullPoint). Only the few
// calls Heimdeck needs, with WS-Security UsernameToken (digest) login.

type ONVIF struct {
	Address    string // http://192.168.1.50:8000 or the full device service address
	User, Pass string
	Client     *http.Client

	mu     sync.Mutex
	offset time.Duration // camera clock minus ours, for the login digest
	media  string
	events string
}

// ONVIFInfo is what a camera tells about itself.
type ONVIFInfo struct {
	StreamURI   string
	SnapshotURI string
	Events      bool
}

func (o *ONVIF) deviceURL() string {
	a := strings.TrimRight(strings.TrimSpace(o.Address), "/")
	if !strings.Contains(a, "://") {
		a = "http://" + a
	}
	if u, err := url.Parse(a); err == nil && (u.Path == "" || u.Path == "/") {
		return a + "/onvif/device_service"
	}
	return a
}

// soap sends one request and decodes the body's first element into out.
func (o *ONVIF) soap(ctx context.Context, addr, action, body string, out any, auth bool) error {
	var hdr string
	if auth && o.User != "" {
		hdr = "<s:Header>" + o.security() + "</s:Header>"
	}
	env := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" ` +
		`xmlns:tds="http://www.onvif.org/ver10/device/wsdl" xmlns:trt="http://www.onvif.org/ver10/media/wsdl" ` +
		`xmlns:tev="http://www.onvif.org/ver10/events/wsdl" xmlns:tt="http://www.onvif.org/ver10/schema" ` +
		`xmlns:wsnt="http://docs.oasis-open.org/wsn/b-2" xmlns:wsa="http://www.w3.org/2005/08/addressing">` +
		hdr + "<s:Body>" + body + "</s:Body></s:Envelope>"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, addr, strings.NewReader(env))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", `application/soap+xml; charset=utf-8; action="`+action+`"`)
	resp, err := o.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode == 401 || bytes.Contains(data, []byte("NotAuthorized")) {
		return shown("Die Kamera lehnt die ONVIF-Anmeldung ab – Benutzer und Passwort prüfen (bei manchen Kameras ist es ein eigenes ONVIF-Konto)")
	}
	if resp.StatusCode >= 400 {
		reason := between(string(data), "Text", "</")
		if i := strings.LastIndex(reason, ">"); i >= 0 {
			reason = reason[i+1:]
		}
		return fmt.Errorf("ONVIF: Status %d %s", resp.StatusCode, strings.TrimSpace(reason))
	}
	if out == nil {
		return nil
	}
	var env2 struct {
		Body struct {
			Inner []byte `xml:",innerxml"`
		} `xml:"Body"`
	}
	if err := xml.Unmarshal(data, &env2); err != nil {
		return err
	}
	return xml.Unmarshal(env2.Body.Inner, out)
}

func (o *ONVIF) client() *http.Client {
	if o.Client != nil {
		return o.Client
	}
	return &http.Client{Timeout: 15 * time.Second}
}

// security builds the UsernameToken with a password digest.
func (o *ONVIF) security() string {
	nonce := make([]byte, 16)
	rand.Read(nonce)
	o.mu.Lock()
	created := time.Now().Add(o.offset).UTC().Format("2006-01-02T15:04:05.000Z")
	o.mu.Unlock()
	h := sha1.New()
	h.Write(nonce)
	h.Write([]byte(created))
	h.Write([]byte(o.Pass))
	digest := base64.StdEncoding.EncodeToString(h.Sum(nil))
	return `<wsse:Security s:mustUnderstand="1" xmlns:wsse="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-secext-1.0.xsd" ` +
		`xmlns:wsu="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-wssecurity-utility-1.0.xsd"><wsse:UsernameToken>` +
		"<wsse:Username>" + xmlEscape(o.User) + "</wsse:Username>" +
		`<wsse:Password Type="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-username-token-profile-1.0#PasswordDigest">` + digest + "</wsse:Password>" +
		`<wsse:Nonce EncodingType="http://docs.oasis-open.org/wss/2004/01/oasis-200401-wss-soap-message-security-1.0#Base64Binary">` + base64.StdEncoding.EncodeToString(nonce) + "</wsse:Nonce>" +
		"<wsu:Created>" + created + "</wsu:Created></wsse:UsernameToken></wsse:Security>"
}

func xmlEscape(s string) string {
	var b bytes.Buffer
	xml.EscapeText(&b, []byte(s))
	return b.String()
}

// syncClock reads the camera's time: the login digest fails when the clocks differ.
func (o *ONVIF) syncClock(ctx context.Context) {
	var res struct {
		UTC struct {
			Date struct{ Year, Month, Day int }     `xml:"Date"`
			Time struct{ Hour, Minute, Second int } `xml:"Time"`
		} `xml:"SystemDateAndTime>UTCDateTime"`
	}
	if err := o.soap(ctx, o.deviceURL(), "http://www.onvif.org/ver10/device/wsdl/GetSystemDateAndTime", "<tds:GetSystemDateAndTime/>", &res, false); err != nil {
		return
	}
	d, t := res.UTC.Date, res.UTC.Time
	if d.Year < 2000 {
		return
	}
	cam := time.Date(d.Year, time.Month(d.Month), d.Day, t.Hour, t.Minute, t.Second, 0, time.UTC)
	o.mu.Lock()
	o.offset = time.Until(cam)
	o.mu.Unlock()
}

// Info connects and finds the stream, snapshot and event addresses.
func (o *ONVIF) Info(ctx context.Context) (ONVIFInfo, error) {
	o.syncClock(ctx)
	var caps struct {
		Media  string `xml:"Capabilities>Media>XAddr"`
		Events string `xml:"Capabilities>Events>XAddr"`
	}
	err := o.soap(ctx, o.deviceURL(), "http://www.onvif.org/ver10/device/wsdl/GetCapabilities",
		"<tds:GetCapabilities><tds:Category>All</tds:Category></tds:GetCapabilities>", &caps, true)
	if err != nil {
		return ONVIFInfo{}, onvifFriendly(err)
	}
	o.mu.Lock()
	o.media, o.events = o.fixHost(caps.Media), o.fixHost(caps.Events)
	media := o.media
	o.mu.Unlock()
	info := ONVIFInfo{Events: caps.Events != ""}
	if media == "" {
		return info, nil
	}
	var profiles struct {
		Profiles []struct {
			Token string `xml:"token,attr"`
		} `xml:"Profiles"`
	}
	if err := o.soap(ctx, media, "http://www.onvif.org/ver10/media/wsdl/GetProfiles", "<trt:GetProfiles/>", &profiles, true); err != nil || len(profiles.Profiles) == 0 {
		return info, nil
	}
	token := xmlEscape(profiles.Profiles[0].Token)
	var stream struct {
		URI string `xml:"MediaUri>Uri"`
	}
	if o.soap(ctx, media, "http://www.onvif.org/ver10/media/wsdl/GetStreamUri",
		`<trt:GetStreamUri><trt:StreamSetup><tt:Stream>RTP-Unicast</tt:Stream><tt:Transport><tt:Protocol>RTSP</tt:Protocol></tt:Transport></trt:StreamSetup>`+
			"<trt:ProfileToken>"+token+"</trt:ProfileToken></trt:GetStreamUri>", &stream, true) == nil {
		info.StreamURI = o.fixHost(strings.TrimSpace(stream.URI))
	}
	var snap struct {
		URI string `xml:"MediaUri>Uri"`
	}
	if o.soap(ctx, media, "http://www.onvif.org/ver10/media/wsdl/GetSnapshotUri",
		"<trt:GetSnapshotUri><trt:ProfileToken>"+token+"</trt:ProfileToken></trt:GetSnapshotUri>", &snap, true) == nil {
		info.SnapshotURI = o.fixHost(strings.TrimSpace(snap.URI))
	}
	return info, nil
}

// fixHost replaces the host a camera reports with the one it was reached
// at: behind NAT or Docker cameras often report an address that does not work.
func (o *ONVIF) fixHost(addr string) string {
	if addr == "" {
		return ""
	}
	u, err := url.Parse(addr)
	dev, err2 := url.Parse(o.deviceURL())
	if err != nil || err2 != nil {
		return addr
	}
	port := u.Port()
	u.Host = dev.Hostname()
	if port != "" {
		u.Host += ":" + port
	} else if u.Scheme == dev.Scheme && dev.Port() != "" {
		u.Host += ":" + dev.Port()
	}
	return u.String()
}

func onvifFriendly(err error) error {
	s := err.Error()
	switch {
	case isNetworkError(s), strings.Contains(s, "timeout"):
		return shown("ONVIF antwortet nicht – Adresse und Port prüfen (oft 80, 8000 oder 2020) und ob ONVIF in der Kamera eingeschaltet ist")
	}
	return err
}

// onvifMessage is one notification from a PullMessages answer.
type onvifMessage struct {
	Topic string `xml:"Topic"`
	Items []struct {
		Name  string `xml:"Name,attr"`
		Value string `xml:"Value,attr"`
	} `xml:"Message>Message>Data>SimpleItem"`
}

// Kind of an ONVIF event, or "" when it is not one Heimdeck shows (also
// for the "off" message at the end of a movement).
func (m onvifMessage) kind() string {
	t := strings.ToLower(m.Topic)
	on := false
	for _, it := range m.Items {
		v := strings.ToLower(it.Value)
		if v == "true" || v == "1" || v == "active" {
			on = true
		}
	}
	if !on {
		return ""
	}
	switch {
	case strings.Contains(t, "visitor"), strings.Contains(t, "doorbell"), strings.Contains(t, "call"):
		return "Klingel"
	case strings.Contains(t, "people"), strings.Contains(t, "person"), strings.Contains(t, "human"):
		return "Person"
	case strings.Contains(t, "vehicle"), strings.Contains(t, "car"):
		return "Fahrzeug"
	case strings.Contains(t, "animal"), strings.Contains(t, "pet"), strings.Contains(t, "dog"):
		return "Tier"
	case strings.Contains(t, "tamper"):
		return "Sabotage"
	case strings.Contains(t, "linedetector"), strings.Contains(t, "crossing"), strings.Contains(t, "intrusion"), strings.Contains(t, "fielddetector"):
		return "Bereich betreten"
	case strings.Contains(t, "motion"):
		return "Bewegung"
	}
	return ""
}

// PullEvents subscribes to the camera's events and calls on with the kind of
// each new one, until ctx ends. It returns on errors; the caller retries.
func (o *ONVIF) PullEvents(ctx context.Context, on func(kind string)) error {
	o.mu.Lock()
	addr := o.events
	o.mu.Unlock()
	if addr == "" {
		if _, err := o.Info(ctx); err != nil {
			return err
		}
		o.mu.Lock()
		addr = o.events
		o.mu.Unlock()
		if addr == "" {
			return shown("Die Kamera bietet über ONVIF keine Ereignisse an")
		}
	}
	var sub struct {
		Address string `xml:"SubscriptionReference>Address"`
	}
	if err := o.soap(ctx, addr, "http://www.onvif.org/ver10/events/wsdl/EventPortType/CreatePullPointSubscriptionRequest",
		"<tev:CreatePullPointSubscription><tev:InitialTerminationTime>PT10M</tev:InitialTerminationTime></tev:CreatePullPointSubscription>", &sub, true); err != nil {
		return err
	}
	pull := o.fixHost(strings.TrimSpace(sub.Address))
	if pull == "" {
		pull = addr
	}
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		o.soap(c, pull, "http://docs.oasis-open.org/wsn/bw-2/SubscriptionManager/UnsubscribeRequest", "<wsnt:Unsubscribe/>", nil, true)
	}()
	renewed := time.Now()
	last := map[string]time.Time{}
	for ctx.Err() == nil {
		if time.Since(renewed) > 5*time.Minute {
			if err := o.soap(ctx, pull, "http://docs.oasis-open.org/wsn/bw-2/SubscriptionManager/RenewRequest",
				"<wsnt:Renew><wsnt:TerminationTime>PT10M</wsnt:TerminationTime></wsnt:Renew>", nil, true); err != nil {
				return err
			}
			renewed = time.Now()
		}
		var res struct {
			Messages []onvifMessage `xml:"NotificationMessage"`
		}
		c, cancel := context.WithTimeout(ctx, 40*time.Second)
		err := o.soap(c, pull, "http://www.onvif.org/ver10/events/wsdl/PullPointSubscription/PullMessagesRequest",
			"<tev:PullMessages><tev:Timeout>PT20S</tev:Timeout><tev:MessageLimit>20</tev:MessageLimit></tev:PullMessages>", &res, true)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		for _, m := range res.Messages {
			k := m.kind()
			// Cameras repeat a running alarm; one event per kind and minute.
			if k == "" || time.Since(last[k]) < time.Minute {
				continue
			}
			last[k] = time.Now()
			on(k)
		}
	}
	return nil
}

// Snapshot loads a JPEG from the camera's snapshot address, with digest or
// basic login (most cameras use digest). The password goes out only once the
// camera asked for a login, and in the way it asked: basic sends it readable,
// which a camera that wants digest must not get.
func Snapshot(ctx context.Context, client *http.Client, addr, user, pass string) ([]byte, string, error) {
	get := func(auth string) (*http.Response, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, addr, nil)
		if err != nil {
			return nil, err
		}
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		return client.Do(req)
	}
	resp, err := get("")
	if err != nil {
		return nil, "", err
	}
	if resp.StatusCode == 401 && user != "" {
		chal := resp.Header.Get("WWW-Authenticate")
		resp.Body.Close()
		auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))
		if strings.HasPrefix(strings.ToLower(chal), "digest") {
			u, _ := url.Parse(addr)
			auth = digestAuth(chal, user, pass, "GET", u.RequestURI())
		}
		if resp, err = get(auth); err != nil {
			return nil, "", err
		}
		if resp.StatusCode == 401 {
			resp.Body.Close()
			return nil, "", shown("Die Kamera lehnt die Anmeldung für das Standbild ab")
		}
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", shown(fmt.Sprintf("Standbild: Status %d", resp.StatusCode))
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return nil, "", err
	}
	ct := http.DetectContentType(data)
	if !strings.HasPrefix(ct, "image/") {
		return nil, "", shown("Die Standbild-Adresse liefert kein Bild")
	}
	return data, ct, nil
}

// digestAuth answers an HTTP digest challenge (RFC 7616, MD5, qop=auth).
func digestAuth(chal, user, pass, method, uri string) string {
	p := map[string]string{}
	for _, part := range splitParams(chal[len("digest"):]) {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if ok {
			p[strings.ToLower(k)] = strings.Trim(v, `"`)
		}
	}
	h := func(s string) string { x := md5.Sum([]byte(s)); return hex.EncodeToString(x[:]) }
	ha1 := h(user + ":" + p["realm"] + ":" + pass)
	ha2 := h(method + ":" + uri)
	cnonceB := make([]byte, 8)
	rand.Read(cnonceB)
	cnonce := hex.EncodeToString(cnonceB)
	var resp, qop string
	if strings.Contains(p["qop"], "auth") {
		qop = "auth"
		resp = h(ha1 + ":" + p["nonce"] + ":00000001:" + cnonce + ":auth:" + ha2)
	} else {
		resp = h(ha1 + ":" + p["nonce"] + ":" + ha2)
	}
	s := fmt.Sprintf(`Digest username="%s", realm="%s", nonce="%s", uri="%s", response="%s"`, user, p["realm"], p["nonce"], uri, resp)
	if qop != "" {
		s += fmt.Sprintf(`, qop=auth, nc=00000001, cnonce="%s"`, cnonce)
	}
	if p["opaque"] != "" {
		s += fmt.Sprintf(`, opaque="%s"`, p["opaque"])
	}
	return s
}

// splitParams splits "a=1, b="x, y"" at the commas outside quotes.
func splitParams(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			cur.WriteRune(r)
		case r == ',' && !quoted:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	return append(out, cur.String())
}

func between(s, a, b string) string {
	i := strings.Index(s, a)
	if i < 0 {
		return ""
	}
	s = s[i+len(a):]
	if j := strings.Index(s, b); j >= 0 {
		return s[:j]
	}
	return s
}
