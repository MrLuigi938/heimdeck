package integrations

import "testing"

func TestMatch(t *testing.T) {
	cases := map[string]string{
		"lscr.io/linuxserver/radarr:latest":          "radarr",
		"ghcr.io/hotio/sonarr:release":               "sonarr",
		"fallenbagel/jellyseerr:2.5":                 "jellyseerr",
		"ghcr.io/paperless-ngx/paperless-ngx:latest": "paperless",
		"jlesage/jdownloader-2":                      "jdownloader",
		"ghcr.io/alexta69/metube":                    "metube",
		"rommapp/romm:3.8":                           "romm",
		"stashapp/stash:latest":                      "stash",
		"zoraxy/zoraxy:latest":                       "zoraxy",
		"localhost:5000/prowlarr":                    "prowlarr",
	}
	for image, want := range cases {
		in, ok := Match(image)
		if !ok || in.Def().ID != want {
			got := "<none>"
			if ok {
				got = in.Def().ID
			}
			t.Errorf("Match(%q) = %s, want %s", image, got, want)
		}
	}
	if _, ok := Match("custom/thing"); ok {
		t.Error("unknown image should not match")
	}
}

func TestFormatting(t *testing.T) {
	de := &Instance{Lang: "de"}
	en := &Instance{Lang: "en"}
	if got := de.Bytes(5.2 * 1024 * 1024 * 1024); got != "5,2 GB" {
		t.Errorf("Bytes(de) = %q", got)
	}
	if got := en.Bytes(5.2 * 1024 * 1024 * 1024); got != "5.2 GB" {
		t.Errorf("Bytes(en) = %q", got)
	}
	if got := de.Num(11250); got != "11.250" {
		t.Errorf("Num(de) = %q", got)
	}
	if got := en.Num(11250); got != "11,250" {
		t.Errorf("Num(en) = %q", got)
	}
	if got := en.T("noch %s", "5 min"); got != "5 min left" {
		t.Errorf("T(en) = %q", got)
	}
	if got := en.Status("downloading"); got != "Downloading" {
		t.Errorf("Status(en) = %q", got)
	}
}
