// Package camera reads the video of IP cameras over RTSP and hands it to the
// browser as fragmented MP4 (Media Source Extensions) – without decoding or
// re-encoding, so it needs no ffmpeg and hardly any CPU. One connection per
// camera is shared by everyone watching; it closes a little while after the
// last viewer left, unless a motion watcher keeps it open.
package camera

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h264"
	"github.com/bluenviron/mediacommon/v2/pkg/codecs/h265"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4/seekablebuffer"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"
	"github.com/pion/rtp"
)

// Source is where a camera's stream comes from.
type Source struct {
	URL  string // rtsp://user:pass@host:554/path
	Dial func(ctx context.Context, network, address string) (net.Conn, error)
}

// Frame is one picture as it went to the viewers, for the motion detection.
type Frame struct {
	Size int
	Key  bool
	At   time.Time
}

const (
	idleClose    = 20 * time.Second // keep the camera open this long after the last viewer
	stillMaxAge  = 2 * time.Minute  // a kept key frame is good as a still for so long
	connectWait  = 15 * time.Second // how long a viewer waits for the first picture
	subscriberQ  = 256              // frames buffered per viewer before it is dropped
	maxFrameSize = 8 << 20
)

// ErrNoVideo is returned when the stream has no H.264/H.265 video.
var ErrNoVideo = shown("Der Stream enthält kein H.264- oder H.265-Video")

type feed struct {
	key string
	src Source

	mu       sync.Mutex
	running  bool
	viewers  map[chan []byte]bool // true once the viewer got a key frame
	keepers  int
	watchers map[chan Frame]struct{}
	idle     *time.Timer
	cancel   context.CancelFunc

	codec    string // e.g. avc1.64001f, for MediaSource.isTypeSupported
	init     []byte // fMP4 init segment
	still    []byte // init + the last key frame as one fragment
	stillAt  time.Time
	lastErr  error
	ready    chan struct{} // closed once init is there or the attempt failed
	readyErr error
}

var (
	feedsMu sync.Mutex
	feeds   = map[string]*feed{}
)

func getFeed(src Source) *feed {
	feedsMu.Lock()
	defer feedsMu.Unlock()
	f := feeds[src.URL]
	if f == nil {
		f = &feed{key: src.URL, src: src, viewers: map[chan []byte]bool{}, watchers: map[chan Frame]struct{}{}}
		feeds[src.URL] = f
	} else {
		f.mu.Lock()
		f.src.Dial = src.Dial
		f.mu.Unlock()
	}
	return f
}

// startLocked starts the connection if it is not running. f.mu is held.
func (f *feed) startLocked() {
	if f.idle != nil {
		f.idle.Stop()
		f.idle = nil
	}
	if f.running {
		return
	}
	f.running = true
	f.ready = make(chan struct{})
	f.readyErr = nil
	ctx, cancel := context.WithCancel(context.Background())
	f.cancel = cancel
	go f.run(ctx)
}

// releaseLocked closes the connection after a while when nobody needs it.
func (f *feed) releaseLocked() {
	if len(f.viewers) > 0 || f.keepers > 0 || !f.running || f.idle != nil {
		return
	}
	f.idle = time.AfterFunc(idleClose, func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.idle = nil
		if len(f.viewers) == 0 && f.keepers == 0 && f.running {
			f.cancel()
			f.running = false
		}
	})
}

// run keeps the connection up (reconnecting with a pause) until cancelled.
func (f *feed) run(ctx context.Context) {
	wait := 2 * time.Second
	for {
		err := f.session(ctx)
		if ctx.Err() != nil {
			return
		}
		f.mu.Lock()
		f.lastErr = err
		f.failReadyLocked(err)
		f.dropViewersLocked()
		f.mu.Unlock()
		slog.Debug("Kamera getrennt", "fehler", redact(err))
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		if wait < time.Minute {
			wait *= 2
		}
	}
}

func (f *feed) failReadyLocked(err error) {
	select {
	case <-f.ready:
	default:
		f.readyErr = err
		close(f.ready)
	}
}

