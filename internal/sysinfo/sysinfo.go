// Package sysinfo reads the load of the machine Heimdeck runs on: CPU, memory,
// disks, network and disk throughput, temperature. On Linux it reads /proc
// and /sys; elsewhere Read reports that it is not supported.
//
// In a Docker container most values are the host's (CPU, memory, disk
// throughput, temperature); the network is the container's own unless it
// runs with network_mode: host. In a Proxmox LXC container they are the
// container's (its CPU and memory limits).
package sysinfo

import (
	"errors"
	"sync"
	"time"
)

var ErrUnsupported = errors.New("nur unter Linux verfügbar")

type Disk struct {
	Path        string
	Total, Used uint64
}

type Info struct {
	CPU        float64 // percent, all cores
	Cores      int
	Load       [3]float64
	MemTotal   uint64
	MemUsed    uint64
	SwapTotal  uint64
	SwapUsed   uint64
	Disks      []Disk
	NetRx      float64 // bytes per second, all interfaces but loopback
	NetTx      float64
	DiskRead   float64 // bytes per second, all disks
	DiskWrite  float64
	Temp       float64 // highest temperature in °C, 0 = unknown
	Uptime     time.Duration
	Rates      bool // NetRx … DiskWrite are measured (needs two samples)
	Containers bool // running in a container (values may be the host's)
}

// counters are the totals rates are computed from.
type counters struct {
	at                    time.Time
	cpuIdle, cpuTotal     uint64
	rx, tx, read, written uint64
}

var (
	mu   sync.Mutex
	last counters
)

// rates turns two samples into per-second values. The first call measures
// over a quarter of a second, later ones over the time since the last call.
func rates(sample func() counters) (now counters, prev counters) {
	mu.Lock()
	defer mu.Unlock()
	prev = last
	if prev.at.IsZero() || time.Since(prev.at) > 10*time.Minute {
		prev = sample()
		time.Sleep(250 * time.Millisecond)
	}
	now = sample()
	if now.at.Sub(prev.at) < 200*time.Millisecond {
		// called again right away: keep the older sample for a meaningful rate
		return now, prev
	}
	last = now
	return now, prev
}

func perSecond(a, b uint64, d time.Duration) float64 {
	if b < a || d <= 0 {
		return 0
	}
	return float64(b-a) / d.Seconds()
}
