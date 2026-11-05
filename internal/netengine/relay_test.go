package netengine

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

// Regression: the traffic dashboard needs real download AND upload counters.
// Before the fix both directions were added to BytesOut, leaving BytesIn at 0.
func TestRelayCountsDirectionsSeparately(t *testing.T) {
	e := &Engine{metrics: &Metrics{}}
	client, clientPeer := net.Pipe()
	upstream, upstreamPeer := net.Pipe()
	rec := &SessionRec{State: "active"}

	relayDone := make(chan struct{})
	go func() {
		e.relay(client, upstream, rec)
		close(relayDone)
	}()

	// client -> upstream: 100 bytes of upload.
	upload := bytes.Repeat([]byte{'u'}, 100)
	go func() { _, _ = clientPeer.Write(upload) }()
	gotUpload := make([]byte, len(upload))
	if _, err := io.ReadFull(upstreamPeer, gotUpload); err != nil {
		t.Fatalf("read upload: %v", err)
	}

	// upstream -> client: 250 bytes of download.
	download := bytes.Repeat([]byte{'d'}, 250)
	go func() { _, _ = upstreamPeer.Write(download) }()
	gotDownload := make([]byte, len(download))
	if _, err := io.ReadFull(clientPeer, gotDownload); err != nil {
		t.Fatalf("read download: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)
	for e.metrics.BytesIn.Load() < uint64(len(download)) || e.metrics.BytesOut.Load() < uint64(len(upload)) {
		if time.Now().After(deadline) {
			t.Fatalf("counters not updated: in=%d out=%d", e.metrics.BytesIn.Load(), e.metrics.BytesOut.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}

	if !bytes.Equal(gotUpload, upload) || !bytes.Equal(gotDownload, download) {
		t.Fatal("relay corrupted payload")
	}
	if got := e.metrics.BytesIn.Load(); got != uint64(len(download)) {
		t.Fatalf("BytesIn = %d, want %d", got, len(download))
	}
	if got := e.metrics.BytesOut.Load(); got != uint64(len(upload)) {
		t.Fatalf("BytesOut = %d, want %d", got, len(upload))
	}
	rec.mu.Lock()
	in, out := rec.BytesIn, rec.BytesOut
	rec.mu.Unlock()
	if in != uint64(len(download)) || out != uint64(len(upload)) {
		t.Fatalf("session bytes = (%d,%d), want (%d,%d)", in, out, len(download), len(upload))
	}

	_ = client.Close()
	_ = clientPeer.Close()
	_ = upstream.Close()
	_ = upstreamPeer.Close()
	select {
	case <-relayDone:
	case <-time.After(2 * time.Second):
		t.Fatal("relay did not exit after connections closed")
	}
}
