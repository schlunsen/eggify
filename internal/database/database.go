// Package database provides SQLite database management for command history and conversation tracking.
// It implements a singleton pattern for database access and handles schema migrations,
// connection pooling, and WAL mode for concurrent access.
package database

import (
	"context"
	"database/sql"
	_ "embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync"

	sqlite3 "github.com/mattn/go-sqlite3"
)

// driverName is the mattn/go-sqlite3 driver registered with a per-connection
// hook for the pragmas the DSN cannot express.
const driverName = "sqlite3_eggify"

func init() {
	sql.Register(driverName, &sqlite3.SQLiteDriver{
		ConnectHook: func(conn *sqlite3.SQLiteConn) error {
			_, err := conn.Exec("PRAGMA temp_store = MEMORY", nil)
			return err
		},
	})
}

// dsnParams are applied by the driver to every connection it opens. Pragmas
// set with db.Exec only reach the one pooled connection that ran them, which
// left foreign_keys OFF (and ON DELETE CASCADE silently skipped) on the rest.
const dsnParams = "_foreign_keys=on&_journal_mode=WAL&_synchronous=NORMAL&_busy_timeout=5000&_cache_size=-64000"

// DSN returns the data source name used to open the eggify database at path.
// A plain path (not a file: URI) keeps paths with URI-special characters working.
func DSN(path string) string {
	return path + "?" + dsnParams
}

// vacuumAfterOrphanCleanup is the number of orphan rows the orphan cleanup
// migration must remove before the database is vacuumed to reclaim the space.
const vacuumAfterOrphanCleanup = 10000

//go:embed schema.sql
var schemaSQL string

// Database represents the SQLite database connection
type Database struct {
	db   *sql.DB
	path string
	mu   sync.RWMutex
}

var (
	instance *Database
	once     sync.Once
	mu       sync.Mutex
)

// Initialize creates and initializes the database
func Initialize(dataDir string) (*Database, error) {
	mu.Lock()
	defer mu.Unlock()

	// For testing: allow re-initialization if instance is nil
	if instance != nil {
		return instance, nil
	}

	var initErr error

	// Ensure data directory exists
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	dbPath := filepath.Join(dataDir, "eggify.db")

	// Check if database file exists before opening
	dbExists := false
	if _, err := os.Stat(dbPath); err == nil {
		dbExists = true
	}

	db, err := sql.Open(driverName, DSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Ping to force database file creation
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Set strict permissions on database file (0600 - user read/write only)
	// This ensures sensitive command history is only readable by the user
	if !dbExists {
		if err := os.Chmod(dbPath, 0600); err != nil {
			db.Close()
			return nil, fmt.Errorf("failed to set database file permissions: %w", err)
		}
	}

	// foreign_keys, journal_mode, synchronous, busy_timeout, cache_size and
	// temp_store are set per connection by the DSN and the driver hook.

	// Run schema migrations
	if _, err := db.Exec(schemaSQL); err != nil {
		initErr = fmt.Errorf("failed to execute schema: %w", err)
		db.Close()
		return nil, initErr
	}

	// Run additional migrations for existing databases
	if err := runMigrations(db); err != nil {
		initErr = fmt.Errorf("failed to run migrations: %w", err)
		db.Close()
		return nil, initErr
	}

	// Reclaim the space freed by a large orphan cleanup (VACUUM cannot run
	// inside the migration transaction).
	if orphanRowsDeleted > vacuumAfterOrphanCleanup {
		vacuumDatabase(db, dbPath)
	}

	// Run startup optimization for large databases
	if dbExists {
		if fileInfo, err := os.Stat(dbPath); err == nil {
			sizeMB := fileInfo.Size() / (1024 * 1024)
			if sizeMB > 100 {
				fmt.Printf("⚡ Database is %dMB, running WAL checkpoint...\n", sizeMB)
				db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
			}
		}
	}

	// Set permissions on WAL and SHM files created by SQLite (WAL mode)
	// These files may be created after opening the database
	walPath := dbPath + "-wal"
	shmPath := dbPath + "-shm"

	if _, err := os.Stat(walPath); err == nil {
		os.Chmod(walPath, 0600)
	}
	if _, err := os.Stat(shmPath); err == nil {
		os.Chmod(shmPath, 0600)
	}

	instance = &Database{
		db:   db,
		path: dbPath,
	}

	return instance, nil
}

// vacuumDatabase rebuilds the database file to return freed pages to the OS.
// Failures are logged, never fatal.
func vacuumDatabase(db *sql.DB, dbPath string) {
	before := fileSize(dbPath) + fileSize(dbPath+"-wal")
	log.Printf("Vacuuming database after removing %d orphaned rows (%d MB)...", orphanRowsDeleted, before/(1024*1024))

	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		log.Printf("Warning: vacuum skipped: %v", err)
		return
	}
	defer conn.Close()

	// Build the transient copy on disk, not in memory.
	if _, err := conn.ExecContext(ctx, "PRAGMA temp_store = FILE"); err != nil {
		log.Printf("Warning: vacuum skipped: %v", err)
		return
	}
	defer conn.ExecContext(ctx, "PRAGMA temp_store = MEMORY")

	if _, err := conn.ExecContext(ctx, "VACUUM"); err != nil {
		log.Printf("Warning: vacuum failed: %v", err)
		return
	}
	if _, err := conn.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		log.Printf("Warning: WAL checkpoint after vacuum failed: %v", err)
	}
	after := fileSize(dbPath) + fileSize(dbPath+"-wal")
	log.Printf("Vacuum complete: %d MB -> %d MB", before/(1024*1024), after/(1024*1024))
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// GetInstance returns the singleton database instance
func GetInstance() *Database {
	return instance
}

