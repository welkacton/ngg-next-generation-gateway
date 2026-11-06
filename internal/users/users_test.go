package users

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newReg(t *testing.T) *Registry {
	t.Helper()
	r := NewRegistry(filepath.Join(t.TempDir(), "users-config.json"))
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestStatusIsDerivedFromRealFields(t *testing.T) {
	now := time.Date(2026, 11, 6, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name string
		u    User
		want Status
	}{
		{"disabled wins", User{Enabled: false, ExpiresAt: now.Add(time.Hour)}, StatusDisabled},
		{"expired", User{Enabled: true, ExpiresAt: now.Add(-time.Second)}, StatusExpired},
		{"limited", User{Enabled: true, DataLimit: 100, UsedBytes: 100}, StatusLimited},
		{"active", User{Enabled: true, DataLimit: 100, UsedBytes: 99}, StatusActive},
		{"never expires", User{Enabled: true, ExpiresAt: time.Time{}}, StatusActive},
	}
	for _, tc := range cases {
		if got := tc.u.Status(now); got != tc.want {
			t.Fatalf("%s: got %s want %s", tc.name, got, tc.want)
		}
	}
}

func TestCRUDAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users-config.json")
	r := NewRegistry(path)
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	u := User{Username: "US-01", Enabled: true, DataLimit: 1024, Group: "Default-Squad"}
	saved, err := r.Add(u)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if saved.ID == "" || saved.ResetStrategy != ResetNever {
		t.Fatalf("defaults not applied: %+v", saved)
	}
	if _, err := r.Add(User{Username: "us-01"}); err == nil {
		t.Fatal("duplicate username must be rejected (case-insensitive)")
	}
	if _, err := r.Add(User{Username: "   "}); err == nil {
		t.Fatal("empty username must be rejected")
	}

	if err := r.AddUsage(saved.ID, 700); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Get(saved.ID)
	if got.UsedBytes != 700 || got.Status(time.Now().UTC()) != StatusActive {
		t.Fatalf("usage not accumulated: %+v", got)
	}
	if err := r.AddUsage(saved.ID, 400); err != nil {
		t.Fatal(err)
	}
	got, _ = r.Get(saved.ID)
	if got.Status(time.Now().UTC()) != StatusLimited {
		t.Fatalf("quota must flip status to limited: %+v", got)
	}
	if got.Remaining() != 0 {
		t.Fatalf("remaining = %d", got.Remaining())
	}

	reset, err := r.ResetUsage(saved.ID)
	if err != nil || reset.UsedBytes != 0 {
		t.Fatalf("reset: %v %+v", err, reset)
	}

	// Reload from disk: the record must survive a daemon restart.
	r2 := NewRegistry(path)
	if err := r2.Load(); err != nil {
		t.Fatal(err)
	}
	if _, ok := r2.Get(saved.ID); !ok {
		t.Fatal("user lost after reload")
	}
	if err := r2.Remove(saved.ID); err != nil {
		t.Fatal(err)
	}
	if err := r2.Remove(saved.ID); err == nil {
		t.Fatal("removing a missing user must fail")
	}
}

func TestStatsAndStatusFilter(t *testing.T) {
	r := newReg(t)
	now := time.Now().UTC()
	for _, u := range []User{
		{Username: "a", Enabled: true},
		{Username: "b", Enabled: false},
		{Username: "c", Enabled: true, ExpiresAt: now.Add(-time.Hour)},
		{Username: "d", Enabled: true, DataLimit: 10, UsedBytes: 10},
	} {
		if _, err := r.Add(u); err != nil {
			t.Fatal(err)
		}
	}
	s := r.Stats()
	if s.Total != 4 || s.Active != 1 || s.Disabled != 1 || s.Expired != 1 || s.Limited != 1 {
		t.Fatalf("stats wrong: %+v", s)
	}
	if n := len(r.List("active")); n != 1 {
		t.Fatalf("active filter = %d", n)
	}
	if n := len(r.List("all")); n != 4 {
		t.Fatalf("all filter = %d", n)
	}
}