func (f *feed) dropViewersLocked() {
	for ch := range f.viewers {
		close(ch)
		delete(f.viewers, ch)
	}
}

// session is one RTSP connection: it returns when the camera goes away.
func (f *feed) session(ctx context.Context) error {
	u, err := base.ParseURL(f.src.URL)
	if err != nil {
		return shown("Ungültige RTSP-Adresse")
	}
	tcp := gortsplib.ProtocolTCP // works through Docker and firewalls
	c := &gortsplib.Client{Scheme: u.Scheme, Host: u.Host, Protocol: &tcp, DialContext: f.src.Dial,
		ReadTimeout: 10 * time.Second, WriteTimeout: 10 * time.Second, UserAgent: "Heimdeck"}
	if err := c.Start(); err != nil {
		return err
	}
	defer c.Close()
	go func() {
		<-ctx.Done()
		c.Close()
	}()

	desc, _, err := c.Describe(u)
	if err != nil {
		return friendly(err)
	}
	var h264f *format.H264
	var h265f *format.H265
	medi := desc.FindFormat(&h264f)
	if medi == nil {
		medi = desc.FindFormat(&h265f)
	}
	if medi == nil {
		return ErrNoVideo
	}
	if _, err := c.Setup(desc.BaseURL, medi, 0, 0); err != nil {
		return friendly(err)
	}

	m := &muxer{feed: f}
	var decode func(*rtp.Packet) ([][]byte, error)
	if h264f != nil {
		dec, err := h264f.CreateDecoder()
		if err != nil {
			return err
		}
		decode = dec.Decode
		sps, pps := h264f.SafeParams()
		m.setParams(nil, sps, pps)
		c.OnPacketRTP(medi, h264f, func(pkt *rtp.Packet) { m.packet(c, medi, pkt, decode) })
	} else {
		m.h265 = true
		dec, err := h265f.CreateDecoder()
		if err != nil {
			return err
		}
		decode = dec.Decode
		vps, sps, pps := h265f.SafeParams()
		m.setParams(vps, sps, pps)
		c.OnPacketRTP(medi, h265f, func(pkt *rtp.Packet) { m.packet(c, medi, pkt, decode) })
	}
	if _, err := c.Play(nil); err != nil {
		return friendly(err)
	}
	return friendly(c.Wait())
}

// muxer turns access units into fMP4 fragments, one per picture. A picture
// is sent when the next one arrives, so its duration is known.
type muxer struct {
	feed          *feed
	h265          bool
	vps, sps, pps []byte
	initDone      bool
	started       bool // got the first key frame
	base          int64
	seq           uint32
	prev          *pending
	lastPTS       int64
}

type pending struct {
	pts int64
	au  [][]byte
	key bool
	at  time.Time
}

func (m *muxer) setParams(vps, sps, pps []byte) {
	if vps != nil {
		m.vps = vps
	}
	if sps != nil {
		m.sps = sps
	}
	if pps != nil {
		m.pps = pps
	}
}

func (m *muxer) packet(c *gortsplib.Client, medi *description.Media, pkt *rtp.Packet, decode func(*rtp.Packet) ([][]byte, error)) {
	// The time decoder has to see every packet, not only the last of a picture.
	pts, ok := c.PacketPTS(medi, pkt)
	au, err := decode(pkt)
	if err != nil || !ok {
		return // more packets needed, or a broken one
	}
	m.unit(pts, au)
}

// unit handles one decoded access unit with its presentation time (90 kHz).
func (m *muxer) unit(pts int64, au [][]byte) {
	au, changed := m.takeParams(au)
	if len(au) == 0 {
		return
	}
	key := m.isKey(au)
	if !m.started {
		if !key || m.sps == nil || m.pps == nil || (m.h265 && m.vps == nil) {
			return
		}
		m.started = true
		m.base = pts
	}
	if changed && m.initDone {
		// New resolution or codec settings: viewers start over.
		m.flush(nil)
		m.initDone = false
		m.feed.mu.Lock()
		m.feed.dropViewersLocked()
		m.feed.mu.Unlock()
	}
	if !m.initDone {
		if !key {
			return
		}
		if err := m.writeInit(); err != nil {
			return
		}
		m.initDone = true
		m.base = pts
		m.prev = nil
	}
	if pts <= m.lastPTS && m.prev != nil {
		pts = m.lastPTS + 1 // keep the times rising (cameras without B-frames)
	}
	m.lastPTS = pts
	m.flush(&pending{pts: pts, au: au, key: key, at: time.Now()})
}

