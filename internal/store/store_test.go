package store

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ngg-project/ngg/pkg/contracts"
)

func TestStoreRoundTrip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "ngg.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if _, _, ok, err := s.GetDesired("mihomo"); err != nil || ok {
		t.Fatalf("unexpected initial desired state: ok=%v err=%v", ok, err)
	}
	if err := s.SetDesired("mihomo", contracts.StateRunning, true, "1.19.33"); err != nil {
		t.Fatal(err)
	}
	state, auto, ok, err := s.GetDesired("mihomo")
	if err != nil || !ok || state != contracts.StateRunning || !auto {
		t.Fatalf("state=%s auto=%v ok=%v err=%v", state, auto, ok, err)
	}
	if err := s.SetDesired("mihomo", contracts.StateStopped, false, "1.19.33"); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListDesired()
	if err != nil || len(list) != 1 || list["mihomo"].State != contracts.StateStopped {
		t.Fatalf("list=%+v err=%v", list, err)
	}

	b := contracts.ConfigBackup{
		ID: "bk-1", CoreID: "mihomo", Path: "/tmp/a.yaml", SHA256: "abc",
		Size: 42, Reason: "test", CreatedAt: time.Now().UTC(),
	}
	if err := s.AddBackup(b); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetBackup("bk-1")
	if err != nil || got.SHA256 != "abc" || got.CoreID != "mihomo" {
		t.Fatalf("backup=%+v err=%v", got, err)
	}
	all, err := s.ListBackups("mihomo")
	if err != nil || len(all) != 1 {
		t.Fatalf("backups=%+v err=%v", all, err)
	}
	if _, err := s.GetBackup("missing"); err == nil {
		t.Fatal("missing backup must error")
	}

	if err := s.RecordInstall("mihomo", "1.19.33", "/bin/mihomo", "deadbeef"); err != nil {
		t.Fatal(err)
	}
	ver, digest, ok, err := s.Installed("mihomo")
	if err != nil || !ok || ver != "1.19.33" || digest != "deadbeef" {
		t.Fatalf("installed=%s %s %v %v", ver, digest, ok, err)
	}
	if err := s.ClearInstall("mihomo"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok, _ := s.Installed("mihomo"); ok {
		t.Fatal("install record not cleared")
	}

	for _, action := range []string{"core.start", "core.stop", "config.apply"} {
		if err := s.AddAudit(action, "mihomo", "detail"); err != nil {
			t.Fatal(err)
		}
	}
	audit, err := s.Audit(10)
	if err != nil || len(audit) != 3 || audit[0].Action != "config.apply" {
		t.Fatalf("audit=%+v err=%v", audit, err)
	}
}
