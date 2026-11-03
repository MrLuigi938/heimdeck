package camera

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// A camera that speaks just enough ONVIF.
func fakeONVIF(t *testing.T) (*httptest.Server, *atomic.Int32) {
	pulls := &atomic.Int32{}
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		b := string(body)
		if !strings.Contains(b, "GetSystemDateAndTime") && !strings.Contains(b, "<wsse:Username>admin</wsse:Username>") {
			w.WriteHeader(400)
			io.WriteString(w, `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body><s:Fault><s:Reason><s:Text>NotAuthorized</s:Text></s:Reason></s:Fault></s:Body></s:Envelope>`)
			return
		}
		reply := func(inner string) {
			io.WriteString(w, `<?xml version="1.0"?><s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope" xmlns:tt="http://www.onvif.org/ver10/schema"><s:Body>`+inner+`</s:Body></s:Envelope>`)
		}
		now := time.Now().UTC()
		switch {
		case strings.Contains(b, "GetSystemDateAndTime"):
			reply(`<tds:GetSystemDateAndTimeResponse><tds:SystemDateAndTime><tt:UTCDateTime><tt:Time><tt:Hour>` + itoa(now.Hour()) + `</tt:Hour><tt:Minute>` + itoa(now.Minute()) + `</tt:Minute><tt:Second>0</tt:Second></tt:Time><tt:Date><tt:Year>` + itoa(now.Year()) + `</tt:Year><tt:Month>` + itoa(int(now.Month())) + `</tt:Month><tt:Day>` + itoa(now.Day()) + `</tt:Day></tt:Date></tt:UTCDateTime></tds:SystemDateAndTime></tds:GetSystemDateAndTimeResponse>`)
		case strings.Contains(b, "GetCapabilities"):
			// The camera reports an address that only works inside its own network.
			reply(`<tds:GetCapabilitiesResponse><tds:Capabilities><tt:Events><tt:XAddr>http://10.9.9.9/onvif/events</tt:XAddr></tt:Events><tt:Media><tt:XAddr>http://10.9.9.9/onvif/media</tt:XAddr></tt:Media></tds:Capabilities></tds:GetCapabilitiesResponse>`)
		case strings.Contains(b, "GetProfiles"):
			reply(`<trt:GetProfilesResponse><trt:Profiles token="main"/></trt:GetProfilesResponse>`)
		case strings.Contains(b, "GetStreamUri"):
			reply(`<trt:GetStreamUriResponse><trt:MediaUri><tt:Uri>rtsp://10.9.9.9:554/stream1</tt:Uri></trt:MediaUri></trt:GetStreamUriResponse>`)
		case strings.Contains(b, "GetSnapshotUri"):
			reply(`<trt:GetSnapshotUriResponse><trt:MediaUri><tt:Uri>http://10.9.9.9/snap.jpg</tt:Uri></trt:MediaUri></trt:GetSnapshotUriResponse>`)
		case strings.Contains(b, "CreatePullPointSubscription"):
			reply(`<tev:CreatePullPointSubscriptionResponse><tev:SubscriptionReference><wsa:Address>http://10.9.9.9/onvif/pull/1</wsa:Address></tev:SubscriptionReference></tev:CreatePullPointSubscriptionResponse>`)
		case strings.Contains(b, "PullMessages"):
			n := pulls.Add(1)
			msgs := ""
			if n == 1 {
				msgs = `<wsnt:NotificationMessage><wsnt:Topic>tns1:RuleEngine/CellMotionDetector/Motion</wsnt:Topic><wsnt:Message><tt:Message><tt:Data><tt:SimpleItem Name="IsMotion" Value="true"/></tt:Data></tt:Message></wsnt:Message></wsnt:NotificationMessage>` +
					`<wsnt:NotificationMessage><wsnt:Topic>tns1:RuleEngine/MyRuleDetector/PeopleDetect</wsnt:Topic><wsnt:Message><tt:Message><tt:Data><tt:SimpleItem Name="State" Value="true"/></tt:Data></tt:Message></wsnt:Message></wsnt:NotificationMessage>`
			}
			if n == 2 {
				msgs = `<wsnt:NotificationMessage><wsnt:Topic>tns1:RuleEngine/CellMotionDetector/Motion</wsnt:Topic><wsnt:Message><tt:Message><tt:Data><tt:SimpleItem Name="IsMotion" Value="false"/></tt:Data></tt:Message></wsnt:Message></wsnt:NotificationMessage>`
			}
			reply(`<tev:PullMessagesResponse>` + msgs + `</tev:PullMessagesResponse>`)
		default:
			reply("")
		}
	}))
	return srv, pulls
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestONVIF(t *testing.T) {
	srv, _ := fakeONVIF(t)
	defer srv.Close()
	o := &ONVIF{Address: srv.URL, User: "admin", Pass: "pw"}
	info, err := o.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	host := strings.TrimPrefix(srv.URL, "http://")
	hostOnly := host[:strings.LastIndex(host, ":")]
	if info.StreamURI != "rtsp://"+hostOnly+":554/stream1" || info.SnapshotURI != srv.URL+"/snap.jpg" || !info.Events {
		t.Fatalf("info %+v", info)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var kinds []string
	o.PullEvents(ctx, func(k string) {
		kinds = append(kinds, k)
		if len(kinds) == 2 {
			time.AfterFunc(300*time.Millisecond, cancel) // the "off" message must not count
		}
	})
	if strings.Join(kinds, ",") != "Bewegung,Person" {
		t.Fatalf("events %v", kinds)
	}

	bad := &ONVIF{Address: srv.URL, User: "someone", Pass: "pw"}
	if _, err := bad.Info(context.Background()); err == nil || !strings.Contains(err.Error(), "Anmeldung") {
		t.Fatalf("wrong login: %v", err)
	}
}

func TestSnapshotDigest(t *testing.T) {
	jpeg := []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00rest")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := r.Header.Get("Authorization")
		if !strings.HasPrefix(a, "Digest ") {
			w.Header().Set("WWW-Authenticate", `Digest realm="cam", nonce="abc", qop="auth"`)
			w.WriteHeader(401)
			return
		}
		p := map[string]string{}
		for _, part := range splitParams(a[7:]) {
			k, v, _ := strings.Cut(strings.TrimSpace(part), "=")
			p[k] = strings.Trim(v, `"`)
		}
		h := func(s string) string { x := md5.Sum([]byte(s)); return hex.EncodeToString(x[:]) }
		want := h(h("admin:cam:pw") + ":abc:" + p["nc"] + ":" + p["cnonce"] + ":auth:" + h("GET:/snap.jpg"))
		if p["response"] != want {
			w.WriteHeader(401)
			return
		}
		w.Write(jpeg)
	}))
	defer srv.Close()
	data, ct, err := Snapshot(context.Background(), srv.Client(), srv.URL+"/snap.jpg", "admin", "pw")
	if err != nil || ct != "image/jpeg" || string(data) != string(jpeg) {
		t.Fatalf("%v %q", err, ct)
	}
}
