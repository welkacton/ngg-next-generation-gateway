package hostmon

import (
	"errors"
	"net"

	"golang.org/x/crypto/ssh"
)

// ErrHostKeyMismatch is returned (usually wrapped by the SSH handshake) when a
// server key does not exactly match the stored TOFU fingerprint. safeError
// turns it into a generic Chinese message; the raw error never reaches a user.
var ErrHostKeyMismatch = errors.New("hostmon: host key fingerprint mismatch")

// compareFingerprint is the pure, unit-testable core of the TOFU policy:
//   - expected empty -> trust on first use, return the fingerprint to persist;
//   - exact match     -> verified, nothing to persist;
//   - anything else   -> hard failure (possible man-in-the-middle).
func compareFingerprint(expected, actual string) (string, error) {
	switch {
	case expected == "":
		return actual, nil
	case expected == actual:
		return "", nil
	default:
		return "", ErrHostKeyMismatch
	}
}

// hostKeyCallback builds the ssh.HostKeyCallback. On a first connection it
// sends the newly trusted fingerprint on captured (buffered, non-blocking, so
// it is race-free with the handshake goroutine). InsecureIgnoreHostKey is never
// used: an empty expected fingerprint only allows exactly one TOFU capture.
func hostKeyCallback(expected string, captured chan<- string) ssh.HostKeyCallback {
	return func(hostname string, remote net.Addr, key ssh.PublicKey) error {
		fp := ssh.FingerprintSHA256(key)
		toStore, err := compareFingerprint(expected, fp)
		if err != nil {
			return err
		}
		if toStore != "" && captured != nil {
			select {
			case captured <- toStore:
			default:
			}
		}
		return nil
	}
}
