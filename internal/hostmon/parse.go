package hostmon

import (
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var errParse = errors.New("hostmon: parse collected data")

var markers = map[string]bool{
	"__CPU1__": true,
	"__CPU2__": true,
	"__MEM__":  true,
	"__DISK__": true,
	"__NET__":  true,
	"__SYS__":  true,
}

// sections splits the command output into marker-delimited buckets. Blank
// lines and any text before the first marker are ignored. Bad or empty input
// simply yields fewer sections; it never panics.
func sections(output string) map[string][]string {
	out := map[string][]string{}
	cur := ""
	for _, raw := range strings.Split(output, "\n") {
		line := strings.TrimRight(raw, "\r")
		if markers[strings.TrimSpace(line)] {
			cur = strings.TrimSpace(line)
			if _, ok := out[cur]; !ok {
				out[cur] = []string{}
			}
			continue
		}
		if cur == "" {
			continue
		}
		out[cur] = append(out[cur], line)
	}
	return out
}

func firstLine(lines []string) string {
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			return t
		}
	}
	return ""
}

// parseCPULine reads the aggregate "cpu" line of /proc/stat. total is the sum
// of the first eight counters (user..steal); idle is idle+iowait. guest
// counters are already included in user/nice and are intentionally skipped.
func parseCPULine(line string) (total, idle uint64, err error) {
	f := strings.Fields(line)
	if len(f) < 5 || f[0] != "cpu" {
		return 0, 0, errors.New("bad cpu line")
	}
	n := len(f) - 1
	if n > 8 {
		n = 8
	}
	for i := 1; i <= n; i++ {
		v, e := strconv.ParseUint(f[i], 10, 64)
		if e != nil {
			return 0, 0, fmt.Errorf("bad cpu value %q", f[i])
		}
		total += v
	}
	iv, e := strconv.ParseUint(f[4], 10, 64)
	if e != nil {
		return 0, 0, e
	}
	idle = iv
	if len(f) > 5 {
		if iw, e := strconv.ParseUint(f[5], 10, 64); e == nil {
			idle += iw
		}
	}
	return total, idle, nil
}

// cpuUsage computes 1 - idle_delta/total_delta between two /proc/stat samples,
// clamped to 0..100 and rounded to one decimal place.
func cpuUsage(a, b string) (float64, error) {
	t1, i1, err := parseCPULine(a)
	if err != nil {
		return 0, err
	}
	t2, i2, err := parseCPULine(b)
	if err != nil {
		return 0, err
	}
	// A counter reset (reboot) or a non-monotonic sample is not a real delta.
	if t2 < t1 || i2 < i1 {
		return 0, nil
	}
	dt := t2 - t1
	di := i2 - i1
	if dt == 0 {
		return 0, nil
	}
	if di > dt {
		di = dt
	}
	usage := (1 - float64(di)/float64(dt)) * 100
	if usage < 0 {
		usage = 0
	}
	if usage > 100 {
		usage = 100
	}
	return math.Round(usage*10) / 10, nil
}

// parseMem reads MemTotal / MemAvailable (kB) and converts to bytes.
func parseMem(lines []string) (total, used uint64, err error) {
	var totalKB, availKB uint64
	var haveTotal, haveAvail bool
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		switch f[0] {
		case "MemTotal:":
			if v, e := strconv.ParseUint(f[1], 10, 64); e == nil {
				totalKB, haveTotal = v, true
			}
		case "MemAvailable:":
			if v, e := strconv.ParseUint(f[1], 10, 64); e == nil {
				availKB, haveAvail = v, true
			}
		}
	}
	if !haveTotal || !haveAvail {
		return 0, 0, errors.New("missing MemTotal/MemAvailable")
	}
	if availKB > totalKB {
		availKB = totalKB
	}
	return totalKB * 1024, (totalKB - availKB) * 1024, nil
}

// parseDisk sums the total/used columns of every df row. The -P output keeps
// one filesystem per line; numeric columns are at fixed positions even when a
// mount point contains spaces. Non-data lines are skipped.
func parseDisk(lines []string) (total, used uint64, err error) {
	found := 0
	for _, l := range lines {
		f := strings.Fields(l)
		if len(f) < 3 {
			continue
		}
		if strings.HasPrefix(f[0], "Filesystem") {
			continue
		}
		t, e1 := strconv.ParseUint(f[1], 10, 64)
		u, e2 := strconv.ParseUint(f[2], 10, 64)
		if e1 != nil || e2 != nil {
			continue
		}
		total += t
		used += u
		found++
	}
	if found == 0 {
		return 0, 0, errors.New("no disk rows")
	}
	return total, used, nil
}

