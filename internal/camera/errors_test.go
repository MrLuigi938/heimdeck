package camera

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestPublicErrors(t *testing.T) {
	for _, tc := range []struct {
		in   error
		want string
	}{
		{errors.New(`dial tcp 192.168.1.50:554: connect: connection refused`), errUnreachable.Error()},
		{errors.New(`Post "http://192.168.1.50:8000/onvif": context deadline exceeded`), errUnreachable.Error()},
		{errors.New(`lookup kamera.local: no such host`), errUnreachable.Error()},
		{errors.New(`bad status from rtsp://admin:pw@192.168.1.50/live`), errGeneral.Error()},
		{fmt.Errorf("session: %w", shown("Die Kamera schickt kein Bild")), "Die Kamera schickt kein Bild"},
	} {
		if got := Public(tc.in).Error(); got != tc.want {
			t.Errorf("%v: got %q", tc.in, got)
		}
	}
	// A port with "401" in it is no refused login.
	if got := friendly(errors.New("dial tcp 10.0.0.5:4010: i/o timeout")).Error(); strings.Contains(got, "Anmeldung") {
		t.Errorf("address taken for a status: %q", got)
	}
	if got := friendly(errors.New("bad status code: 401 (Unauthorized)")).Error(); !strings.Contains(got, "Anmeldung") {
		t.Errorf("refused login: %q", got)
	}
}
