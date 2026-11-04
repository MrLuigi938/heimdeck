package camera

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/heimdeck/heimdeck/internal/camera/testcam"
)

// A connection that is open for one account must not serve an account that
// may not reach that address – neither a running stream nor a kept picture.
func TestRunningFeedDoesNotServeARestrictedAccount(t *testing.T) {
	cam, err := testcam.Start(freeAddr(t), 0)
	if err != nil {
		t.Fatal(err)
	}
	defer cam.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	allowed := Source{URL: cam.URL, Dial: (&net.Dialer{}).DialContext}
	if _, _, _, err := Still(ctx, allowed, time.Minute); err != nil {
		t.Fatal(err)
	}
	live, err := Watch(ctx, allowed)
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close()

	blocked := errors.New("blocked")
	restricted := Source{URL: cam.URL, Scope: "public", Dial: func(context.Context, string, string) (net.Conn, error) { return nil, blocked }}
	short, stop := context.WithTimeout(ctx, 3*time.Second)
	defer stop()
	if data, _, _, err := Still(short, restricted, time.Minute); err == nil {
		t.Fatalf("restricted account got a picture of %d bytes", len(data))
	}
	if l, err := Watch(short, restricted); err == nil {
		l.Close()
		t.Fatal("restricted account joined the running stream")
	}
}

// A camera that went away does not leave a viewer waiting on the old session.
func TestCameraGoneGivesAnError(t *testing.T) {
	addr := freeAddr(t)
	cam, err := testcam.Start(addr, 0)
	if err != nil {
		t.Fatal(err)
	}
	src := Source{URL: cam.URL, Dial: (&net.Dialer{}).DialContext}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	live, err := Watch(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	cam.Close()
	// The running viewer ends …
	end := time.After(15 * time.Second)
wait:
	for {
		select {
		case _, ok := <-live.Frames:
			if !ok {
				break wait
			}
		case <-end:
			t.Fatal("viewer still waiting after the camera went away")
		}
	}
	live.Close()
	// … and a new one is told, instead of waiting for pictures that never come.
	short, stop := context.WithTimeout(ctx, 8*time.Second)
	defer stop()
	if l, err := Watch(short, src); err == nil {
		l.Close()
		t.Fatal("no error for a camera that is gone")
	}
}