// takeParams keeps parameter sets sent in-band and reports whether they changed.
func (m *muxer) takeParams(au [][]byte) ([][]byte, bool) {
	changed := false
	set := func(dst *[]byte, n []byte) {
		if string(*dst) != string(n) {
			if *dst != nil {
				changed = true
			}
			*dst = append([]byte(nil), n...)
		}
	}
	for _, n := range au {
		if len(n) == 0 {
			continue
		}
		if m.h265 {
			if len(n) < 2 {
				continue
			}
			switch h265.NALUType((n[0] >> 1) & 0b111111) {
			case h265.NALUType_VPS_NUT:
				set(&m.vps, n)
			case h265.NALUType_SPS_NUT:
				set(&m.sps, n)
			case h265.NALUType_PPS_NUT:
				set(&m.pps, n)
			}
			continue
		}
		switch h264.NALUType(n[0] & 0x1f) {
		case h264.NALUTypeSPS:
			set(&m.sps, n)
		case h264.NALUTypePPS:
			set(&m.pps, n)
		}
	}
	return au, changed
}

func (m *muxer) isKey(au [][]byte) bool {
	if m.h265 {
		return h265.IsRandomAccess(au)
	}
	return h264.IsRandomAccess(au)
}

func (m *muxer) writeInit() error {
	var codec codecs.Codec
	var name string
	if m.h265 {
		codec = &codecs.H265{VPS: m.vps, SPS: m.sps, PPS: m.pps}
		name = hevcCodecString(m.sps)
	} else {
		codec = &codecs.H264{SPS: m.sps, PPS: m.pps}
		name = avcCodecString(m.sps)
	}
	init := fmp4.Init{Tracks: []*fmp4.InitTrack{{ID: 1, TimeScale: 90000, Codec: codec}}}
	var buf seekablebuffer.Buffer
	if err := init.Marshal(&buf); err != nil {
		return err
	}
	f := m.feed
	f.mu.Lock()
	f.init = buf.Bytes()
	f.codec = name
	f.lastErr = nil
	select {
	case <-f.ready:
	default:
		close(f.ready)
	}
	f.mu.Unlock()
	return nil
}

// flush sends the waiting picture (now that its duration is known) and keeps next.
func (m *muxer) flush(next *pending) {
	p := m.prev
	m.prev = next
	if p == nil {
		return
	}
	dur := uint32(3000) // 30 fps when unknown
	if next != nil && next.pts > p.pts {
		dur = uint32(min(next.pts-p.pts, 90000))
	}
	var sample *fmp4.Sample
	var err error
	if m.h265 {
		sample, err = fmp4.NewSampleH265(0, p.au)
	} else {
		sample, err = fmp4.NewSampleH264(0, p.au)
	}
	if err != nil || len(sample.Payload) > maxFrameSize {
		return
	}
	sample.Duration = dur
	m.seq++
	part := fmp4.Part{SequenceNumber: m.seq, Tracks: []*fmp4.PartTrack{{ID: 1, BaseTime: uint64(p.pts - m.base), Samples: []*fmp4.Sample{sample}}}}
	var buf seekablebuffer.Buffer
	if err := part.Marshal(&buf); err != nil {
		return
	}
	data := buf.Bytes()

	f := m.feed
	f.mu.Lock()
	if p.key {
		// The key frame alone, starting at time 0: the still picture.
		still := fmp4.Part{SequenceNumber: 1, Tracks: []*fmp4.PartTrack{{ID: 1, BaseTime: 0, Samples: []*fmp4.Sample{sample}}}}
		var sb seekablebuffer.Buffer
		if still.Marshal(&sb) == nil {
			f.still = append(append([]byte(nil), f.init...), sb.Bytes()...)
			f.stillAt = p.at
		}
	}
	for ch, started := range f.viewers {
		if !started {
			// A new viewer starts with a key frame: the browser cannot show the others before.
			if !p.key {
				continue
			}
			f.viewers[ch] = true
		}
		select {
		case ch <- data:
		default: // too slow – it reconnects
			close(ch)
			delete(f.viewers, ch)
		}
	}
	fr := Frame{Size: len(sample.Payload), Key: p.key, At: p.at}
	for ch := range f.watchers {
		select {
		case ch <- fr:
		default:
		}
	}
	f.mu.Unlock()
}

