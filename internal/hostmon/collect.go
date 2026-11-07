package hostmon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	// collectBudget bounds one entire collection: TCP dial + SSH handshake +
	// the read-only command (which contains a 1s sleep). A deadline on the
	// underlying net.Conn and a watchdog that closes the client on ctx.Done()
	// guarantee the caller is never left blocked.
	collectBudget   = 15 * time.Second
	defaultInterval = 30 * time.Second
)

// collectScript runs a single read-only SSH session. Every command only reads
// procfs / standard system files; there are no writes, no sudo and no package
// installation. Markers delimit the sections consumed by parseAll.
const collectScript = `printf '__CPU1__\n'; head -1 /proc/stat; sleep 1; printf '__CPU2__\n'; head -1 /proc/stat;
printf '__MEM__\n'; grep -E '^(MemTotal|MemAvailable):' /proc/meminfo;
printf '__DISK__\n'; df -B1 -P -x tmpfs -x devtmpfs | tail -n +2;
printf '__NET__\n'; cat /proc/net/dev;
printf '__SYS__\n'; hostname; uname -srm; nproc; cut -d' ' -f1 /proc/uptime; grep '^PRETTY_NAME=' /etc/os-release`

// sample is the parsed, raw result of one collection (before rate derivation).
type sample struct {
	hostname  string
	os        string
	kernel    string
	uptimeSec int64
	cpuCores  int
	cpuPct    float64
	memTotal  uint64
	memUsed   uint64
	diskTotal uint64
	diskUsed  uint64
	netRx     uint64
	netTx     uint64
}

// collectFunc returns a sample, an optional newly-trusted TOFU fingerprint and
// an error. It is a field on Monitor so unit tests can substitute a fake.
type collectFunc func(ctx context.Context, cfg SSHConfig) (sample, string, error)

// realCollect dials over SSH, runs collectScript and parses its output.
func realCollect(ctx context.Context, cfg SSHConfig) (sample, string, error) {
	auth, err := buildAuth(cfg)
	if err != nil {
		return sample{}, "", err
	}

	captured := make(chan string, 1)
	cc := &ssh.ClientConfig{
		User:            cfg.User,
		Auth:            auth,
		HostKeyCallback: hostKeyCallback(cfg.HostKeyFingerprint, captured),
	}
	client, err := dialSSH(ctx, cfg, cc)
	if err != nil {
		return sample{}, "", err
	}

	// Watchdog: cancel (Stop / budget expiry) closes the client, which unblocks
	// a blocking CombinedOutput even if the conn deadline alone is not enough.
	defused := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			client.Close()
		case <-defused:
		}
	}()
	defer close(defused)
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		return sample{}, "", err
	}
	defer session.Close()

	out, err := session.CombinedOutput(collectScript)
	if err != nil {
		return sample{}, "", err
	}
	s, err := parseAll(string(out))
	if err != nil {
		return sample{}, "", err
	}

	var fp string
	select {
	case fp = <-captured:
	default:
	}
	return s, fp, nil
}

// dialSSH opens the TCP connection with the caller's context, then bounds the
// whole SSH session (handshake + command I/O) with a single conn deadline. A
// short-lived watchdog aborts a stalled handshake promptly on cancel/timeout
// so Stop() never has to wait for the full budget.
func dialSSH(ctx context.Context, cfg SSHConfig, cc *ssh.ClientConfig) (*ssh.Client, error) {
	addr := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	} else {
		_ = conn.SetDeadline(time.Now().Add(collectBudget))
	}

	handshakeDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-handshakeDone:
		}
	}()

	c, chans, reqs, err := ssh.NewClientConn(conn, addr, cc)
	close(handshakeDone)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return ssh.NewClient(c, chans, reqs), nil
}

// buildAuth assembles the auth methods. A malformed private key is reported
// early (mapped by safeError to a generic message) instead of silently leaving
// the client with no credentials.
func buildAuth(cfg SSHConfig) ([]ssh.AuthMethod, error) {
	var methods []ssh.AuthMethod
	if cfg.PrivateKey != "" {
		signer, err := ssh.ParsePrivateKey([]byte(cfg.PrivateKey))
		if err != nil {
			return nil, fmt.Errorf("invalid private key: %w", err)
		}
		methods = append(methods, ssh.PublicKeys(signer))
	}
	if cfg.Password != "" {
		methods = append(methods, ssh.Password(cfg.Password))
	}
	if len(methods) == 0 {
		return nil, errors.New("no credentials provided")
	}
	return methods, nil
}
