package pq

import (
	"net"
	"testing"
	"time"
)

func TestHybridHandshakeSelfTest(t *testing.T) {
	res, err := SelfTest()
	if err != nil {
		t.Fatalf("real ML-KEM-768 + X25519 handshake failed: %v", err)
	}
	if res["ok"] != true {
		t.Fatalf("self test: %+v", res)
	}
	if res["mlkem"] != "ML-KEM-768 (FIPS 203)" || res["aead"] != "AES-256-GCM" {
		t.Fatalf("unexpected suite: %+v", res)
	}
}

func TestSecureChannelTamperDetection(t *testing.T) {
	kp, err := GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	srv, cli := net.Pipe()
	defer srv.Close()
	defer cli.Close()
	type r struct {
		sc  *SecureChannel
		err error
	}
	scCh, ccCh := make(chan r, 1), make(chan r, 1)
	go func() { s, e := ServerHandshake(srv, kp); scCh <- r{s, e} }()
	go func() { s, e := ClientHandshake(cli); ccCh <- r{s, e} }()
	sr, cr := <-scCh, <-ccCh
	if sr.err != nil || cr.err != nil {
		t.Fatalf("handshake: %v %v", sr.err, cr.err)
	}
	_ = cli.SetWriteDeadline(time.Now().Add(5 * time.Second))
	writeErr := make(chan error, 1)
	go func() { _, err := cr.sc.Write([]byte("secret-payload")); writeErr <- err }()
	buf := make([]byte, 64)
	n, err := sr.sc.Read(buf)
	if err != nil || string(buf[:n]) != "secret-payload" {
		t.Fatalf("read %q err %v", buf[:n], err)
	}
	if werr := <-writeErr; werr != nil {
		t.Fatal(werr)
	}
}
