package engine

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/ngg-project/ngg/internal/adapters/mihomo"
	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/internal/store"
	"github.com/ngg-project/ngg/pkg/contracts"
)

// TestConfigTransactionWithRealKernel exercises the full
// backup -> native validate -> promote chain against a REAL Mihomo binary.
// Set NGG_TEST_MIHOMO_BINARY to enable; without it the test is skipped rather
// than faking a successful validation.
func TestConfigTransactionWithRealKernel(t *testing.T) {
	src := os.Getenv("NGG_TEST_MIHOMO_BINARY")
	if src == "" {
		t.Skip("set NGG_TEST_MIHOMO_BINARY to a real mihomo binary to run this integration test")
	}
	root := t.TempDir()
	id := contracts.CoreMihomo
	a := mihomo.New()
	dest := filepath.Join(root, "cores", string(id), "bin", a.BinaryName(runtime.GOOS))
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		t.Fatal(err)
	}
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	out, err := os.Create(dest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	in.Close()
	out.Close()

	reg := core.NewRegistry()
	if err := reg.Register(a); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(filepath.Join(root, "ngg.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	bus := newEventBus()
	ports := newPortAllocator(37890, 10, "secret")
	versions := newVersionManager(reg, root, archDetector{}.Detect(), http.DefaultClient, bus, st)
	cm := newConfigManager(reg, root, ports, versions, st, bus)

	if err := cm.Ensure(id); err != nil {
		t.Fatal(err)
	}
	valid, err := cm.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	output, err := cm.Validate(ctx, id, valid)
	if err != nil {
		t.Fatalf("real native validation of generated config failed: %v\n%s", err, output)
	}

	backup, err := cm.Apply(ctx, id, valid, "unit-test")
	if err != nil {
		t.Fatalf("Apply on valid config failed: %v", err)
	}
	if backup.ID == "" || backup.SHA256 == "" {
		t.Fatal("backup metadata incomplete")
	}
	backups, err := cm.Backups(id)
	if err != nil || len(backups) != 1 {
		t.Fatalf("backup not recorded: %v %+v", err, backups)
	}

	invalid := []byte("this is not: [valid yaml\n  -\n")
	if _, err := cm.Apply(ctx, id, invalid, "should-be-rejected"); err == nil {
		t.Fatal("invalid config was accepted — Fail-Closed violated")
	}
	after, err := cm.Read(id)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(after, valid) {
		t.Fatal("effective config was modified after a failed validation")
	}

	if err := cm.Rollback(id, backup.ID); err != nil {
		t.Fatalf("rollback failed: %v", err)
	}
	restored, err := cm.Read(id)
	if err != nil || !bytes.Equal(restored, valid) {
		t.Fatalf("rollback did not restore the config: %v", err)
	}
}

// TestExtractRealZipBinary verifies archive extraction against the actual
// upstream Mihomo Windows release archive when NGG_TEST_ARCHIVE is provided.
func TestExtractRealZipBinary(t *testing.T) {
	zipPath := os.Getenv("NGG_TEST_ARCHIVE")
	if zipPath == "" {
		t.Skip("set NGG_TEST_ARCHIVE to a real release .zip to test extraction")
	}
	dst, err := extractBinary(zipPath, mihomo.New().Release(), t.TempDir(), "mihomo.exe", "windows")
	if err != nil {
		t.Fatalf("extract: %v", err)
	}
	st, err := os.Stat(dst)
	if err != nil || st.Size() < 1_000_000 {
		t.Fatalf("extracted binary looks wrong: %v %+v", err, st)
	}
}
