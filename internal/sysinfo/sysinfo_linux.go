package sysinfo

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Read returns the current load. paths are the mount points whose space is
// shown (e.g. "/" and the data directory); duplicates of the same file
// system are left out.
func Read(paths []string) (Info, error) {
	var info Info
	if _, err := os.Stat("/proc/stat"); err != nil {
		return info, ErrUnsupported
	}
	info.Cores = runtime.NumCPU()
	now, prev := rates(sample)
	if dt := now.cpuTotal - prev.cpuTotal; now.cpuTotal > prev.cpuTotal {
		info.CPU = 100 * float64(dt-(now.cpuIdle-prev.cpuIdle)) / float64(dt)
	}
	d := now.at.Sub(prev.at)
	info.NetRx, info.NetTx = perSecond(prev.rx, now.rx, d), perSecond(prev.tx, now.tx, d)
	info.DiskRead, info.DiskWrite = perSecond(prev.read, now.read, d), perSecond(prev.written, now.written, d)
	info.Rates = d > 0

	if f := fields("/proc/loadavg"); len(f) >= 3 {
		for i := range 3 {
			info.Load[i], _ = strconv.ParseFloat(f[i], 64)
		}
	}
	mem := meminfo()
	info.MemTotal = mem["MemTotal"]
	if avail, ok := mem["MemAvailable"]; ok && avail <= info.MemTotal {
		info.MemUsed = info.MemTotal - avail
	}
	info.SwapTotal = mem["SwapTotal"]
	if mem["SwapFree"] <= info.SwapTotal {
		info.SwapUsed = info.SwapTotal - mem["SwapFree"]
	}
	if f := fields("/proc/uptime"); len(f) > 0 {
		if s, err := strconv.ParseFloat(f[0], 64); err == nil {
			info.Uptime = time.Duration(s) * time.Second
		}
	}
	seen := map[uint64]bool{}
	for _, p := range paths {
		var st syscall.Statfs_t
		if syscall.Statfs(p, &st) != nil || st.Blocks == 0 {
			continue
		}
		id := uint64(st.Fsid.X__val[0])<<32 | uint64(uint32(st.Fsid.X__val[1]))
		if seen[id] && id != 0 {
			continue
		}
		seen[id] = true
		total := st.Blocks * uint64(st.Bsize)
		free := st.Bavail * uint64(st.Bsize)
		info.Disks = append(info.Disks, Disk{Path: p, Total: total, Used: total - free})
	}
	info.Temp = temperature()
	_, err := os.Stat("/.dockerenv")
	info.Containers = err == nil || os.Getenv("container") != ""
	return info, nil
}

func fields(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	return strings.Fields(string(b))
}

func meminfo() map[string]uint64 {
	out := map[string]uint64{}
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, _ := strconv.ParseUint(f[0], 10, 64)
		out[k] = n * 1024 // kB
	}
	return out
}

// wholeDisk matches disks, not their partitions: sda, vda, xvda, nvme0n1, mmcblk0.
var wholeDisk = regexp.MustCompile(`^((s|v|xv|h)d[a-z]+|nvme\d+n\d+|mmcblk\d+)$`)

func sample() counters {
	c := counters{at: time.Now()}
	if f := fields("/proc/stat"); len(f) > 8 && f[0] == "cpu" {
		for i, v := range f[1:9] {
			n, _ := strconv.ParseUint(v, 10, 64)
			c.cpuTotal += n
			if i == 3 || i == 4 { // idle, iowait
				c.cpuIdle += n
			}
		}
	}
	if b, err := os.ReadFile("/proc/net/dev"); err == nil {
		for _, line := range strings.Split(string(b), "\n")[2:] {
			name, rest, ok := strings.Cut(line, ":")
			name = strings.TrimSpace(name)
			if !ok || name == "lo" || strings.HasPrefix(name, "veth") || strings.HasPrefix(name, "docker") || strings.HasPrefix(name, "br-") {
				continue
			}
			f := strings.Fields(rest)
			if len(f) < 9 {
				continue
			}
			rx, _ := strconv.ParseUint(f[0], 10, 64)
			tx, _ := strconv.ParseUint(f[8], 10, 64)
			c.rx += rx
			c.tx += tx
		}
	}
	if b, err := os.ReadFile("/proc/diskstats"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) < 10 || !wholeDisk.MatchString(f[2]) {
				continue
			}
			r, _ := strconv.ParseUint(f[5], 10, 64)
			w, _ := strconv.ParseUint(f[9], 10, 64)
			c.read += r * 512 // sectors
			c.written += w * 512
		}
	}
	return c
}

// temperature is the hottest CPU-ish sensor, from thermal zones or hwmon.
func temperature() float64 {
	best := 0.0
	read := func(path string) {
		b, err := os.ReadFile(path)
		if err != nil {
			return
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(string(b)), 64)
		if err != nil {
			return
		}
		if v > 1000 {
			v /= 1000 // millidegrees
		}
		if v > best && v < 150 {
			best = v
		}
	}
	zones, _ := filepath.Glob("/sys/class/thermal/thermal_zone*/temp")
	for _, z := range zones {
		read(z)
	}
	if best == 0 {
		sensors, _ := filepath.Glob("/sys/class/hwmon/hwmon*/temp*_input")
		for _, s := range sensors {
			read(s)
		}
	}
	return best
}
