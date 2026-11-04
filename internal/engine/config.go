package engine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
)

// configManager enforces the mandatory config transaction:
//
//  1. backup the current effective config (hash recorded)
//  2. write the candidate into a staging directory
//  3. invoke the KERNEL'S OWN validator on the candidate
//  4. only on success, atomically promote candidate -> effective config
//
// A validation failure leaves the effective config byte-for-byte untouched.
type configManager struct {
	registry *core.Registry
	root     string
	ports    core.PortAllocator
	versions core.VersionManager
	store    core.StateStore
	bus      core.EventBus
	mu       sync.Mutex // one config transaction at a time, all cores
}

var _ core.ConfigManager = (*configManager)(nil)

func newConfigManager(reg *core.Registry, root string, ports core.PortAllocator, versions core.VersionManager, st core.StateStore, bus core.EventBus) *configManager {
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	return &configManager{registry: reg, root: root, ports: ports, versions: versions, store: st, bus: bus}
}

func (c *configManager) coreDir(id contracts.CoreID) string {
	return filepath.Join(c.root, "cores", string(id))
}

func (c *configManager) Path(id contracts.CoreID) string {
	a, err := c.registry.Get(id)
	if err != nil {
		return ""
	}
	return filepath.Join(c.coreDir(id), a.ConfigFileName())
}

func (c *configManager) stagingPath(id contracts.CoreID) string {
	a, _ := c.registry.Get(id)
	return filepath.Join(c.coreDir(id), ".staging", a.ConfigFileName())
}

func (c *configManager) Read(id contracts.CoreID) ([]byte, error) {
	p := c.Path(id)
	if p == "" {
		return nil, fmt.Errorf("kernel %q is not registered", id)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read config of %q: %w", id, err)
	}
	return b, nil
}

// Ensure creates a valid Worker Mode baseline config if none exists.
func (c *configManager) Ensure(id contracts.CoreID) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	a, err := c.registry.Get(id)
	if err != nil {
		return err
	}
	path := c.Path(id)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	params, err := c.ports.Allocate(id)
	if err != nil {
		return err
	}
	params.Dir = c.coreDir(id)
	content, err := a.DefaultConfig(params)
	if err != nil {
		return fmt.Errorf("generate default config for %q: %w", id, err)
	}
	if err := os.MkdirAll(c.coreDir(id), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		return err
	}
	_ = c.store.AddAudit("config.create", id, fmt.Sprintf("generated Worker Mode baseline config (%d bytes)", len(content)))
	c.emit(id, "config.created", map[string]any{"path": path, "bytes": len(content)})
	return nil
}

// Validate runs the kernel's native validator on the supplied content without
// touching the effective config.
func (c *configManager) Validate(ctx context.Context, id contracts.CoreID, content []byte) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, err := c.registry.Get(id); err != nil {
		return "", err
	}
	stage := c.stagingPath(id)
	if err := os.MkdirAll(filepath.Dir(stage), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(stage, content, 0o600); err != nil {
		return "", err
	}
	defer os.Remove(stage)
	return c.nativeValidate(ctx, id, stage)
}

// Apply is the full backup -> validate -> promote transaction.
func (c *configManager) Apply(ctx context.Context, id contracts.CoreID, content []byte, reason string) (contracts.ConfigBackup, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	a, err := c.registry.Get(id)
	if err != nil {
		return contracts.ConfigBackup{}, err
	}
	if reason == "" {
		reason = "api.update"
	}
	if err := os.MkdirAll(filepath.Dir(c.stagingPath(id)), 0o755); err != nil {
		return contracts.ConfigBackup{}, err
	}

	// Step 1: backup the effective config first, unconditionally.
	backup, err := c.backupActive(id, reason)
	if err != nil {
		return contracts.ConfigBackup{}, fmt.Errorf("backup step failed, aborting config change: %w", err)
	}
	c.emit(id, "config.backup", backup)

	// Step 2: write candidate into staging (same filename as real config so
	// kernels that derive behavior from extension behave identically).
	stage := c.stagingPath(id)
	if err := os.WriteFile(stage, content, 0o600); err != nil {
		return contracts.ConfigBackup{}, err
	}
	defer os.Remove(stage)

	// Step 3: native validation. Any failure aborts; effective config is intact.
	output, err := c.nativeValidate(ctx, id, stage)
	if err != nil {
		_ = c.store.AddAudit("config.rejected", id, fmt.Sprintf("%s: %v", reason, err))
		return contracts.ConfigBackup{}, fmt.Errorf("原生配置校验未通过，已保留原配置 (备份 %s): %w", backup.ID, err)
	}

	// Step 4: promote.
	target := c.Path(id)
	if err := os.Rename(stage, target); err != nil {
		return contracts.ConfigBackup{}, fmt.Errorf("promote config: %w", err)
	}
	if err := c.store.AddBackup(backup); err != nil {
		return contracts.ConfigBackup{}, fmt.Errorf("record backup: %w", err)
	}
	_ = c.store.AddAudit("config.apply", id, fmt.Sprintf("%s: %d bytes, backup=%s, validator=%s", reason, len(content), backup.ID, truncate(output, 120)))
	c.emit(id, "config.applied", map[string]any{"backup_id": backup.ID, "bytes": len(content), "validator_output": truncate(output, 400)})
	_ = a
	return backup, nil
}