// ---------------------------------------------------------------- *
// Subscription binding
// ---------------------------------------------------------------- *

func TestSubscriptionBindingPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users-config.json")
	r := NewRegistry(path)
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	saved, err := r.Add(User{Username: "US-01", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.SetSubscription(saved.ID, "us01", "trojan", "gateway", []string{"ads", "ads", " geosite "})
	if err != nil {
		t.Fatalf("SetSubscription: %v", err)
	}
	if got.NodeID != "us01" || got.Protocol != ProtocolTrojan || got.DNSProfile != DNSProfileGateway {
		t.Fatalf("binding not applied: %+v", got)
	}
	if len(got.RuleSetIDs) != 2 || got.RuleSetIDs[0] != "ads" || got.RuleSetIDs[1] != "geosite" {
		t.Fatalf("rule set ids must be trimmed and de-duplicated: %v", got.RuleSetIDs)
	}

	// Reload: the binding is part of the user record and must survive a restart.
	r2 := NewRegistry(path)
	if err := r2.Load(); err != nil {
		t.Fatal(err)
	}
	back, ok := r2.Get(saved.ID)
	if !ok {
		t.Fatal("user lost after reload")
	}
	if back.NodeID != "us01" || back.Protocol != ProtocolTrojan || back.DNSProfile != DNSProfileGateway || len(back.RuleSetIDs) != 2 {
		t.Fatalf("binding lost after reload: %+v", back)
	}

	// The generic PUT from the UI does not know about the binding and must not
	// wipe it.
	back.NodeID, back.Protocol, back.DNSProfile, back.RuleSetIDs = "", "", "", nil
	put, err := r2.Update(saved.ID, back)
	if err != nil {
		t.Fatal(err)
	}
	if put.NodeID != "us01" || put.Protocol != ProtocolTrojan || put.DNSProfile != DNSProfileGateway || len(put.RuleSetIDs) != 2 {
		t.Fatalf("generic update wiped the binding: %+v", put)
	}
}

func TestSubscriptionRejectsUnknownValues(t *testing.T) {
	r := newReg(t)
	u, err := r.Add(User{Username: "US-01", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.SetSubscription(u.ID, "h", "wireguard", "system", nil); !errors.Is(err, ErrInvalidSubscription) {
		t.Fatalf("unsupported protocol must be rejected, got %v", err)
	}
	if _, err := r.SetSubscription(u.ID, "h", "vless", "quad9", nil); !errors.Is(err, ErrInvalidSubscription) {
		t.Fatalf("unsupported dns profile must be rejected, got %v", err)
	}
	if _, err := r.SetSubscription("usr_missing", "h", "vless", "system", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user must be ErrNotFound, got %v", err)
	}
	if got, _ := r.Get(u.ID); got.Protocol != "" || got.NodeID != "" {
		t.Fatalf("rejected writes must not touch the record: %+v", got)
	}
}

func TestSubscriptionTokenLifecycle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users-config.json")
	r := NewRegistry(path)
	if err := r.Load(); err != nil {
		t.Fatal(err)
	}
	u, err := r.Add(User{Username: "US-01", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	first, err := r.EnsureSubscriptionToken(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.SubscriptionToken) != 43 {
		t.Fatalf("token must be 32 bytes of base64url (43 chars), got %q", first.SubscriptionToken)
	}
	if first.SubscriptionAt.IsZero() {
		t.Fatal("subscription_at must be stamped on first issue")
	}
	// Idempotent: a second call must return the same token.
	again, err := r.EnsureSubscriptionToken(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.SubscriptionToken != first.SubscriptionToken {
		t.Fatalf("EnsureSubscriptionToken must not rotate: %q != %q", again.SubscriptionToken, first.SubscriptionToken)
	}

	// Tokens live in the sidecar, never in users.json.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), first.SubscriptionToken) {
		t.Fatal("users.json must not contain the subscription token")
	}

	// Reload: the token survives and still resolves.
	r2 := NewRegistry(path)
	if err := r2.Load(); err != nil {
		t.Fatal(err)
	}
	if got, ok := r2.UserBySubscriptionToken(first.SubscriptionToken); !ok || got.ID != u.ID {
		t.Fatalf("token must resolve after reload: %v %+v", ok, got)
	}
	if _, ok := r2.Get(u.ID); !ok {
		t.Fatal("user lost after reload")
	}
	if reloaded, _ := r2.Get(u.ID); reloaded.SubscriptionToken != first.SubscriptionToken {
		t.Fatalf("token not reattached on Get: %q", reloaded.SubscriptionToken)
	}

	rotated, err := r2.RotateSubscriptionToken(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if rotated.SubscriptionToken == first.SubscriptionToken {
		t.Fatal("rotate must mint a different token")
	}
	if _, ok := r2.UserBySubscriptionToken(first.SubscriptionToken); ok {
		t.Fatal("the rotated-away token must stop resolving immediately")
	}
	if got, ok := r2.UserBySubscriptionToken(rotated.SubscriptionToken); !ok || got.ID != u.ID {
		t.Fatal("the new token must resolve")
	}
}

func TestSubscriptionTokenOfDisabledUserStillResolves(t *testing.T) {
	r := newReg(t)
	u, err := r.Add(User{Username: "US-01", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	withToken, err := r.EnsureSubscriptionToken(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := r.UserBySubscriptionToken(withToken.SubscriptionToken)
	if !ok {
		t.Fatal("a disabled user must still be found by token; the renderer decides usability")
	}
	if got.SubscriptionUsable(time.Now().UTC()) {
		t.Fatal("a disabled account must not be usable for subscriptions")
	}

	active, err := r.Add(User{Username: "US-02", Enabled: true, ExpiresAt: time.Now().UTC().Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	if active.SubscriptionUsable(time.Now().UTC()) {
		t.Fatal("an expired account must not be usable for subscriptions")
	}
	active.ExpiresAt = time.Now().UTC().Add(time.Hour)
	if !active.SubscriptionUsable(time.Now().UTC()) {
		t.Fatal("an enabled, unexpired account must be usable")
	}
	if _, ok := r.UserBySubscriptionToken("not-a-real-token"); ok {
		t.Fatal("an unknown token must not resolve")
	}
}

func TestRemoveDropsSubscriptionToken(t *testing.T) {
	r := newReg(t)
	u, err := r.Add(User{Username: "US-01", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	withToken, err := r.EnsureSubscriptionToken(u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Remove(u.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := r.UserBySubscriptionToken(withToken.SubscriptionToken); ok {
		t.Fatal("a removed user's token must stop resolving")
	}
}

func TestLegacyRecordLoadsWithZeroBinding(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "users-config.json")
	// Exactly the pre-subscription shape: no node_id / protocol / rule_set_ids
	// / dns_profile / subscription_at keys at all.
	legacy := `[{"id":"usr_legacy","username":"US-LEGACY","enabled":true,"reset_strategy":"never"}]`
	if err := os.WriteFile(path, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(path)
	if err := r.Load(); err != nil {
		t.Fatalf("legacy users.json must load: %v", err)
	}
	u, ok := r.Get("usr_legacy")
	if !ok {
		t.Fatal("legacy user lost")
	}
	if u.NodeID != "" || u.Protocol != "" || len(u.RuleSetIDs) != 0 || u.SubscriptionToken != "" {
		t.Fatalf("legacy binding must stay empty: %+v", u)
	}
	if u.DNSProfile != DNSProfileSystem {
		t.Fatalf("empty dns_profile must normalise to system, got %q", u.DNSProfile)
	}
}
