package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestVersionOrder(t *testing.T) {
	for _, c := range []struct {
		a, b  string
		newer bool
	}{
		{"1.2.0", "1.1.1", true},
		{"v1.10.0", "1.9.9", true},
		{"1.2.0", "1.2.0-beta.1", true},
		{"1.2.0-beta.1", "1.2.0", false},
		{"1.2.0-beta.2", "1.2.0-beta.1", true},
		{"1.2.0-beta.10", "1.2.0-beta.9", true},
		{"1.2.0-rc.1", "1.2.0-beta.3", true},
		{"1.1.1", "1.1.1", false},
	} {
		if got := parseVersion(c.a).newer(parseVersion(c.b)); got != c.newer {
			t.Errorf("%s newer than %s: got %v", c.a, c.b, got)
		}
	}
	if parseVersion("dev").ok || parseVersion("1.2").ok {
		t.Error("invalid versions parsed")
	}
}

func TestNewestRelease(t *testing.T) {
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[
			{"tag_name":"v1.3.0-beta.1","html_url":"u-beta","prerelease":true},
			{"tag_name":"v1.2.1","html_url":"u-121"},
			{"tag_name":"v1.4.0","html_url":"u-draft","draft":true},
			{"tag_name":"v1.1.1","html_url":"u-111"}]`))
	}))
	defer gh.Close()
	old := releasesURL
	releasesURL = gh.URL
	defer func() { releasesURL = old }()

	for current, want := range map[string]string{
		"1.1.1":        "1.2.1",        // stable: betas are left out
		"1.2.1":        "",             // up to date
		"1.2.0-beta.1": "1.3.0-beta.1", // a beta also gets newer betas
	} {
		got, err := newestRelease(context.Background(), current)
		if err != nil {
			t.Fatal(err)
		}
		if (got == nil && want != "") || (got != nil && got.Version != want) {
			t.Errorf("%s: got %+v, want %q", current, got, want)
		}
	}
}