// parseNetDev sums rx_bytes / tx_bytes over every non-lo interface. rx_bytes is
// the first counter after the colon and tx_bytes the ninth. Header lines and
// the loopback interface are skipped. A host with no non-lo interface returns
// (0, 0) with no error, which is the real answer.
func parseNetDev(lines []string) (rx, tx uint64, err error) {
	for _, l := range lines {
		idx := strings.Index(l, ":")
		if idx < 0 {
			continue
		}
		iface := strings.TrimSpace(l[:idx])
		if iface == "" || iface == "lo" {
			continue
		}
		stats := strings.Fields(l[idx+1:])
		if len(stats) < 9 {
			continue
		}
		r, e1 := strconv.ParseUint(stats[0], 10, 64)
		t, e2 := strconv.ParseUint(stats[8], 10, 64)
		if e1 != nil || e2 != nil {
			continue
		}
		rx += r
		tx += t
	}
	return rx, tx, nil
}

func cleanPrettyName(line string) string {
	v := strings.TrimSpace(strings.TrimPrefix(line, "PRETTY_NAME="))
	if len(v) >= 2 {
		q := v[0]
		if (q == '"' || q == '\'') && v[len(v)-1] == q {
			v = v[1 : len(v)-1]
		}
	}
	return v
}

type sysSample struct {
	hostname  string
	os        string
	kernel    string
	uptimeSec int64
	cpuCores  int
}

// parseSys reads the SYS section best-effort: hostname, uname -srm, nproc,
// uptime and PRETTY_NAME. Missing lines simply leave zero/empty values.
func parseSys(lines []string) sysSample {
	var s sysSample
	var data []string
	for _, l := range lines {
		t := strings.TrimSpace(l)
		if t == "" {
			continue
		}
		if strings.HasPrefix(t, "PRETTY_NAME=") {
			s.os = cleanPrettyName(t)
			continue
		}
		data = append(data, t)
	}
	if len(data) > 0 {
		s.hostname = data[0]
	}
	if len(data) > 1 {
		s.kernel = data[1]
	}
	if len(data) > 2 {
		if n, e := strconv.Atoi(data[2]); e == nil && n > 0 {
			s.cpuCores = n
		}
	}
	if len(data) > 3 {
		if f, e := strconv.ParseFloat(data[3], 64); e == nil && f > 0 {
			s.uptimeSec = int64(f)
		}
	}
	return s
}

// parseAll assembles one sample. CPU/MEM/DISK/NET are required; SYS is
// best-effort. Any malformed required section returns errParse so the caller
// marks the collection offline instead of publishing fabricated zeros.
func parseAll(output string) (sample, error) {
	sec := sections(output)
	for _, m := range []string{"__CPU1__", "__CPU2__", "__MEM__", "__DISK__", "__NET__", "__SYS__"} {
		if _, ok := sec[m]; !ok {
			return sample{}, fmt.Errorf("%w: missing %s section", errParse, m)
		}
	}
	var s sample
	pct, err := cpuUsage(firstLine(sec["__CPU1__"]), firstLine(sec["__CPU2__"]))
	if err != nil {
		return sample{}, fmt.Errorf("%w: cpu: %v", errParse, err)
	}
	s.cpuPct = pct

	mt, mu, err := parseMem(sec["__MEM__"])
	if err != nil {
		return sample{}, fmt.Errorf("%w: mem: %v", errParse, err)
	}
	s.memTotal, s.memUsed = mt, mu

	dt, du, err := parseDisk(sec["__DISK__"])
	if err != nil {
		return sample{}, fmt.Errorf("%w: disk: %v", errParse, err)
	}
	s.diskTotal, s.diskUsed = dt, du

	rx, tx, err := parseNetDev(sec["__NET__"])
	if err != nil {
		return sample{}, fmt.Errorf("%w: net: %v", errParse, err)
	}
	s.netRx, s.netTx = rx, tx

	sys := parseSys(sec["__SYS__"])
	s.hostname, s.os, s.kernel = sys.hostname, sys.os, sys.kernel
	s.uptimeSec, s.cpuCores = sys.uptimeSec, sys.cpuCores
	return s, nil
}
