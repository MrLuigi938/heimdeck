// Package testcam is a fake RTSP camera for tests: it draws a yard with a
// ticking clock and a person walking through now and then, and sends it as
// real H.264 – encoded the simplest valid way (uncompressed I_PCM macroblocks,
// unchanged ones skipped), so still scenes give tiny pictures and movement
// large ones, like with a real camera.
package testcam

import (
	"sync"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
)

const (
	W, H = 640, 352
	mbW  = W / 16
	mbH  = H / 16
	FPS  = 15
	GOP  = 2 * FPS
)

// Picture is a YUV 4:2:0 frame.
type Picture struct{ Y, U, V []byte }

func newPicture() *Picture {
	return &Picture{Y: make([]byte, W*H), U: make([]byte, W*H/4), V: make([]byte, W*H/4)}
}

// Walks are the times (seconds in each loop of Loop) when someone walks by.
var Walks = [][2]float64{{8, 12}, {22, 25}}

const Loop = 30.0

// Scene draws the picture at time t.
func Scene(t float64) *Picture {
	p := newPicture()
	rect := func(x0, y0, x1, y1 int, y, u, v byte) {
		for yy := max(y0, 0); yy < min(y1, H); yy++ {
			for xx := max(x0, 0); xx < min(x1, W); xx++ {
				p.Y[yy*W+xx] = y
				if yy%2 == 0 && xx%2 == 0 {
					p.U[(yy/2)*(W/2)+xx/2] = u
					p.V[(yy/2)*(W/2)+xx/2] = v
				}
			}
		}
	}
	rect(0, 0, W, 150, 140, 150, 112)     // sky
	rect(0, 150, W, H, 105, 105, 110)     // grass
	rect(60, 70, 240, 190, 140, 120, 135) // house
	rect(100, 40, 200, 70, 70, 110, 160)  // roof
	rect(130, 130, 164, 190, 60, 120, 135)
	rect(80, 90, 116, 118, 210, 130, 125)
	rect(185, 90, 221, 118, 210, 130, 125)
	for _, x := range []int{420, 520, 590} {
		rect(x-6, 120, x+6, 180, 55, 120, 135)
		rect(x-34, 80, x+34, 130, 85, 110, 105)
	}
	rect(0, 250, W, 280, 165, 120, 132) // path
	lt := t - float64(int(t/Loop))*Loop
	for _, w := range Walks {
		if lt >= w[0] && lt < w[1] {
			pos := (lt - w[0]) / (w[1] - w[0])
			x := int(-40 + pos*(W+80))
			rect(x-14, 200, x+14, 250, 40, 140, 120)  // body
			rect(x-11, 176, x+11, 198, 180, 115, 145) // head
			step := int(lt*8) % 2 * 8
			rect(x-12+step, 250, x-3+step, 272, 30, 128, 128)
			rect(x+3-step, 250, x+12-step, 272, 30, 128, 128)
		}
	}
	// The seconds of the clock as a small changing block, like an overlay.
	s := int(t) % 10
	rect(16, 16, 48, 32, byte(40+s*20), 128, 128)
	return p
}

// Encoder writes pictures as H.264 Baseline (I_PCM / P_Skip).
type Encoder struct {
	n    int
	prev *Picture
	idr  int
}

// SPS and PPS of the stream.
func SPS() []byte {
	var b bits
	b.u(8, 66) // profile_idc: Baseline
	b.u(8, 0xc0)
	b.u(8, 30) // level 3.0
	b.ue(0)    // seq_parameter_set_id
	b.ue(0)    // log2_max_frame_num_minus4
	b.ue(2)    // pic_order_cnt_type
	b.ue(1)    // max_num_ref_frames
	b.u(1, 0)
	b.ue(mbW - 1)
	b.ue(mbH - 1)
	b.u(1, 1) // frame_mbs_only_flag
	b.u(1, 1) // direct_8x8_inference_flag
	b.u(1, 0) // frame_cropping_flag
	b.u(1, 0) // vui_parameters_present_flag
	return nal(0x67, b.trailing())
}

