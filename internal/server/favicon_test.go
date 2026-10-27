package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestIconLinks(t *testing.T) {
	base, _ := url.Parse("http://nas.local:5000/web/")
	page := []byte(`<html><head>
		<link rel="mask-icon" href="/mask.svg">
		<link rel="shortcut icon" href="favicon-16.png" sizes="16x16">
		<LINK REL="apple-touch-icon" HREF='/apple.png'>
		<link href="/icons/192.png" rel="icon" sizes="192x192">
		<link rel="stylesheet" href="/x.css">`)
	got := iconLinks(page, base)
	want := []string{"http://nas.local:5000/icons/192.png", "http://nas.local:5000/apple.png", "http://nas.local:5000/web/favicon-16.png"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

func TestFetchFavicon(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n0000IHDR")
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<link rel="icon" href="/missing.png"><link rel="icon" href="/login.html" sizes="32x32">`))
	})
	mux.HandleFunc("/login.html", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("<html>login</html>")) })
	mux.HandleFunc("/favicon.ico", func(w http.ResponseWriter, r *http.Request) { w.Write(png) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	f, err := fetchFavicon(context.Background(), srv.URL, false, false)
	if err != nil || f.ctype != "image/png" {
		t.Fatalf("%v %q", err, f.ctype)
	}
	none := httptest.NewServer(http.NotFoundHandler())
	defer none.Close()
	if _, err := fetchFavicon(context.Background(), none.URL, false, false); err == nil {
		t.Fatal("found an icon where there is none")
	}
}
