package logging

import "testing"

func TestRedact(t *testing.T) {
	for in, want := range map[string]string{
		`Get "https://sab.example/api?mode=queue&apikey=234b86c4&output=json": timeout`: `Get "https://sab.example/api?mode=queue&apikey=***&output=json": timeout`,
		`Post "https://discord.com/api/webhooks/123456/abcDEF-ghi": EOF`:                `Post "https://discord.com/api/webhooks/123456/***": EOF`,
		`Get "http://admin:geheim@192.168.1.5/status": refused`:                         `Get "http://***@192.168.1.5/status": refused`,
		`dial tcp 192.168.1.5:8080: connection refused`:                                 `dial tcp 192.168.1.5:8080: connection refused`,
		`Post "https://api.telegram.org/bot123456:AAH-x_y/sendMessage": EOF`:            `Post "https://api.telegram.org/bot***/sendMessage": EOF`,
		`https://x/api?token=abc;key=def`:                                               `https://x/api?token=***;key=***`,
	} {
		if got := Redact(in); got != want {
			t.Errorf("Redact(%q)\n got %q\nwant %q", in, got, want)
		}
	}
}