func PPS() []byte {
	var b bits
	b.ue(0)
	b.ue(0)
	b.u(1, 0) // CAVLC
	b.u(1, 0)
	b.ue(0) // slice groups
	b.ue(0)
	b.ue(0)
	b.u(1, 0)
	b.u(2, 0)
	b.se(0)
	b.se(0)
	b.se(0)
	b.u(1, 1) // deblocking_filter_control_present_flag
	b.u(1, 0)
	b.u(1, 0)
	return nal(0x68, b.trailing())
}

// Encode returns the access unit (NAL units) of the next picture.
func (e *Encoder) Encode(p *Picture) (au [][]byte, key bool) {
	key = e.n%GOP == 0 || e.prev == nil
	frameNum := e.n % GOP % 16
	var b bits
	b.ue(0) // first_mb_in_slice
	if key {
		b.ue(7) // I slice
	} else {
		b.ue(5) // P slice
	}
	b.ue(0)                  // pic_parameter_set_id
	b.u(4, uint64(frameNum)) // frame_num
	if key {
		b.ue(uint64(e.idr % 65536)) // idr_pic_id
		e.idr++
	} else {
		b.u(1, 0) // num_ref_idx_active_override_flag
		b.u(1, 0) // ref_pic_list_modification_flag_l0
	}
	if key {
		b.u(1, 0) // no_output_of_prior_pics_flag
		b.u(1, 0) // long_term_reference_flag
	} else {
		b.u(1, 0) // adaptive_ref_pic_marking_mode_flag
	}
	b.se(0) // slice_qp_delta
	b.ue(1) // disable_deblocking_filter_idc: off
	skip := 0
	for my := 0; my < mbH; my++ {
		for mx := 0; mx < mbW; mx++ {
			if !key && !changed(e.prev, p, mx, my) {
				skip++
				continue
			}
			if key {
				b.ue(25) // I_PCM
			} else {
				b.ue(uint64(skip))
				skip = 0
				b.ue(30) // I_PCM in a P slice
			}
			b.align()
			for y := 0; y < 16; y++ {
				for x := 0; x < 16; x++ {
					b.byte(pcm(p.Y[(my*16+y)*W+mx*16+x]))
				}
			}
			for _, plane := range [][]byte{p.U, p.V} {
				for y := 0; y < 8; y++ {
					for x := 0; x < 8; x++ {
						b.byte(pcm(plane[(my*8+y)*(W/2)+mx*8+x]))
					}
				}
			}
		}
	}
	if skip > 0 {
		b.ue(uint64(skip))
	}
	header := byte(0x21) // non-IDR, nal_ref_idc 1
	if key {
		header = 0x65
	}
	slice := nal(header, b.trailing())
	e.prev = p
	e.n++
	if key {
		return [][]byte{SPS(), PPS(), slice}, true
	}
	return [][]byte{slice}, false
}

func pcm(v byte) byte { return max(v, 1) }

func changed(a, b *Picture, mx, my int) bool {
	for y := 0; y < 16; y++ {
		o := (my*16+y)*W + mx*16
		if string(a.Y[o:o+16]) != string(b.Y[o:o+16]) {
			return true
		}
	}
	for y := 0; y < 8; y++ {
		o := (my*8+y)*(W/2) + mx*8
		if string(a.U[o:o+8]) != string(b.U[o:o+8]) || string(a.V[o:o+8]) != string(b.V[o:o+8]) {
			return true
		}
	}
	return false
}

// nal adds the header and emulation prevention bytes.
func nal(header byte, rbsp []byte) []byte {
	out := []byte{header}
	zeros := 0
	for _, c := range rbsp {
		if zeros >= 2 && c <= 3 {
			out = append(out, 3)
			zeros = 0
		}
		out = append(out, c)
		if c == 0 {
			zeros++
		} else {
			zeros = 0
		}
	}
	return out
}