// Close closes the database connection
func (d *Database) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.db != nil {
		return d.db.Close()
	}
	return nil
}

// ResetInstance resets the singleton instance (for testing only)
func ResetInstance() {
	mu.Lock()
	defer mu.Unlock()

	if instance != nil {
		instance.Close()
		instance = nil
	}
	once = sync.Once{}
}

// GetDB returns the underlying sql.DB for direct access
func (d *Database) GetDB() *sql.DB {
	return d.db
}

// Path returns the database file path
func (d *Database) Path() string {
	return d.path
}

// HealthCheck verifies database connectivity
func (d *Database) HealthCheck() error {
	d.mu.RLock()
	defer d.mu.RUnlock()

	if d.db == nil {
		return fmt.Errorf("database not initialized")
	}

	return d.db.Ping()
}

// Stats returns database statistics
func (d *Database) Stats() (map[string]interface{}, error) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	stats := make(map[string]interface{})

	// Get table counts
	// Note: conversations table was dropped in later migration
	tables := []string{"shell_commands", "claude_commands", "command_stats"}
	for _, table := range tables {
		var count int
		query := fmt.Sprintf("SELECT COUNT(*) FROM %s", table)
		if err := d.db.QueryRow(query).Scan(&count); err != nil {
			return nil, fmt.Errorf("failed to get count for %s: %w", table, err)
		}
		stats[table+"_count"] = count
	}

	// Get database file size
	if fileInfo, err := os.Stat(d.path); err == nil {
		stats["db_size_bytes"] = fileInfo.Size()
	}

	return stats, nil
}

// Vacuum reclaims unused disk space by rebuilding the database file
func (d *Database) Vacuum() error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.db == nil {
		return fmt.Errorf("database not initialized")
	}

	// First, run VACUUM to rebuild the database
	_, err := d.db.Exec("VACUUM")
	if err != nil {
		return fmt.Errorf("failed to vacuum database: %w", err)
	}

	// Then checkpoint the WAL to ensure all data is written to main DB file
	_, err = d.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	if err != nil {
		return fmt.Errorf("failed to checkpoint WAL after vacuum: %w", err)
	}

	return nil
}
