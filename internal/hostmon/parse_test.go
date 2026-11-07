package hostmon

import (
	"strings"
	"testing"
)

// fullOutput is a realistic concatenation of what collectScript prints on an
// Ubuntu 24.04 host. The second df row deliberately has spaces in its mount
// point, and the lo interface must be excluded from traffic totals.
const fullOutput = `__CPU1__
cpu  1000 0 500 8000 200 0 100 0 0 0
__CPU2__
cpu  1100 0 600 8100 200 0 100 0 0 0
__MEM__
MemTotal:       16384000 kB
MemAvailable:    8000000 kB
__DISK__
/dev/sda1       1000000000000 400000000000 600000000000  40% /
/dev/sdb1        200000000000  50000000000 150000000000  25% /mnt/data with space
__NET__
Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 999999 10 0 0 0 0 0 0 888888 10 0 0 0 0 0 0
  eth0: 1000 20 0 0 0 0 0 0 2000 20 0 0 0 0 0 0
  ens3: 3000 30 0 0 0 0 0 0 4000 30 0 0 0 0 0 0
__SYS__
vps-ubuntu
Linux 6.8.0-31-generic x86_64
4
12345.67
PRETTY_NAME="Ubuntu 24.04.1 LTS"
`

func TestParseAllFullSample(t *testing.T) {
	s, err := parseAll(fullOutput)
	if err != nil {
		t.Fatalf("parseAll(fullOutput) error: %v", err)
	}
	if s.cpuPct != 66.7 {
		t.Errorf("cpuPct = %v, want 66.7", s.cpuPct)
	}
	if s.memTotal != 16384000*1024 || s.memUsed != (16384000-8000000)*1024 {
		t.Errorf("mem = (%d, %d), want (%d, %d)", s.memTotal, s.memUsed,
			16384000*1024, (16384000-8000000)*1024)
	}
	// Multi-line df, including a mount point containing spaces.
	if s.diskTotal != 1200000000000 || s.diskUsed != 450000000000 {
		t.Errorf("disk = (%d, %d), want (1200000000000, 450000000000)", s.diskTotal, s.diskUsed)
	}
	// lo (999999/888888) must be excluded: eth0 1000/2000 + ens3 3000/4000.
	if s.netRx != 4000 || s.netTx != 6000 {
		t.Errorf("net = (%d, %d), want (4000, 6000)", s.netRx, s.netTx)
	}
	if s.hostname != "vps-ubuntu" {
		t.Errorf("hostname = %q", s.hostname)
	}
	if s.kernel != "Linux 6.8.0-31-generic x86_64" {
		t.Errorf("kernel = %q", s.kernel)
	}
	if s.cpuCores != 4 {
		t.Errorf("cpuCores = %d, want 4", s.cpuCores)
	}
	if s.uptimeSec != 12345 {
		t.Errorf("uptimeSec = %d, want 12345", s.uptimeSec)
	}
	if s.os != "Ubuntu 24.04.1 LTS" {
		t.Errorf("os = %q, want unquoted PRETTY_NAME", s.os)
	}
}