type bits struct {
	buf []byte
	n   int // bits used in the last byte
}

func (b *bits) bit(v uint64) {
	if b.n == 0 {
		b.buf = append(b.buf, 0)
	}
	if v != 0 {
		b.buf[len(b.buf)-1] |= 1 << (7 - b.n)
	}
	b.n = (b.n + 1) % 8
}

func (b *bits) u(n int, v uint64) {
	for i := n - 1; i >= 0; i-- {
		b.bit(v >> i & 1)
	}
}

func (b *bits) ue(v uint64) {
	v++
	n := 0
	for x := v; x > 1; x >>= 1 {
		n++
	}
	b.u(n, 0)
	b.u(n+1, v)
}

func (b *bits) se(v int64) {
	if v <= 0 {
		b.ue(uint64(-2 * v))
	} else {
		b.ue(uint64(2*v - 1))
	}
}

func (b *bits) align() {
	for b.n != 0 {
		b.bit(0)
	}
}

func (b *bits) byte(v byte) { b.buf = append(b.buf, v) } // only when aligned

func (b *bits) trailing() []byte {
	b.bit(1)
	b.align()
	return b.buf
}

// Server is a running fake camera.
type Server struct {
	URL    string
	server *gortsplib.Server
	stream *gortsplib.ServerStream
	stop   chan struct{}
	mu     sync.Mutex
	// Start moves the camera's clock: the scene at time Start+elapsed.
	Start time.Time
}

type handler struct{ s *Server }

func (h handler) OnDescribe(*gortsplib.ServerHandlerOnDescribeCtx) (*base.Response, *gortsplib.ServerStream, error) {
	return &base.Response{StatusCode: base.StatusOK}, h.s.stream, nil
}

func (h handler) OnSetup(*gortsplib.ServerHandlerOnSetupCtx) (*base.Response, *gortsplib.ServerStream, error) {
	return &base.Response{StatusCode: base.StatusOK}, h.s.stream, nil
}

func (h handler) OnPlay(*gortsplib.ServerHandlerOnPlayCtx) (*base.Response, error) {
	return &base.Response{StatusCode: base.StatusOK}, nil
}

// Start runs a fake camera on addr (e.g. "127.0.0.1:8554"); offset shifts
// the scene's clock (to start right before a walk, for example).
func Start(addr string, offset time.Duration) (*Server, error) {
	s := &Server{URL: "rtsp://" + addr + "/live", stop: make(chan struct{})}
	s.server = &gortsplib.Server{Handler: handler{s}, RTSPAddress: addr}
	if err := s.server.Start(); err != nil {
		return nil, err
	}
	forma := &format.H264{PayloadTyp: 96, PacketizationMode: 1, SPS: SPS(), PPS: PPS()}
	desc := &description.Session{Medias: []*description.Media{{Type: description.MediaTypeVideo, Formats: []format.Format{forma}}}}
	s.stream = &gortsplib.ServerStream{Server: s.server, Desc: desc}
	if err := s.stream.Initialize(); err != nil {
		s.server.Close()
		return nil, err
	}
	enc, err := forma.CreateEncoder()
	if err != nil {
		return nil, err
	}
	go func() {
		var e Encoder
		tick := time.NewTicker(time.Second / FPS)
		defer tick.Stop()
		start := time.Now()
		for i := 0; ; i++ {
			select {
			case <-s.stop:
				return
			case <-tick.C:
			}
			t := float64(i)/FPS + offset.Seconds()
			au, _ := e.Encode(Scene(t))
			pkts, err := enc.Encode(au)
			if err != nil {
				continue
			}
			ts := uint32(time.Since(start).Seconds() * 90000)
			for _, pk := range pkts {
				pk.Timestamp = ts
				s.stream.WritePacketRTP(desc.Medias[0], pk)
			}
		}
	}()
	return s, nil
}

func (s *Server) Close() {
	close(s.stop)
	s.stream.Close()
	s.server.Close()
}
