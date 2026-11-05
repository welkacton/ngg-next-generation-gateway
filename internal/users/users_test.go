package users

import (
	"path/filepath"
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
