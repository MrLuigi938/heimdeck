package docker

import (
	"context"
	"os"
	"testing"
)

func TestParseRef(t *testing.T) {
	cases := map[string][3]string{
		"nginx":                             {"registry-1.docker.io", "library/nginx", "latest"},
		"jellyfin/jellyfin:10.10":           {"registry-1.docker.io", "jellyfin/jellyfin", "10.10"},
		"lscr.io/linuxserver/sonarr:latest": {"lscr.io", "linuxserver/sonarr", "latest"},
		"ghcr.io/mrluigi938/heimdeck":       {"ghcr.io", "mrluigi938/heimdeck", "latest"},
		"localhost:5000/app:dev":            {"localhost:5000", "app", "dev"},
		"docker.io/library/postgres:16":     {"registry-1.docker.io", "library/postgres", "16"},
	}
	for ref, want := range cases {
		h, r, tg := parseRef(ref)
		if [3]string{h, r, tg} != want {
			t.Errorf("%s: %s %s %s", ref, h, r, tg)
		}
	}
}

// TestRemoteDigest talks to a real registry; run with HEIMDECK_NET_TESTS=1.
func TestRemoteDigest(t *testing.T) {
	if os.Getenv("HEIMDECK_NET_TESTS") == "" {
		t.Skip("network test")
	}
	for _, ref := range []string{"ghcr.io/mrluigi938/heimdeck:latest"} {
		d, err := NewUpdates().remoteDigest(context.Background(), ref)
		t.Logf("%s → %s %v", ref, d, err)
		if err != nil || len(d) != 71 {
			t.Errorf("%s: %q %v", ref, d, err)
		}
	}
}