// Live is a running view of a camera.
type Live struct {
	Codec  string
	Init   []byte
	Frames <-chan []byte // closed when the camera went away
	stop   func()
}

func (l *Live) Close() { l.stop() }

// Watch starts the camera (or joins the running connection) and returns its
// pictures from the next key frame on.
func Watch(ctx context.Context, src Source) (*Live, error) {
	f := getFeed(src)
	f.mu.Lock()
	f.startLocked()
	ready := f.ready
	f.mu.Unlock()
	if err := waitReady(ctx, f, ready); err != nil {
		f.mu.Lock()
		f.releaseLocked()
		f.mu.Unlock()
		return nil, err
	}
	ch := make(chan []byte, subscriberQ)
	f.mu.Lock()
	f.viewers[ch] = false
	l := &Live{Codec: f.codec, Init: f.init, Frames: ch}
	f.mu.Unlock()
	var once sync.Once
	l.stop = func() {
		once.Do(func() {
			f.mu.Lock()
			if _, ok := f.viewers[ch]; ok {
				delete(f.viewers, ch)
				close(ch)
			}
			f.releaseLocked()
			f.mu.Unlock()
		})
	}
	return l, nil
}

func waitReady(ctx context.Context, f *feed, ready chan struct{}) error {
	t := time.NewTimer(connectWait)
	defer t.Stop()
	select {
	case <-ready:
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.init == nil {
			if f.readyErr != nil {
				return f.readyErr
			}
			return shown("Die Kamera schickt kein Bild")
		}
		return nil
	case <-t.C:
		f.mu.Lock()
		defer f.mu.Unlock()
		if f.lastErr != nil {
			return f.lastErr
		}
		return shown("Die Kamera antwortet nicht rechtzeitig")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Still returns a key frame as a small fMP4 (init + one picture), its codec
// and when it was taken. A kept one is used if it is younger than maxAge
// (so a page full of viewers does not wake the camera each time); otherwise
// it opens the camera until a new one arrives.
func Still(ctx context.Context, src Source, maxAge time.Duration) ([]byte, string, time.Time, error) {
	maxAge = min(maxAge, stillMaxAge)
	f := getFeed(src)
	f.mu.Lock()
	if f.still != nil && time.Since(f.stillAt) < maxAge {
		defer f.mu.Unlock()
		return f.still, f.codec, f.stillAt, nil
	}
	f.mu.Unlock()
	l, err := Watch(ctx, src)
	if err != nil {
		return nil, "", time.Time{}, err
	}
	defer l.Close()
	deadline := time.NewTimer(connectWait)
	defer deadline.Stop()
	for {
		f.mu.Lock()
		if f.still != nil && time.Since(f.stillAt) < maxAge {
			defer f.mu.Unlock()
			return f.still, f.codec, f.stillAt, nil
		}
		f.mu.Unlock()
		select {
		case _, ok := <-l.Frames:
			if !ok {
				return nil, "", time.Time{}, shown("Die Kamera hat die Verbindung beendet")
			}
		case <-deadline.C:
			return nil, "", time.Time{}, shown("Die Kamera schickt kein Bild")
		case <-ctx.Done():
			return nil, "", time.Time{}, ctx.Err()
		}
	}
}

// Frames keeps the camera open and reports the size of every picture, for
// the motion detection. Stop it with the returned function.
func Frames(src Source) (<-chan Frame, func()) {
	f := getFeed(src)
	ch := make(chan Frame, 64)
	f.mu.Lock()
	f.keepers++
	f.watchers[ch] = struct{}{}
	f.startLocked()
	f.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			f.mu.Lock()
			f.keepers--
			delete(f.watchers, ch)
			f.releaseLocked()
			f.mu.Unlock()
		})
	}
}

