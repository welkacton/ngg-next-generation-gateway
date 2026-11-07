package hostmon

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func testMonitor(dir string, fn collectFunc) *Monitor {
	m := New(dir)
	m.collectFn = fn
	return m
}

// offlineFake keeps every Set/Refresh instant: no unit test ever opens a socket.
func offlineFake(_ context.Context, _ SSHConfig) (sample, string, error) {
	return sample{}, "", errors.New("offline")
}

func testCfg() SSHConfig {
	return SSHConfig{
		HostID: "",
		Host:   "50.118.185.194",
		Port:   0, // normalises to 22
		User:   "root",
		// A distinctive secret that must never appear in any returned field.
		Password:   "S3cr3t-P@ssw0rd!",
		PrivateKey: "-----BEGIN OPENSSH PRIVATE KEY-----AAAA-----END-----",
	}
}

func TestConfigsIsSanitizedAndIsACopy(t *testing.T) {
	m := testMonitor(t.TempDir(), offlineFake)
	if err := m.Set(testCfg()); err != nil {
		t.Fatalf("Set error: %v", err)
	}

	views := m.Configs()
	if len(views) != 1 {
		t.Fatalf("Configs len = %d, want 1", len(views))
	}
	v := views[0]
	if !v.HasPassword || !v.HasPrivateKey {
		t.Errorf("HasPassword/HasPrivateKey = (%v, %v), want (true, true)", v.HasPassword, v.HasPrivateKey)
	}
	if v.Port != 22 || v.Host != "50.118.185.194" || v.User != "root" {
		t.Errorf("ConfigView = %+v", v)
	}

	// The serialised view must contain neither the credential keys nor their
	// values. has_password / has_private_key are boolean flags and are allowed.
	b, err := json.Marshal(views)
	if err != nil {
		t.Fatalf("marshal views: %v", err)
	}
	js := string(b)
	var arr []map[string]interface{}
	if err := json.Unmarshal(b, &arr); err != nil || len(arr) != 1 {
		t.Fatalf("decode views array: %v", err)
	}
	if _, ok := arr[0]["password"]; ok {
		t.Errorf("Configs() JSON contains a password field: %s", js)
	}
	if _, ok := arr[0]["private_key"]; ok {
		t.Errorf("Configs() JSON contains a private_key field: %s", js)
	}
	for _, leak := range []string{"S3cr3t-P@ssw0rd!", "BEGIN OPENSSH PRIVATE KEY"} {
		if strings.Contains(js, leak) {
			t.Errorf("Configs() JSON leaks %q: %s", leak, js)
		}
	}

	// Metrics/All must also be credential-free.
	all, _ := json.Marshal(m.All())
	if strings.Contains(string(all), "S3cr3t-P@ssw0rd!") {
		t.Errorf("All() JSON leaks the password: %s", all)
	}

	// Configs() returns a copy: mutating the result must not affect the monitor.
	views[0].Host = "mutated"
	if m.Configs()[0].Host == "mutated" {
		t.Error("Configs() leaked the internal slice")
	}
}

func TestSetLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	m := testMonitor(dir, offlineFake)
	cfg := testCfg()
	cfg.HostKeyFingerprint = "SHA256:stored"
	if err := m.Set(cfg); err != nil {
		t.Fatalf("Set error: %v", err)
	}

	m2 := testMonitor(dir, offlineFake)
	if err := m2.Load(); err != nil {
		t.Fatalf("Load error: %v", err)
	}
	views := m2.Configs()
	if len(views) != 1 {
		t.Fatalf("after Load Configs len = %d, want 1", len(views))
	}
	if views[0].Host != "50.118.185.194" || views[0].Port != 22 ||
		!views[0].HasPassword || views[0].HostKeyFingerprint != "SHA256:stored" {
		t.Errorf("restored view = %+v", views[0])
	}

	// Re-Set without a fingerprint must not downgrade the stored TOFU key.
	cfg2 := testCfg()
	cfg2.HostKeyFingerprint = ""
	if err := m2.Set(cfg2); err != nil {
		t.Fatalf("second Set error: %v", err)
	}
	if got := m2.Configs()[0].HostKeyFingerprint; got != "SHA256:stored" {
		t.Errorf("fingerprint was downgraded to %q", got)
	}

	// A fresh install (missing file) loads cleanly with no configs.
	if err := testMonitor(t.TempDir(), offlineFake).Load(); err != nil {
		t.Errorf("Load on empty dir: %v", err)
	}
}

func TestScrubAndSafeError(t *testing.T) {
	cfg := testCfg()
	if got := scrub("auth failed with S3cr3t-P@ssw0rd! and key", cfg); strings.Contains(got, "S3cr3t-P@ssw0rd!") {
		t.Errorf("scrub did not remove the password: %s", got)
	}
	if got := scrub("-----BEGIN OPENSSH PRIVATE KEY-----AAAA-----END-----", cfg); strings.Contains(got, "BEGIN OPENSSH") {
		t.Errorf("scrub did not remove the private key: %s", got)
	}

	if got := safeError(nil, cfg); got != "" {
		t.Errorf("safeError(nil) = %q", got)
	}
	// Even an arbitrary error containing the password must be cleaned.
	if got := safeError(errors.New("boom S3cr3t-P@ssw0rd!"), cfg); strings.Contains(got, "S3cr3t-P@ssw0rd!") {
		t.Errorf("safeError leaked the password: %s", got)
	}
	// Classified errors use fixed, generic Chinese messages.
	if got := safeError(ErrHostKeyMismatch, cfg); !strings.Contains(got, "主机密钥校验失败") {
		t.Errorf("host key error = %q", got)
	}
	if got := safeError(errors.New("ssh: unable to authenticate, attempted methods [none password]"), cfg); !strings.Contains(got, "认证失败") {
		t.Errorf("auth error = %q", got)
	}
	if got := safeError(errors.New("ssh: handshake failed: hostmon: host key fingerprint mismatch"), cfg); !strings.Contains(got, "主机密钥校验失败") {
		t.Errorf("wrapped host key error = %q", got)
	}
	if got := safeError(errParse, cfg); !strings.Contains(got, "解析失败") {
		t.Errorf("parse error = %q", got)
	}
}

