package camera

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/heimdeck/heimdeck/internal/camera/testcam"
)

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().String()
}

func TestLiveStillAndMotion(t *testing.T) {
	// Start the scene 3 s before someone walks by.
	cam, err := testcam.Start(freeAddr(t), time.Duration((testcam.Walks[0][0]-3)*float64(time.Second)))
	if err != nil {
		t.Fatal(err)
	}
	defer cam.Close()
	src := Source{URL: "rtsp://user:secret@" + cam.URL[len("rtsp://"):], Dial: (&net.Dialer{}).DialContext}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	live, err := Watch(ctx, src)
	if err != nil {
		t.Fatal(err)
	}
	if live.Codec != "avc1.42c01e" {
		t.Errorf("codec %q", live.Codec)
	}
	if !bytes.Contains(live.Init, []byte("ftyp")) || !bytes.Contains(live.Init, []byte("avcC")) {
		t.Fatal("init segment without ftyp/avcC")
	}
	for i := 0; i < 5; i++ {
		select {
		case f := <-live.Frames:
			if !bytes.Contains(f, []byte("moof")) || !bytes.Contains(f, []byte("mdat")) {
				t.Fatal("fragment without moof/mdat")
			}
		case <-ctx.Done():
			t.Fatal("no frames")
		}
	}
	live.Close()

	still, codec, _, err := Still(ctx, src, time.Minute)
	if err != nil || codec == "" || !bytes.Contains(still, []byte("moof")) {
		t.Fatalf("still: %v %q", err, codec)
	}

	got := make(chan Event, 4)
	mctx, mcancel := context.WithCancel(ctx)
	defer mcancel()
	go WatchMotion(mctx, src, "mittel", func(e Event) { got <- e })
	select {
	case e := <-got:
		if e.Kind != "Bewegung" || e.Type != "video/mp4" || len(e.Image) == 0 {
			t.Fatalf("event %+v", e.Kind)
		}
	case <-ctx.Done():
		t.Fatal("no motion event while someone walked by")
	}
}

func TestDetectorQuietScene(t *testing.T) {
	d := newDetector("mittel")
	now := time.Now()
	// 60 s of a quiet picture with a clock ticking every second: no event.
	for i := 0; i < 900; i++ {
		size := 120
		if i%15 == 0 {
			size = 900
		}
		if d.add(Frame{Size: size, At: now.Add(time.Duration(i) * time.Second / 15)}) {
			t.Fatalf("false motion at frame %d", i)
		}
	}
	// Then movement: one event, not one per picture.
	events := 0
	for i := 900; i < 960; i++ {
		if d.add(Frame{Size: 9000, At: now.Add(time.Duration(i) * time.Second / 15)}) {
			events++
		}
	}
	if events != 1 {
		t.Fatalf("%d events for one movement", events)
	}
}

func TestRedactURL(t *testing.T) {
	got := RedactURL(`dial rtsp://admin:pa55@10.0.0.5:554/live failed, also rtsp://x:y@cam/a`)
	if bytes.Contains([]byte(got), []byte("pa55")) || bytes.Contains([]byte(got), []byte(":y@")) {
		t.Fatal(got)
	}
}

func TestHEVCCodecString(t *testing.T) {
	// SPS of a Main profile, level 4.1 stream.
	sps := []byte{0x42, 0x01, 0x01, 0x01, 0x60, 0x00, 0x00, 0x03, 0x00, 0xb0, 0x00, 0x00, 0x03, 0x00, 0x00, 0x03, 0x00, 0x7b, 0xa0}
	if got := hevcCodecString(sps); got != "hvc1.1.6.L123.B0" {
		t.Fatal(got)
	}
}
