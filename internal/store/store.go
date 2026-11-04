// Package store persists desired kernel state, config backup metadata and an
// audit trail in SQLite. Pure-Go driver (modernc.org/sqlite) keeps CGO out of
// the build so cross-compiling to Linux ARM64 / OpenWrt stays trivial.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ngg-project/ngg/internal/core"
	"github.com/ngg-project/ngg/pkg/contracts"
	_ "modernc.org/sqlite"
)

type Store struct{ db *sql.DB }

var _ core.StateStore = (*Store)(nil)

func Open(dbPath string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1) // SQLite single writer; avoids SQLITE_BUSY under parallel core ops
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA foreign_keys=ON",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	const ddl = `
CREATE TABLE IF NOT EXISTS desired_state (
  core_id TEXT PRIMARY KEY,
  state TEXT NOT NULL,
  auto_start INTEGER NOT NULL DEFAULT 0,
  version TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS config_backups (
  id TEXT PRIMARY KEY,
  core_id TEXT NOT NULL,
  path TEXT NOT NULL,
  sha256 TEXT NOT NULL,
  size INTEGER NOT NULL,
  reason TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_backups_core ON config_backups(core_id, created_at DESC);
CREATE TABLE IF NOT EXISTS installed (
  core_id TEXT PRIMARY KEY,
  version TEXT NOT NULL,
  binary_path TEXT NOT NULL,
  digest TEXT NOT NULL DEFAULT '',
  installed_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS audit_log (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  action TEXT NOT NULL,
  core_id TEXT NOT NULL,
  detail TEXT NOT NULL,
  created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_audit_time ON audit_log(created_at DESC);
`
	_, err := s.db.Exec(ddl)
	return err
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) GetDesired(id contracts.CoreID) (contracts.CoreState, bool, bool, error) {
	var state string
	var auto int
	err := s.db.QueryRow(`SELECT state, auto_start FROM desired_state WHERE core_id=?`, string(id)).Scan(&state, &auto)
	if err == sql.ErrNoRows {
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, err
	}
	return contracts.CoreState(state), auto != 0, true, nil
}

func (s *Store) SetDesired(id contracts.CoreID, state contracts.CoreState, autoStart bool, version string) error {
	_, err := s.db.Exec(`INSERT INTO desired_state(core_id,state,auto_start,version,updated_at)
VALUES(?,?,?,?,?) ON CONFLICT(core_id) DO UPDATE SET state=excluded.state,
auto_start=excluded.auto_start, version=excluded.version, updated_at=excluded.updated_at`,
		string(id), string(state), boolInt(autoStart), version, now())
	return err
}

func (s *Store) ListDesired() (map[contracts.CoreID]core.DesiredRecord, error) {
	rows, err := s.db.Query(`SELECT core_id,state,auto_start,version,updated_at FROM desired_state`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[contracts.CoreID]core.DesiredRecord{}
	for rows.Next() {
		var id, st, ver, ts string
		var auto int
		if err := rows.Scan(&id, &st, &auto, &ver, &ts); err != nil {
			return nil, err
		}
		cid := contracts.CoreID(id)
		out[cid] = core.DesiredRecord{
			CoreID: cid, State: contracts.CoreState(st), AutoStart: auto != 0,
			Version: ver, UpdatedAt: parseTime(ts),
		}
	}
	return out, rows.Err()
}

func (s *Store) AddBackup(b contracts.ConfigBackup) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO config_backups(id,core_id,path,sha256,size,reason,created_at)
VALUES(?,?,?,?,?,?,?)`, b.ID, string(b.CoreID), b.Path, b.SHA256, b.Size, b.Reason, b.CreatedAt.Format(time.RFC3339Nano))
	return err
}

func (s *Store) ListBackups(id contracts.CoreID) ([]contracts.ConfigBackup, error) {
	q := `SELECT id,core_id,path,sha256,size,reason,created_at FROM config_backups`
	args := []any{}
	if id != "" {
		q += ` WHERE core_id=?`
		args = append(args, string(id))
	}
	q += ` ORDER BY created_at DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contracts.ConfigBackup
	for rows.Next() {
		var b contracts.ConfigBackup
		var cid, ts string
		if err := rows.Scan(&b.ID, &cid, &b.Path, &b.SHA256, &b.Size, &b.Reason, &ts); err != nil {
			return nil, err
		}
		b.CoreID = contracts.CoreID(cid)
		b.CreatedAt = parseTime(ts)
		out = append(out, b)
	}
	if out == nil {
		out = []contracts.ConfigBackup{}
	}
	return out, rows.Err()
}

func (s *Store) GetBackup(id string) (contracts.ConfigBackup, error) {
	var b contracts.ConfigBackup
	var cid, ts string
	err := s.db.QueryRow(`SELECT id,core_id,path,sha256,size,reason,created_at FROM config_backups WHERE id=?`, id).
		Scan(&b.ID, &cid, &b.Path, &b.SHA256, &b.Size, &b.Reason, &ts)
	if err == sql.ErrNoRows {
		return b, fmt.Errorf("backup %q not found", id)
	}
	b.CoreID = contracts.CoreID(cid)
	b.CreatedAt = parseTime(ts)
	return b, err
}

func (s *Store) RecordInstall(id contracts.CoreID, version, binaryPath, digest string) error {
	_, err := s.db.Exec(`INSERT INTO installed(core_id,version,binary_path,digest,installed_at)
VALUES(?,?,?,?,?) ON CONFLICT(core_id) DO UPDATE SET version=excluded.version,
binary_path=excluded.binary_path, digest=excluded.digest, installed_at=excluded.installed_at`,
		string(id), version, binaryPath, digest, now())
	return err
}

func (s *Store) ClearInstall(id contracts.CoreID) error {
	_, err := s.db.Exec(`DELETE FROM installed WHERE core_id=?`, string(id))
	return err
}

func (s *Store) Installed(id contracts.CoreID) (version, digest string, ok bool, err error) {
	err = s.db.QueryRow(`SELECT version,digest FROM installed WHERE core_id=?`, string(id)).Scan(&version, &digest)
	if err == sql.ErrNoRows {
		return "", "", false, nil
	}
	return version, digest, err == nil, err
}

func (s *Store) AddAudit(action string, id contracts.CoreID, detail string) error {
	_, err := s.db.Exec(`INSERT INTO audit_log(action,core_id,detail,created_at) VALUES(?,?,?,?)`,
		action, string(id), detail, now())
	return err
}

func (s *Store) Audit(limit int) ([]core.AuditRecord, error) {
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	rows, err := s.db.Query(`SELECT id,action,core_id,detail,created_at FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.AuditRecord
	for rows.Next() {
		var r core.AuditRecord
		var cid, ts string
		if err := rows.Scan(&r.ID, &r.Action, &cid, &r.Detail, &ts); err != nil {
			return nil, err
		}
		r.CoreID = contracts.CoreID(cid)
		r.CreatedAt = parseTime(ts)
		out = append(out, r)
	}
	if out == nil {
		out = []core.AuditRecord{}
	}
	return out, rows.Err()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
func now() string { return time.Now().UTC().Format(time.RFC3339Nano) }
func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, s)
	return t
}
