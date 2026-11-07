package hostmon

import (
	"strings"
	"testing"
)

// TestLineEmitterSplitsChunks: the SSH reader hands us arbitrary byte chunks, so
// a step marker can arrive split in two. The emitter must deliver whole lines
// only — a half-delivered `NGG_STEP 1 …` line would be dropped by the scanner.
func TestLineEmitterSplitsChunks(t *testing.T) {
	var got []string
	e := newLineEmitter(func(line string) { got = append(got, line) })

	for _, chunk := range []string{"NGG_ST", "EP 1 预检\r\nNGG_OK 1\npar", "tial"} {
		if _, err := e.Write([]byte(chunk)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if len(got) != 2 {
		t.Fatalf("lines = %v, want 2 complete lines", got)
	}
	if got[0] != "NGG_STEP 1 预检" || got[1] != "NGG_OK 1" {
		t.Fatalf("lines = %v", got)
	}
	if string(e.buf) != "partial" {
		t.Fatalf("unterminated tail = %q, want partial", e.buf)
	}
}

// TestCappedBufferKeepsTail: a runaway script must not exhaust memory, and what
// survives must be the END of the output (where the failure is).
func TestCappedBufferKeepsTail(t *testing.T) {
	c := newCappedBuffer(8)
	if _, err := c.Write([]byte("0123456789")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := c.String(); got != "23456789" {
		t.Fatalf("buf = %q, want the last 8 bytes", got)
	}
	if !c.Overflowed() {
		t.Fatal("overflow not reported")
	}

	// Small writes: the head is dropped, the newest bytes stay.
	c = newCappedBuffer(8)
	for _, s := range []string{"abc", "def", "ghi", "jkl"} {
		if _, err := c.Write([]byte(s)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if got := c.String(); got != "efghijkl" {
		t.Fatalf("buf = %q, want efghijkl", got)
	}
}

// TestCappedBufferUnderLimit must not report truncation, otherwise every deploy
// would claim its log had been cut.
func TestCappedBufferUnderLimit(t *testing.T) {
	c := newCappedBuffer(64)
	for _, s := range []string{"NGG_STEP 1 预检\n", "NGG_OK 1\n"} {
		if _, err := c.Write([]byte(s)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	if c.Overflowed() {
		t.Fatal("small output wrongly reported as truncated")
	}
	if !strings.HasSuffix(c.String(), "NGG_OK 1\n") {
		t.Fatalf("buf = %q", c.String())
	}
}
