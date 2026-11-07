package hostmon

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

// captureFake records the exact SSHConfig each collection was invoked with,
// while still returning instantly so no unit test opens a socket.
func captureFake(dst *[]SSHConfig) collectFunc {
	return func(_ context.Context, c SSHConfig) (sample, string, error) {
		*dst = append(*dst, c)
		return sample{}, "", errors.New("offline")
	}
}

// The primary PUT case: a bound config (HostID != "") is edited with blank
// credentials ("do not change"). Both the credentials and the stored TOFU
// fingerprint must survive, while the edited fields take effect.
func TestSetEmptyCredentialsKeepStored(t *testing.T) {
	dir := t.TempDir()
	var seen []SSHConfig
	m := testMonitor(dir, captureFake(&seen))
	bound := SSHConfig{
		HostID: "nodeA", Host: "10.0.0.1", Port: 22, User: "root",
		Password: "old-secret", HostKeyFingerprint: "SHA256:stored",
	}
	if err := m.Set(bound); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	edit := SSHConfig{HostID: "nodeA", Host: "10.0.0.1", Port: 2222, User: "admin"}
	if err := m.Set(edit); err != nil {
		t.Fatalf("edit with blank credentials must succeed: %v", err)
	}
	got := seen[len(seen)-1]
	if got.Password != "old-secret" {
		t.Errorf("stored password was cleared: %q", got.Password)
	}
	if got.HostKeyFingerprint != "SHA256:stored" {
		t.Errorf("stored TOFU fingerprint was cleared: %q", got.HostKeyFingerprint)
	}
	if got.Port != 2222 || got.User != "admin" {
		t.Errorf("edited fields not applied: %+v", got)
	}

	// The preserved credential must also survive a reload from disk.
	m2 := testMonitor(dir, offlineFake)
	if err := m2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	views := m2.Configs()
	if len(views) != 1 || !views[0].HasPassword ||
		views[0].HostKeyFingerprint != "SHA256:stored" ||
		views[0].Port != 2222 || views[0].User != "admin" {
		t.Errorf("reloaded view = %+v", views)
	}
}

// A brand-new host with no credentials anywhere must fail validation.
func TestSetEmptyCredentialsOnNewHostFails(t *testing.T) {
	m := testMonitor(t.TempDir(), offlineFake)
	if err := m.Set(SSHConfig{HostID: "new", Host: "10.0.0.9", User: "root"}); err == nil {
		t.Error("expected a validation error for a new host with no credentials")
	}
}

// An explicitly supplied credential replaces the stored one (blank means
// "keep", non-blank means "change").
func TestSetExplicitPasswordOverridesStored(t *testing.T) {
	var seen []SSHConfig
	m := testMonitor(t.TempDir(), captureFake(&seen))
	base := SSHConfig{
		HostID: "nodeA", Host: "10.0.0.1", User: "root",
		Password: "old", PrivateKey: "old-key",
	}
	if err := m.Set(base); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if err := m.Set(SSHConfig{HostID: "nodeA", Host: "10.0.0.1", User: "root", Password: "new"}); err != nil {
		t.Fatalf("second Set: %v", err)
	}
	got := seen[len(seen)-1]
	if got.Password != "new" || got.PrivateKey != "" {
		t.Errorf("explicit password did not override the stored pair: %+v", got)
	}
}

// Remove uses a strict two-pass lookup (HostID first, then Host address), is
// idempotent, and never deletes a preset entry via an unrelated node id.
func TestRemoveTwoPassAndIdempotent(t *testing.T) {
	m := testMonitor(t.TempDir(), offlineFake)
	if err := m.Set(SSHConfig{HostID: "nodeA", Host: "10.0.0.1", User: "root", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	if err := m.Set(SSHConfig{Host: "10.0.0.2", User: "root", Password: "p"}); err != nil {
		t.Fatal(err)
	}
	if got := len(m.Configs()); got != 2 {
		t.Fatalf("setup configs = %d, want 2", got)
	}

	// An empty key is not a real identifier and must be a no-op.
	if err := m.Remove(""); err != nil {
		t.Fatalf("Remove(\"\"): %v", err)
	}
	if len(m.Configs()) != 2 {
		t.Fatal("Remove(\"\") deleted a config")
	}

	// Pass 1: HostID == key removes the bound entry; the preset must survive.
	if err := m.Remove("nodeA"); err != nil {
		t.Fatalf("Remove(nodeA): %v", err)
	}
	configs := m.Configs()
	if len(configs) != 1 || configs[0].HostID != "" || configs[0].Host != "10.0.0.2" {
		t.Fatalf("after Remove(nodeA): %+v", configs)
	}

	// An unrelated node id must not delete the preset (no "match any" fallback).
	if err := m.Remove("unrelated-node"); err != nil {
		t.Fatalf("Remove(unrelated-node): %v", err)
	}
	if len(m.Configs()) != 1 {
		t.Fatal("Remove(unrelated-node) deleted the preset")
	}

	// Pass 2: Host == key removes the preset by its address.
	if err := m.Remove("10.0.0.2"); err != nil {
		t.Fatalf("Remove(address): %v", err)
	}
	if len(m.Configs()) != 0 {
		t.Fatal("preset was not removed by address")
	}

	// DELETE is idempotent: unknown / repeated removals return nil.
	if err := m.Remove("10.0.0.2"); err != nil {
		t.Errorf("repeated Remove: %v", err)
	}
	if err := m.Remove("nope"); err != nil {
		t.Errorf("unknown Remove: %v", err)
	}
}

// A preset entry (HostID == "") must be present in Configs(), be collected by
// Start()'s loop, and be returned by All()/Metrics() without being dropped.
func TestPresetEntryCollectedByStartAndSurfaced(t *testing.T) {
	collected := make(chan struct{}, 32)
	var calls atomic.Int64
	m := testMonitor(t.TempDir(), func(_ context.Context, _ SSHConfig) (sample, string, error) {
		n := calls.Add(1)
		select {
		case collected <- struct{}{}:
		default:
		}
		return sample{cpuCores: int(n)}, "", nil
	})
	if err := m.Set(SSHConfig{Host: "10.0.0.2", User: "root", Password: "p"}); err != nil {
		t.Fatalf("Set preset: %v", err)
	}

	configs := m.Configs()
	if len(configs) != 1 || configs[0].HostID != "" || configs[0].Host != "10.0.0.2" {
		t.Fatalf("Configs() dropped the preset or lost its address: %+v", configs)
	}

	// Consume the signal from Set's immediate collection.
	select {
	case <-collected:
	case <-time.After(time.Second):
		t.Fatal("Set did not run its immediate collection")
	}

	m.Start(50 * time.Millisecond)
	// Wait until Start's loop has actually collected the preset before stopping,
	// so the assertion cannot race the goroutine's startup.
	select {
	case <-collected:
	case <-time.After(2 * time.Second):
		t.Fatal("Start did not run a collection round")
	}
	m.Stop()

	if calls.Load() < 2 {
		t.Errorf("Start did not run a collection round (calls=%d)", calls.Load())
	}
	all := m.All()
	met, ok := all["10.0.0.2"]
	if !ok {
		t.Fatalf("All() dropped the preset: %+v", all)
	}
	if !met.Online || met.CPUCores < 2 {
		t.Errorf("preset metrics = %+v (want Online + a Start-collected sample)", met)
	}
	if got, ok := m.Metrics("10.0.0.2"); !ok || !got.Online {
		t.Errorf("Metrics(address) = %+v (ok=%v)", got, ok)
	}
}
