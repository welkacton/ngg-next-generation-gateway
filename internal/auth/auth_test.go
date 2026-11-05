package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s := NewStoreAt(filepath.Join(t.TempDir(), "auth.json"))
	if err := s.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	return s
}

func TestFreshInstallRequiresSetup(t *testing.T) {
	s := newTestStore(t)
	if !s.SetupRequired() {
		t.Fatal("a missing auth.json must require first-run setup")
	}
	if _, err := s.Login("admin", "password123", "", false); err != ErrSetupRequired {
		t.Fatalf("login before setup must return ErrSetupRequired, got %v", err)
	}
}

func TestSetupThenLogin(t *testing.T) {
	s := newTestStore(t)
	if err := s.Setup("admin", "s3cret-pass"); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if s.SetupRequired() {
		t.Fatal("setup must clear SetupRequired")
	}
	if err := s.Setup("other", "s3cret-pass"); err != ErrAlreadySetup {
		t.Fatalf("second setup must be rejected, got %v", err)
	}

	sess, err := s.Login("admin", "s3cret-pass", "test-agent", false)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	if sess.Token == "" || sess.Username != "admin" {
		t.Fatalf("bad session: %+v", sess)
	}
	if user, ok := s.Lookup(sess.Token); !ok || user != "admin" {
		t.Fatalf("lookup failed: %q %v", user, ok)
	}
	if _, err := s.Login("admin", "wrong-pass", "", false); err == nil {
		t.Fatal("wrong password must fail")
	}
	if _, err := s.Login("nobody", "s3cret-pass", "", false); err == nil {
		t.Fatal("unknown user must fail")
	}
}

func TestSessionIsPersistedHashedAndRevocable(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "auth.json")
	s := NewStoreAt(path)
	if err := s.Load(); err != nil {
		t.Fatal(err)
	}
	if err := s.Setup("admin", "s3cret-pass"); err != nil {
		t.Fatal(err)
	}
	sess, err := s.Login("admin", "s3cret-pass", "", true)
	if err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), sess.Token) {
		t.Fatal("the raw session token must never be written to disk")
	}

	// Restart: the same cookie must still work.
	reopened := NewStoreAt(path)
	if err := reopened.Load(); err != nil {
		t.Fatal(err)
	}
	if _, ok := reopened.Lookup(sess.Token); !ok {
		t.Fatal("session must survive a restart")
	}
	reopened.Logout(sess.Token)
	if _, ok := reopened.Lookup(sess.Token); ok {
		t.Fatal("logout must revoke the session")
	}
}

func TestExpiredSessionIsRejected(t *testing.T) {
	s := newTestStore(t)
	if err := s.Setup("admin", "s3cret-pass"); err != nil {
		t.Fatal(err)
	}
	sess, err := s.Login("admin", "s3cret-pass", "", false)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.data.Sessions[0].ExpiresAt = time.Now().Add(-time.Minute)
	s.mu.Unlock()
	if _, ok := s.Lookup(sess.Token); ok {
		t.Fatal("expired session must be rejected")
	}
}

func TestPasswordPolicyAndChange(t *testing.T) {
	s := newTestStore(t)
	if err := s.Setup("admin", "short"); err != ErrWeakPassword {
		t.Fatalf("short password must be rejected, got %v", err)
	}
	if err := s.Setup("a", "s3cret-pass"); err != ErrInvalidUsername {
		t.Fatalf("bad username must be rejected, got %v", err)
	}
	if err := s.Setup("admin", "s3cret-pass"); err != nil {
		t.Fatal(err)
	}
	sess, err := s.Login("admin", "s3cret-pass", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ChangePassword("nope", "new-pass-123"); err == nil {
		t.Fatal("wrong old password must fail")
	}
	if err := s.ChangePassword("s3cret-pass", "new-pass-123"); err != nil {
		t.Fatalf("change: %v", err)
	}
	if _, ok := s.Lookup(sess.Token); ok {
		t.Fatal("changing the password must revoke existing sessions")
	}
	if _, err := s.Login("admin", "s3cret-pass", "", false); err == nil {
		t.Fatal("old password must stop working")
	}
	if _, err := s.Login("admin", "new-pass-123", "", false); err != nil {
		t.Fatalf("new password must work: %v", err)
	}
}

func TestRepeatedFailuresLockTheAccount(t *testing.T) {
	s := newTestStore(t)
	if err := s.Setup("admin", "s3cret-pass"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxFailures; i++ {
		if _, err := s.Login("admin", "bad-pass", "", false); err == nil {
			t.Fatal("bad password must fail")
		}
	}
	_, err := s.Login("admin", "s3cret-pass", "", false)
	if err == nil {
		t.Fatal("the account must be locked after repeated failures")
	}
	if !strings.Contains(err.Error(), "连续登录失败") {
		t.Fatalf("unexpected lock message: %v", err)
	}
}