func (c *configManager) Backups(id contracts.CoreID) ([]contracts.ConfigBackup, error) {
	return c.store.ListBackups(id)
}

func (c *configManager) Rollback(id contracts.CoreID, backupID string) error {
	b, err := c.store.GetBackup(backupID)
	if err != nil {
		return err
	}
	if b.CoreID != id {
		return fmt.Errorf("backup %q belongs to kernel %q, not %q", backupID, b.CoreID, id)
	}
	content, err := os.ReadFile(b.Path)
	if err != nil {
		return fmt.Errorf("read backup file: %w", err)
	}
	if got := sha256Hex(content); !strings.EqualFold(got, b.SHA256) {
		return fmt.Errorf("backup integrity check failed for %q: recorded %s, actual %s", backupID, b.SHA256, got)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	_, err = c.Apply(ctx, id, content, "rollback:"+backupID)
	return err
}

func (c *configManager) backupActive(id contracts.CoreID, reason string) (contracts.ConfigBackup, error) {
	active := c.Path(id)
	content, err := os.ReadFile(active)
	if err != nil {
		if os.IsNotExist(err) {
			content = []byte{}
		} else {
			return contracts.ConfigBackup{}, err
		}
	}
	dir := filepath.Join(c.coreDir(id), "backups")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return contracts.ConfigBackup{}, err
	}
	stamp := time.Now().UTC().Format("20060102T150405.000000000")
	nonce := make([]byte, 4)
	_, _ = rand.Read(nonce)
	name := fmt.Sprintf("%s-%s%s", stamp, hex.EncodeToString(nonce), filepath.Ext(active))
	dest := filepath.Join(dir, name)
	if err := os.WriteFile(dest, content, 0o600); err != nil {
		return contracts.ConfigBackup{}, err
	}
	return contracts.ConfigBackup{
		ID:     fmt.Sprintf("%s-%s", id, stamp+"-"+hex.EncodeToString(nonce)),
		CoreID: id, Path: dest, SHA256: sha256Hex(content),
		Size: int64(len(content)), Reason: reason, CreatedAt: time.Now().UTC(),
	}, nil
}

// nativeValidate invokes the kernel's own config checker. Adapters may expose
// several real CLI dialects; the first invocation exiting 0 wins.
func (c *configManager) nativeValidate(ctx context.Context, id contracts.CoreID, configPath string) (string, error) {
	a, err := c.registry.Get(id)
	if err != nil {
		return "", err
	}
	binary := c.versions.BinaryPath(id)
	if _, err := os.Stat(binary); err != nil {
		return "", fmt.Errorf("kernel %q is not installed; cannot perform native config validation (Fail-Closed)", id)
	}
	var candidates [][]string
	if p, ok := a.(core.ValidateCandidateProvider); ok {
		candidates = p.ValidateCandidates(configPath)
	}
	if len(candidates) == 0 {
		candidates = [][]string{a.ValidateArgs(configPath)}
	}
	var combined []string
	for _, args := range candidates {
		runCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		cmd := exec.CommandContext(runCtx, binary, args...)
		cmd.Dir = filepath.Dir(configPath)
		out, runErr := cmd.CombinedOutput()
		cancel()
		line := fmt.Sprintf("$ %s %s\n%s", filepath.Base(binary), strings.Join(args, " "), strings.TrimSpace(string(out)))
		combined = append(combined, line)
		if runErr == nil {
			return strings.Join(combined, "\n"), nil
		}
		combined[len(combined)-1] = line + fmt.Sprintf("\n[exit: %v]", runErr)
	}
	return strings.Join(combined, "\n"), fmt.Errorf("all native validation invocations failed")
}

func (c *configManager) emit(id contracts.CoreID, kind string, payload any) {
	c.bus.Publish(contracts.Event{Type: "core.state", CoreID: id, Payload: map[string]any{"event": kind, "detail": payload}})
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