func TestCPUBoundaries(t *testing.T) {
	cases := []struct {
		name string
		a, b string
		want float64
	}{
		{
			name: "100 percent (idle delta zero)",
			a:    "cpu  1000 0 500 8000 200 0 100 0",
			b:    "cpu  2000 0 500 8000 200 0 100 0",
			want: 100,
		},
		{
			name: "0 percent (idle delta equals total delta)",
			a:    "cpu  1000 0 500 8000 200 0 100 0",
			b:    "cpu  1000 0 500 9000 200 0 100 0",
			want: 0,
		},
		{
			name: "no elapsed ticks",
			a:    "cpu  1000 0 500 8000 200 0 100 0",
			b:    "cpu  1000 0 500 8000 200 0 100 0",
			want: 0,
		},
		{
			name: "counter reset (non-monotonic)",
			a:    "cpu  5000 0 500 8000 200 0 100 0",
			b:    "cpu  1000 0 500 8000 200 0 100 0",
			want: 0,
		},
		{
			name: "rounded to one decimal",
			a:    "cpu  1000 0 500 8000 200 0 100 0",
			b:    "cpu  1100 0 600 8100 200 0 100 0",
			want: 66.7,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := cpuUsage(tc.a, tc.b)
			if err != nil {
				t.Fatalf("cpuUsage error: %v", err)
			}
			if got != tc.want {
				t.Errorf("cpuUsage = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseCPULine(t *testing.T) {
	total, idle, err := parseCPULine("cpu  1000 0 500 8000 200 0 100 0 0 0")
	if err != nil {
		t.Fatalf("parseCPULine error: %v", err)
	}
	if total != 9800 || idle != 8200 {
		t.Errorf("(total, idle) = (%d, %d), want (9800, 8200)", total, idle)
	}
	for _, bad := range []string{"", "garbage", "cpu 1 2", "cpu 1 2 3 4 x"} {
		if _, _, err := parseCPULine(bad); err == nil {
			t.Errorf("parseCPULine(%q) expected error, got nil", bad)
		}
	}
}

func TestParseMem(t *testing.T) {
	total, used, err := parseMem([]string{
		"MemTotal:       16384000 kB",
		"MemFree:          100000 kB", // unrelated line must be ignored
		"MemAvailable:    8000000 kB",
	})
	if err != nil {
		t.Fatalf("parseMem error: %v", err)
	}
	if total != 16384000*1024 || used != (16384000-8000000)*1024 {
		t.Errorf("parseMem = (%d, %d)", total, used)
	}
	// Missing / malformed values must not panic and must report an error.
	for _, bad := range [][]string{nil, {""}, {"MemTotal: x kB"}, {"MemTotal: 10 kB"}} {
		if _, _, err := parseMem(bad); err == nil {
			t.Errorf("parseMem(%v) expected error", bad)
		}
	}
	// Available larger than total is clamped instead of producing a negative.
	total, used, err = parseMem([]string{"MemTotal: 10 kB", "MemAvailable: 99 kB"})
	if err != nil || total != 10*1024 || used != 0 {
		t.Errorf("clamp: (%d, %d, %v)", total, used, err)
	}
}

func TestParseDisk(t *testing.T) {
	total, used, err := parseDisk([]string{
		"Filesystem         1B-blocks      Used Available Capacity Mounted on",
		"/dev/sda1       1000000000000 400000000000 600000000000  40% /",
		"df: /some/path: Permission denied", // stderr noise, skipped
		"/dev/sdb1        200000000000  50000000000 150000000000  25% /mnt/with space",
	})
	if err != nil {
		t.Fatalf("parseDisk error: %v", err)
	}
	if total != 1200000000000 || used != 450000000000 {
		t.Errorf("parseDisk = (%d, %d)", total, used)
	}
	for _, bad := range [][]string{nil, {""}, {"garbage"}} {
		if _, _, err := parseDisk(bad); err == nil {
			t.Errorf("parseDisk(%v) expected error", bad)
		}
	}
}

func TestParseNetDev(t *testing.T) {
	lines := []string{
		"Inter-|   Receive                                                |  Transmit",
		" face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed",
		"    lo: 999999 10 0 0 0 0 0 0 888888 10 0 0 0 0 0 0",
		"  eth0: 1000 20 0 0 0 0 0 0 2000 20 0 0 0 0 0 0",
		"  ens3:3000 30 0 0 0 0 0 0 4000 30 0 0 0 0 0 0", // no space after colon
	}
	rx, tx, err := parseNetDev(lines)
	if err != nil {
		t.Fatalf("parseNetDev error: %v", err)
	}
	if rx != 4000 || tx != 6000 {
		t.Errorf("parseNetDev = (%d, %d), want (4000, 6000)", rx, tx)
	}
	// Only lo -> no non-lo traffic, but no error either.
	rx, tx, err = parseNetDev([]string{"    lo: 1 0 0 0 0 0 0 0 1 0 0 0 0 0 0 0"})
	if err != nil || rx != 0 || tx != 0 {
		t.Errorf("lo only = (%d, %d, %v)", rx, tx, err)
	}
	// Empty / garbage input must not panic.
	rx, tx, err = parseNetDev(nil)
	if err != nil || rx != 0 || tx != 0 {
		t.Errorf("empty = (%d, %d, %v)", rx, tx, err)
	}
}

func TestCleanPrettyName(t *testing.T) {
	cases := map[string]string{
		`PRETTY_NAME="Ubuntu 24.04.1 LTS"`:  "Ubuntu 24.04.1 LTS",
		`PRETTY_NAME='Debian GNU/Linux 12'`: "Debian GNU/Linux 12",
		`PRETTY_NAME=Ubuntu`:                "Ubuntu",
	}
	for in, want := range cases {
		if got := cleanPrettyName(in); got != want {
			t.Errorf("cleanPrettyName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseSysBestEffort(t *testing.T) {
	s := parseSys([]string{
		"myhost",
		"Linux 6.8.0-31-generic x86_64",
		"8",
		"987.65",
		`PRETTY_NAME="Ubuntu 24.04 LTS"`,
	})
	if s.hostname != "myhost" || s.kernel != "Linux 6.8.0-31-generic x86_64" ||
		s.cpuCores != 8 || s.uptimeSec != 987 || s.os != "Ubuntu 24.04 LTS" {
		t.Errorf("parseSys = %+v", s)
	}
	// Missing lines degrade to zero values, never panic.
	for _, bad := range [][]string{nil, {""}, {"myhost"}, {"myhost", "uname", "nope", "nope"}} {
		_ = parseSys(bad)
	}
}

func TestParseAllBadInputNoPanic(t *testing.T) {
	inputs := []string{
		"",
		"total garbage",
		"__CPU1__\n__CPU2__\n__MEM__\n__DISK__\n__NET__\n__SYS__\n", // all empty sections
		"__CPU1__\ncpu 1 2 3 4\n__CPU2__\ncpu 1 2 3 4\n",            // missing sections
		strings.Repeat("x", 10000),
	}
	for _, in := range inputs {
		if _, err := parseAll(in); err == nil {
			t.Errorf("parseAll(%q...) expected an error, got nil", truncate(in, 24))
		}
	}
	// sections must tolerate CRLF and still locate markers.
	sec := sections("__CPU1__\r\ncpu 1 2 3 4 5\r\n__MEM__\r\nMemTotal: 10 kB\r\n")
	if _, ok := sec["__CPU1__"]; !ok {
		t.Errorf("sections did not find __CPU1__ in CRLF input")
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
