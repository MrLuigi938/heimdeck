package server

import (
	"fmt"
	"net"
	"strings"
	"testing"
)

// A camera that cannot be reached: the tile says so, but neither the viewer
// nor the owner gets its address or login in the answer.
func TestCameraErrorsHideTheAddress(t *testing.T) {
	// A port where nothing listens.
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	_, port, _ := net.SplitHostPort(addr)

	_, base := multiUserServer(t)
	luis := newTestClient(t, base)
	luis.call("POST", "/api/setup", map[string]string{"username": "luis", "password": "admin-pw-123", "language": "de"})
	_, g := luis.call("POST", "/api/groups", map[string]any{"name": "Kameras"})

	for _, config := range []map[string]string{
		{"rtsp": "rtsp://kamera:geheim@" + addr + "/live"},
		{"rtsp": "rtsp://" + addr + "/live", "username": "kamera", "snapshot": "http://" + addr + "/snap.jpg"},
		{"onvif": "http://" + addr},
	} {
		tile := map[string]any{"groupId": g["id"], "name": "Eingang", "integration": "camera", "showWidget": true, "config": config}
		_, created := luis.call("POST", "/api/tiles", tile)
		answers := []string{}
		for _, path := range []string{"still", "live"} {
			code, res := luis.call("GET", fmt.Sprintf("/api/tiles/%v/camera/%s", created["id"], path), nil)
			if code < 400 {
				t.Fatalf("%s %v: status %d %v", path, config, code, res)
			}
			answers = append(answers, fmt.Sprint(res["error"]))
		}
		_, tested := luis.call("POST", "/api/tiles/test", tile)
		if tested["ok"] == true {
			t.Fatalf("test of an unreachable camera passed: %v", tested)
		}
		answers = append(answers, fmt.Sprint(tested["error"]))
		for _, a := range answers {
			if a == "" || a == "<nil>" {
				t.Fatalf("no message for %v", config)
			}
			for _, leak := range []string{"127.0.0.1", port, "geheim", "kamera:", "dial tcp", "rtsp://", "http://"} {
				if strings.Contains(a, leak) {
					t.Fatalf("%q in the answer %q for %v", leak, a, config)
				}
			}
		}
	}
}
