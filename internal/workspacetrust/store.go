package workspacetrust

import (
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TrustedByGrandfathered marks folders trusted by the migration that
// introduced workspace trust: they already ran sessions with their config.
const TrustedByGrandfathered = "grandfathered"

// Record is one row of trusted_workspaces.
type Record struct {
	Path        string    `json:"path"`
	Fingerprint string    `json:"fingerprint"`
	TrustedAt   time.Time `json:"trusted_at"`
	TrustedBy   string    `json:"trusted_by"`
	// Snapshot is the inventory the user trusted (JSON), so the UI can show
	// what changed since.
	Snapshot string `json:"-"`
}

// CreateTableSQL creates the table (used by migration 51 and by tests).
const CreateTableSQL = `CREATE TABLE IF NOT EXISTS trusted_workspaces (
	path        TEXT PRIMARY KEY,
	fingerprint TEXT NOT NULL,
	trusted_at  DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
	trusted_by  TEXT NOT NULL DEFAULT '',
	snapshot    TEXT NOT NULL DEFAULT ''
)`

// execer is satisfied by *sql.DB and *sql.Tx.
type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

// Upsert trusts path (canonical) with the given fingerprint.
func Upsert(db execer, path, fingerprint, trustedBy string, inv *Inventory) error {
	snapshot := ""
	if inv != nil {
		if b, err := json.Marshal(inv); err == nil {
			snapshot = string(b)
		}
	}
	_, err := db.Exec(`INSERT INTO trusted_workspaces (path, fingerprint, trusted_at, trusted_by, snapshot)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET fingerprint = excluded.fingerprint, trusted_at = excluded.trusted_at,
			trusted_by = excluded.trusted_by, snapshot = excluded.snapshot`,
		path, fingerprint, time.Now().UTC(), trustedBy, snapshot)
	return err
}

// Status is the trust state of a folder.
type Status string

const (
	// StatusNoConfig: the folder has no trust-relevant config; sessions load
	// project settings (CLAUDE.md, commands) as before.
	StatusNoConfig Status = "no_config"
	// StatusTrusted: the config matches what the user trusted.
	StatusTrusted Status = "trusted"
	// StatusUntrusted: the folder has config and was never trusted.
	StatusUntrusted Status = "untrusted"
	// StatusChanged: the folder was trusted, but its config changed since.
	StatusChanged Status = "changed"
)

// Decision says how a session in a folder must run.
type Decision struct {
	Status    Status     `json:"status"`
	Path      string     `json:"path"`
	Inventory *Inventory `json:"inventory,omitempty"`
	Record    *Record    `json:"trusted,omitempty"`
}

// LoadProjectConfig reports whether the CLI may load the folder's project and
// local settings and its .mcp.json servers.
func (d Decision) LoadProjectConfig() bool {
	return d.Status == StatusNoConfig || d.Status == StatusTrusted
}

// Key identifies the config a CLI process was started with. When it changes
// between turns the process must be replaced.
func (d Decision) Key() string {
	fp := ""
	if d.Inventory != nil {
		fp = d.Inventory.Fingerprint
	}
	if d.LoadProjectConfig() {
		return "load:" + fp
	}
	return "skip:" + fp
}

// Checker decides trust for folders, caching scans by file stat so the check
// before every prompt costs three stat calls and one primary-key lookup.
type Checker struct {
	db   *sql.DB
	opts Options

	mu    sync.Mutex
	cache map[string]cachedScan
}

type cachedScan struct {
	sig string
	at  time.Time
	inv *Inventory
}

// NewChecker returns a checker backed by db (nil: nothing is trusted).
func NewChecker(db *sql.DB, opts Options) *Checker {
	return &Checker{db: db, opts: opts, cache: map[string]cachedScan{}}
}

// statSignature changes whenever one of the config files, or one of the
// script files and directories a previous scan read (watch), is created,
// removed or rewritten.
func statSignature(dir string, watch []string) string {
	sig := ""
	paths := make([]string, 0, len(ConfigFiles)+len(watch)+1)
	for _, rel := range ConfigFiles {
		paths = append(paths, filepath.Join(dir, rel))
	}
	paths = append(paths, filepath.Join(dir, HooksDir))
	paths = append(paths, watch...)
	for _, p := range paths {
		fi, err := os.Stat(p)
		if err != nil {
			sig += "-|"
			continue
		}
		sig += fi.ModTime().UTC().Format(time.RFC3339Nano) + ":" + itoa(fi.Size()) + ":" + fi.Mode().String() + "|"
	}
	return sig
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// scanTTL bounds how long a cached scan is trusted on its stat signature
// alone, in case a file was rewritten with the same size inside the
// filesystem's timestamp granularity.
const scanTTL = 10 * time.Second

// Scan returns the folder's inventory, reusing the cached one while no config
// file changed on disk.
func (c *Checker) Scan(dir string) (*Inventory, error) {
	canonical, err := Canonical(dir)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	hit, ok := c.cache[canonical]
	c.mu.Unlock()
	if ok && time.Since(hit.at) < scanTTL && hit.sig == statSignature(canonical, hit.inv.watch) {
		return hit.inv, nil
	}
	inv, err := Scan(canonical, c.opts)
	if err != nil {
		return nil, err
	}
	sig := statSignature(canonical, inv.watch)
	c.mu.Lock()
	c.cache[canonical] = cachedScan{sig: sig, at: time.Now(), inv: inv}
	c.mu.Unlock()
	return inv, nil
}

// Get returns the trust record for a canonical path, or nil.
func (c *Checker) Get(path string) (*Record, error) {
	if c == nil || c.db == nil {
		return nil, nil
	}
	var r Record
	err := c.db.QueryRow(`SELECT path, fingerprint, trusted_at, trusted_by, snapshot FROM trusted_workspaces WHERE path = ?`, path).
		Scan(&r.Path, &r.Fingerprint, &r.TrustedAt, &r.TrustedBy, &r.Snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Decide returns how a session in dir must run. Errors fail closed: a folder
// whose config cannot be read or whose trust cannot be looked up is treated
// as untrusted.
func (c *Checker) Decide(dir string) Decision {
	canonical, err := Canonical(dir)
	if err != nil {
		return Decision{Status: StatusNoConfig, Path: dir}
	}
	inv, err := c.Scan(canonical)
	if err != nil {
		return Decision{Status: StatusUntrusted, Path: canonical, Inventory: &Inventory{Path: canonical, Fingerprint: "unreadable"}}
	}
	d := Decision{Path: canonical, Inventory: inv}
	if !inv.HasConfig() {
		d.Status = StatusNoConfig
		return d
	}
	rec, err := c.Get(canonical)
	if err != nil || rec == nil {
		d.Status = StatusUntrusted
		return d
	}
	d.Record = rec
	switch {
	case rec.Fingerprint == inv.Fingerprint:
		d.Status = StatusTrusted
	case c.upgradeLegacy(rec, inv):
		d.Status = StatusTrusted
	default:
		d.Status = StatusChanged
	}
	return d
}

// upgradeLegacy re-fingerprints a folder trusted before scripts were part of
// the fingerprint. Its settings still match what the person trusted, and the
// scripts were never checked before, so their current content is trusted
// once (as it was implicitly before); from then on editing them asks again.
func (c *Checker) upgradeLegacy(rec *Record, inv *Inventory) bool {
	if c == nil || c.db == nil || strings.HasPrefix(rec.Fingerprint, FingerprintVersion) ||
		inv.LegacyFingerprint == "" || rec.Fingerprint != inv.LegacyFingerprint {
		return false
	}
	if err := Upsert(c.db, inv.Path, inv.Fingerprint, rec.TrustedBy, inv); err != nil {
		return false
	}
	rec.Fingerprint = inv.Fingerprint
	return true
}

// Trust records that a person trusted dir's config with the fingerprint they
// reviewed. It fails with ErrFingerprintMismatch when the config changed after
// they looked at it, so nobody trusts something they did not see.
func (c *Checker) Trust(dir, reviewedFingerprint, trustedBy string) (Decision, error) {
	if c == nil || c.db == nil {
		return Decision{}, errors.New("workspace trust store unavailable")
	}
	// Always compare against the files as they are now, not a cached scan.
	if canonical, err := Canonical(dir); err == nil {
		c.forget(canonical)
	}
	d := c.Decide(dir)
	if d.Inventory == nil || !d.Inventory.HasConfig() {
		return d, ErrNothingToTrust
	}
	if reviewedFingerprint != d.Inventory.Fingerprint {
		return d, ErrFingerprintMismatch
	}
	if err := Upsert(c.db, d.Path, d.Inventory.Fingerprint, trustedBy, d.Inventory); err != nil {
		return d, err
	}
	return c.Decide(dir), nil
}

// WithOwnWrite runs write, a change Eggify itself makes to dir's config on a
// person's behalf (e.g. removing a legacy always-allow entry). If the folder
// was trusted with its current config before the write, the new fingerprint
// is trusted too, so Eggify's own edits never ask again. A folder that was not
// trusted (or whose config had already drifted) stays as it was.
func (c *Checker) WithOwnWrite(dir string, write func() error) error {
	if c == nil {
		return write()
	}
	before := c.Decide(dir)
	if err := write(); err != nil {
		return err
	}
	if before.Status != StatusTrusted || before.Record == nil {
		return nil
	}
	c.forget(before.Path)
	after := c.Decide(dir)
	if after.Inventory == nil || !after.Inventory.HasConfig() || after.Status == StatusTrusted {
		return nil
	}
	return Upsert(c.db, after.Path, after.Inventory.Fingerprint, before.Record.TrustedBy, after.Inventory)
}

// Invalidate drops the cached scan of dir, so the next check reads the files.
func (c *Checker) Invalidate(dir string) {
	if canonical, err := Canonical(dir); err == nil {
		c.forget(canonical)
	}
}

func (c *Checker) forget(path string) {
	c.mu.Lock()
	delete(c.cache, path)
	c.mu.Unlock()
}

// Revoke removes trust for dir.
func (c *Checker) Revoke(dir string) error {
	if c == nil || c.db == nil {
		return errors.New("workspace trust store unavailable")
	}
	canonical, err := Canonical(dir)
	if err != nil {
		return err
	}
	_, err = c.db.Exec(`DELETE FROM trusted_workspaces WHERE path = ?`, canonical)
	return err
}

// Errors returned by Trust.
var (
	ErrFingerprintMismatch = errors.New("the folder's config changed since it was reviewed; review it again")
	ErrNothingToTrust      = errors.New("the folder has no Claude config to trust")
)