// Probe connects once and reports what the camera sends (for the tile's test).
func Probe(ctx context.Context, src Source) (string, error) {
	l, err := Watch(ctx, src)
	if err != nil {
		return "", err
	}
	l.Close()
	return l.Codec, nil
}

// friendly turns common RTSP errors into texts for the tile.
func friendly(err error) error {
	if err == nil {
		return nil
	}
	s := err.Error()
	slog.Debug("Kamera-Verbindung", "fehler", RedactURL(s))
	switch {
	// Connection errors first: their address may contain "401" or "404" (a port).
	case strings.Contains(s, "connection refused"), strings.Contains(s, "connectex"):
		return shown("Die Kamera nimmt keine RTSP-Verbindung an – ist RTSP eingeschaltet und der Port richtig (meist 554)?")
	case isNetworkError(s):
		return errUnreachable
	case strings.Contains(s, "401"), strings.Contains(s, "authentication"), strings.Contains(s, "Unauthorized"):
		return shown("Die Kamera lehnt die Anmeldung ab – Benutzer und Passwort prüfen")
	case strings.Contains(s, "404"):
		return shown("Diesen Stream kennt die Kamera nicht – den Pfad der RTSP-Adresse prüfen")
	}
	return err
}

// redact removes the password from RTSP addresses in error texts.
func redact(err error) string {
	if err == nil {
		return ""
	}
	return RedactURL(err.Error())
}

// RedactURL hides user and password in rtsp:// addresses.
func RedactURL(s string) string {
	for _, scheme := range []string{"rtsp://", "rtsps://"} {
		for i := strings.Index(s, scheme); i >= 0; {
			rest := s[i+len(scheme):]
			end := strings.IndexAny(rest, "/ \"'")
			if end < 0 {
				end = len(rest)
			}
			if at := strings.LastIndex(rest[:end], "@"); at >= 0 {
				s = s[:i+len(scheme)] + "***@" + rest[at+1:]
			}
			next := strings.Index(s[i+len(scheme):], scheme)
			if next < 0 {
				break
			}
			i += len(scheme) + next
		}
	}
	return s
}

// avcCodecString builds "avc1.PPCCLL" from the SPS.
func avcCodecString(sps []byte) string {
	if len(sps) < 4 {
		return "avc1.42e01f"
	}
	return fmt.Sprintf("avc1.%02x%02x%02x", sps[1], sps[2], sps[3])
}

// hevcCodecString builds "hvc1.<profile>.<compat>.<tier><level>.<constraints>" (ISO/IEC 14496-15 E.3).
func hevcCodecString(sps []byte) string {
	b := h264.EmulationPreventionRemove(sps)
	if len(b) < 15 {
		return "hvc1.1.6.L93.B0"
	}
	ptl := b[3]
	space := []string{"", "A", "B", "C"}[ptl>>6]
	tier := "L"
	if ptl&0x20 != 0 {
		tier = "H"
	}
	profile := ptl & 0x1f
	compat := uint32(b[4])<<24 | uint32(b[5])<<16 | uint32(b[6])<<8 | uint32(b[7])
	var rev uint32
	for i := 0; i < 32; i++ {
		if compat&(1<<i) != 0 {
			rev |= 1 << (31 - i)
		}
	}
	s := fmt.Sprintf("hvc1.%s%d.%X.%s%d", space, profile, rev, tier, b[14])
	cons := b[8:14]
	last := len(cons)
	for last > 0 && cons[last-1] == 0 {
		last--
	}
	if last == 0 {
		return s + ".B0"
	}
	for _, c := range cons[:last] {
		s += fmt.Sprintf(".%X", c)
	}
	return s
}
