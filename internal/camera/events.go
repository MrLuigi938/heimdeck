package camera

import (
	"context"
	"sort"
	"sync"
	"time"
)

// Events of a camera tile (motion, person, doorbell …), kept in memory: the
// newest few with a picture of the moment.

const keepEvents = 20

type Event struct {
	ID    int64
	At    time.Time
	Kind  string // German text key, e.g. "Bewegung", "Person"
	Image []byte // a picture of the moment: fMP4 (key frame) or JPEG
	Type  string // "video/mp4" or "image/jpeg"; empty without a picture
	Codec string // for video/mp4
}

var (
	eventsMu sync.Mutex
	events   = map[int64][]Event{}
	eventSeq int64
)

// AddEvent keeps an event for a tile and returns it with its ID.
func AddEvent(tile int64, e Event) Event {
	eventsMu.Lock()
	defer eventsMu.Unlock()
	eventSeq++
	e.ID = eventSeq
	list := append(events[tile], e)
	if len(list) > keepEvents {
		list = list[len(list)-keepEvents:]
	}
	events[tile] = list
	return e
}

// Events returns a tile's events, newest first.
func Events(tile int64) []Event {
	eventsMu.Lock()
	defer eventsMu.Unlock()
	list := append([]Event(nil), events[tile]...)
	sort.Slice(list, func(i, j int) bool { return list[i].At.After(list[j].At) })
	return list
}

// EventImage returns the picture of one event.
func EventImage(tile, id int64) (Event, bool) {
	eventsMu.Lock()
	defer eventsMu.Unlock()
	for _, e := range events[tile] {
		if e.ID == id && e.Image != nil {
			return e, true
		}
	}
	return Event{}, false
}

// ForgetEvents drops a tile's events (tile deleted).
func ForgetEvents(tile int64) {
	eventsMu.Lock()
	defer eventsMu.Unlock()
	delete(events, tile)
}

// Sensitivity of the motion detection from the stream.
var sensitivity = map[string]float64{"niedrig": 3.2, "mittel": 2.3, "hoch": 1.7}

// detector notices motion from the size of the pictures: a camera's encoder
// sends only what changed since the last picture, so these get much larger
// when something moves. Key frames are always large and are skipped.
type detector struct {
	factor     float64
	base, fast float64
	n          int
	active     bool
	quietSince time.Time
}

const (
	warmup       = 50               // pictures before the first decision
	minExtra     = 1200.0           // bytes above normal before it counts
	motionGap    = 30 * time.Second // quiet time before a new event
	baseFollow   = 0.004            // how fast "normal" follows the picture size
	fastFollow   = 0.35             // smoothing of the current size
	motionCooled = 3 * time.Second  // no motion for this long ends one
	eventPicWait = 6 * time.Second  // wait for a key frame after motion started
)

func newDetector(sens string) *detector {
	f, ok := sensitivity[sens]
	if !ok {
		f = sensitivity["mittel"]
	}
	return &detector{factor: f}
}

// add takes one picture and reports whether a new motion event starts.
func (d *detector) add(fr Frame) bool {
	if fr.Key {
		return false
	}
	size := float64(fr.Size)
	d.n++
	if d.n == 1 {
		d.base, d.fast = size, size
		return false
	}
	d.fast += fastFollow * (size - d.fast)
	moving := d.n > warmup && d.fast > d.base*d.factor && d.fast-d.base > minExtra
	if !moving {
		// Only quiet pictures teach what "normal" is, so a long movement
		// does not become the new normal.
		d.base += baseFollow * (size - d.base)
		if d.n <= warmup {
			d.base += 0.1 * (size - d.base)
		}
	}
	switch {
	case moving && !d.active:
		quiet := d.quietSince.IsZero() || fr.At.Sub(d.quietSince) >= motionGap
		d.active = true
		return quiet
	case !moving && d.active:
		if d.quietSince.IsZero() || d.quietSince.Before(fr.At.Add(-motionCooled)) {
			d.quietSince = fr.At
		}
		if fr.At.Sub(d.quietSince) >= motionCooled {
			d.active = false
		}
	case moving:
		d.quietSince = time.Time{}
	}
	return false
}

// WatchMotion keeps the stream open and calls on for every new movement,
// with a picture of the moment, until ctx ends.
func WatchMotion(ctx context.Context, src Source, sens string, on func(Event)) {
	frames, stop := Frames(src)
	defer stop()
	d := newDetector(sens)
	for {
		select {
		case <-ctx.Done():
			return
		case fr := <-frames:
			if !d.add(fr) {
				continue
			}
			go func(at time.Time) {
				e := Event{At: at, Kind: "Bewegung"}
				e.Image, e.Codec = keyFrameAfter(ctx, src, at)
				if e.Image != nil {
					e.Type = "video/mp4"
				}
				on(e)
			}(fr.At)
		}
	}
}

// keyFrameAfter waits for the camera's next key frame after t (cameras send
// one every 1–4 seconds) and returns it as a still.
func keyFrameAfter(ctx context.Context, src Source, t time.Time) ([]byte, string) {
	f := getFeed(src)
	deadline := time.Now().Add(eventPicWait)
	for time.Now().Before(deadline) {
		f.mu.Lock()
		still, codec, at := f.still, f.codec, f.stillAt
		f.mu.Unlock()
		if still != nil && at.After(t) {
			return still, codec
		}
		select {
		case <-ctx.Done():
			return nil, ""
		case <-time.After(200 * time.Millisecond):
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.still, f.codec // an older picture is better than none
}