func TestRefreshAndRemoveNotFound(t *testing.T) {
	m := testMonitor(t.TempDir(), offlineFake)
	if _, err := m.Refresh("anything"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Refresh on empty monitor = %v, want ErrNotFound", err)
	}
	// DELETE is idempotent: removing a config that is not there succeeds.
	if err := m.Remove("anything"); err != nil {
		t.Errorf("Remove on empty monitor = %v, want nil", err)
	}

	if err := m.Set(testCfg()); err != nil {
		t.Fatalf("Set error: %v", err)
	}
	// An IP-matched entry (HostID == "") is addressed by its Host.
	if _, err := m.Refresh("50.118.185.194"); err != nil {
		t.Errorf("Refresh by Host: %v", err)
	}
	if err := m.Remove("50.118.185.194"); err != nil {
		t.Errorf("Remove by Host: %v", err)
	}
	if len(m.Configs()) != 0 {
		t.Error("config was not removed")
	}
	if _, err := m.Refresh("50.118.185.194"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Refresh after remove = %v, want ErrNotFound", err)
	}
}

func TestMetricsAllCopiesAndRate(t *testing.T) {
	calls := 0
	m := testMonitor(t.TempDir(), func(_ context.Context, _ SSHConfig) (sample, string, error) {
		calls++
		return sample{
			cpuCores: 4, cpuPct: 12.5,
			netRx: uint64(calls * 1000),
			netTx: uint64(calls * 500),
		}, "", nil
	})
	if err := m.Set(testCfg()); err != nil {
		t.Fatalf("Set error: %v", err)
	}

	// Set performed one immediate collection: first sample has rate 0.
	first, ok := m.Metrics("50.118.185.194")
	if !ok || !first.Online || first.NetRxRate != 0 || first.NetTxRate != 0 {
		t.Errorf("first metrics = %+v (ok=%v)", first, ok)
	}

	all := m.All()
	if len(all) == 0 {
		t.Fatal("All() is empty after a successful collection")
	}
	// All() and Metrics() return copies: mutating them must not affect state.
	for k := range all {
		all[k] = Metrics{Online: false}
	}
	delete(all, "50.118.185.194")
	if _, ok := m.Metrics("50.118.185.194"); !ok || len(m.All()) == 0 {
		t.Error("All()/Metrics() leaked internal maps")
	}

	time.Sleep(2 * time.Millisecond)
	second, err := m.Refresh("50.118.185.194")
	if err != nil {
		t.Fatalf("Refresh error: %v", err)
	}
	// The second sample has a real elapsed delta -> a positive, non-fabricated rate.
	if second.NetRxRate <= 0 || second.NetTxRate <= 0 {
		t.Errorf("second rates = (%v, %v), want positive", second.NetRxRate, second.NetTxRate)
	}
	if second.NetRxBytes <= first.NetRxBytes {
		t.Errorf("cumulative bytes did not increase: %d -> %d", first.NetRxBytes, second.NetRxBytes)
	}
}

func TestStartStopReentrant(t *testing.T) {
	m := testMonitor(t.TempDir(), func(_ context.Context, _ SSHConfig) (sample, string, error) {
		return sample{cpuCores: 2, cpuPct: 1.0}, "", nil
	})
	if err := m.Set(testCfg()); err != nil {
		t.Fatalf("Set error: %v", err)
	}
	m.Start(20 * time.Millisecond)
	m.Stop()
	m.Stop() // reentrant, must not panic or block
	if len(m.All()) == 0 {
		t.Error("Start's initial round did not populate metrics")
	}
}

// An empty password on Set means "keep the stored credential", and promoting
// an IP-keyed preset to a named node must not leave a duplicate config.
func TestSetKeepsCredentialAndReplacesPreset(t *testing.T) {
	m := testMonitor(t.TempDir(), offlineFake)
	cfg := testCfg()
	cfg.Password = "s3cret"
	if err := m.Set(cfg); err != nil {
		t.Fatalf("initial Set: %v", err)
	}

	named := cfg
	named.HostID = "vps-main"
	named.Password = ""
	named.PrivateKey = ""
	if err := m.Set(named); err != nil {
		t.Fatalf("Set with empty password must reuse the stored one: %v", err)
	}
	configs := m.Configs()
	if len(configs) != 1 {
		t.Fatalf("configs = %d, want 1 (preset must be replaced): %+v", len(configs), configs)
	}
	if configs[0].HostID != "vps-main" || !configs[0].HasPassword || !configs[0].HasPrivateKey {
		t.Fatalf("named config lost its credentials: %+v", configs[0])
	}

	// Changing the password must not bring the old IP-keyed entry back.
	rotated := named
	rotated.Password = "rotated"
	rotated.PrivateKey = ""
	if err := m.Set(rotated); err != nil {
		t.Fatalf("rotate Set: %v", err)
	}
	if got := len(m.Configs()); got != 1 {
		t.Fatalf("configs after rotation = %d, want 1", got)
	}
}
