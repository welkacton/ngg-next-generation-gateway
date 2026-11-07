package hostmon

import (
	"errors"
	"testing"

	"golang.org/x/crypto/ssh"
)

// fakePublicKey is a deterministic ssh.PublicKey so host key tests need no
// crypto/rand and no real key material.
type fakePublicKey struct{ data []byte }

func (k fakePublicKey) Type() string                        { return "ssh-test" }
func (k fakePublicKey) Marshal() []byte                     { return k.data }
func (k fakePublicKey) Verify([]byte, *ssh.Signature) error { return nil }

func TestCompareFingerprint(t *testing.T) {
	const fp = "SHA256:abc123def456"
	// TOFU: empty expected -> capture and trust the presented key.
	got, err := compareFingerprint("", fp)
	if err != nil || got != fp {
		t.Errorf("TOFU = (%q, %v), want (%q, nil)", got, err, fp)
	}
	// Exact match -> verified, nothing new to persist.
	got, err = compareFingerprint(fp, fp)
	if err != nil || got != "" {
		t.Errorf("match = (%q, %v), want (\"\", nil)", got, err)
	}
	// Mismatch -> hard failure (possible MITM).
	got, err = compareFingerprint(fp, "SHA256:DIFFERENT")
	if !errors.Is(err, ErrHostKeyMismatch) || got != "" {
		t.Errorf("mismatch = (%q, %v), want (\"\", ErrHostKeyMismatch)", got, err)
	}
}

func TestHostKeyCallbackTOFUAndMismatch(t *testing.T) {
	key := fakePublicKey{data: []byte("server-key-bytes")}
	fp := ssh.FingerprintSHA256(key)

	// First connection: no stored fingerprint -> TOFU captures the key.
	captured := make(chan string, 1)
	cb := hostKeyCallback("", captured)
	if err := cb("host:22", nil, key); err != nil {
		t.Fatalf("TOFU callback error: %v", err)
	}
	select {
	case stored := <-captured:
		if stored != fp {
			t.Errorf("captured fingerprint = %q, want %q", stored, fp)
		}
	default:
		t.Error("TOFU did not capture a fingerprint")
	}

	// Subsequent connection with the stored fingerprint -> verified, no new
	// capture, no error.
	captured2 := make(chan string, 1)
	cb2 := hostKeyCallback(fp, captured2)
	if err := cb2("host:22", nil, key); err != nil {
		t.Errorf("verify callback error: %v", err)
	}
	if len(captured2) != 0 {
		t.Error("verified connection should not capture a new fingerprint")
	}

	// A different stored fingerprint -> mismatch error surfaced by the callback.
	cb3 := hostKeyCallback("SHA256:some-other-key", nil)
	err := cb3("host:22", nil, key)
	if !errors.Is(err, ErrHostKeyMismatch) {
		t.Errorf("mismatch callback error = %v, want ErrHostKeyMismatch", err)
	}
}
