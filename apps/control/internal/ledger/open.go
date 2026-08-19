// Package ledger owns Qianshou's central SQLite ledger. No Runner process may
// open this database directly.
package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

const (
	DatabaseFilename = "qianshou.db"
	BackupDirectory  = "backups"
	busyTimeoutMS    = 5000
)

type Store struct {
	db     *sql.DB
	home   string
	dbPath string
	lock   *os.File
}

func Open(ctx context.Context, home string) (*Store, error) {
	return openWithMigrations(ctx, home, migrations())
}

func openWithMigrations(ctx context.Context, home string, all []migration) (_ *Store, returnedErr error) {
	if err := validateMigrations(all); err != nil {
		return nil, err
	}
	home = filepath.Clean(strings.TrimSpace(home))
	if home == "." || !filepath.IsAbs(home) {
		return nil, fmt.Errorf("Qianshou home must be an absolute path")
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return nil, fmt.Errorf("create Qianshou home: %w", err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		return nil, fmt.Errorf("secure Qianshou home: %w", err)
	}
	lock, err := acquireOwnershipLock(home)
	if err != nil {
		return nil, err
	}
	defer func() {
		if returnedErr != nil {
			releaseOwnershipLock(lock)
		}
	}()

	dbPath := filepath.Join(home, DatabaseFilename)
	info, statErr := os.Stat(dbPath)
	newDatabase := errors.Is(statErr, os.ErrNotExist) || (statErr == nil && info.Size() == 0)
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return nil, fmt.Errorf("inspect SQLite ledger: %w", statErr)
	}

	db, err := sql.Open("sqlite", sqliteDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("open SQLite ledger: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	store := &Store{db: db, home: home, dbPath: dbPath, lock: lock}
	defer func() {
		if returnedErr != nil {
			_ = db.Close()
			if newDatabase {
				for _, path := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
					_ = os.Remove(path)
				}
			}
		}
	}()

	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("open SQLite ledger: %w", err)
	}
	if err := store.secureFiles(); err != nil {
		return nil, err
	}
	if err := verifyPragmas(ctx, db); err != nil {
		return nil, err
	}
	if err := migrate(ctx, store, all, newDatabase); err != nil {
		return nil, err
	}
	if err := integrityCheck(ctx, db); err != nil {
		return nil, fmt.Errorf("verify SQLite ledger after migration: %w", err)
	}
	if !newDatabase {
		hasRuns, err := tableExists(ctx, db, "agent_runs")
		if err != nil {
			return nil, fmt.Errorf("inspect run recovery schema: %w", err)
		}
		if hasRuns {
			if err := store.interruptOrphanedRuns(ctx); err != nil {
				return nil, err
			}
		}
	}
	if err := store.secureFiles(); err != nil {
		return nil, err
	}
	return store, nil
}

func sqliteDSN(path string) string {
	u := &url.URL{Scheme: "file", Path: path}
	query := u.Query()
	query.Add("_pragma", "foreign_keys(1)")
	query.Add("_pragma", "journal_mode(WAL)")
	query.Add("_pragma", "synchronous(FULL)")
	query.Add("_pragma", fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS))
	u.RawQuery = query.Encode()
	return u.String()
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) Close() error {
	dbErr := s.db.Close()
	releaseOwnershipLock(s.lock)
	s.lock = nil
	return dbErr
}

func acquireOwnershipLock(home string) (*os.File, error) {
	path := filepath.Join(home, "qianshou.lock")
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open central ledger ownership lock: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		file.Close()
		return nil, fmt.Errorf("secure central ledger ownership lock: %w", err)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("central SQLite ledger is already owned by another server process")
	}
	return file, nil
}

func releaseOwnershipLock(file *os.File) {
	if file == nil {
		return
	}
	_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
	_ = file.Close()
}

func (s *Store) secureFiles() error {
	for _, path := range []string{s.dbPath, s.dbPath + "-wal", s.dbPath + "-shm"} {
		if err := os.Chmod(path, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("secure SQLite file %s: %w", filepath.Base(path), err)
		}
	}
	return nil
}

func verifyPragmas(ctx context.Context, db *sql.DB) error {
	checks := []struct {
		name string
		want string
	}{
		{"foreign_keys", "1"},
		{"journal_mode", "wal"},
		{"synchronous", "2"},
		{"busy_timeout", fmt.Sprint(busyTimeoutMS)},
	}
	for _, check := range checks {
		var got string
		if err := db.QueryRowContext(ctx, "PRAGMA "+check.name).Scan(&got); err != nil {
			return fmt.Errorf("verify PRAGMA %s: %w", check.name, err)
		}
		if strings.ToLower(got) != check.want {
			return fmt.Errorf("unsafe SQLite PRAGMA %s=%q, want %q", check.name, got, check.want)
		}
	}
	return nil
}

func migrate(ctx context.Context, store *Store, all []migration, newDatabase bool) error {
	hasMetadata, err := tableExists(ctx, store.db, "schema_migrations")
	if err != nil {
		return fmt.Errorf("inspect migration metadata: %w", err)
	}
	if !hasMetadata && !newDatabase {
		return fmt.Errorf("existing database is missing schema_migrations")
	}

	applied := []appliedMigration{}
	if hasMetadata {
		applied, err = readAppliedMigrations(ctx, store.db)
		if err != nil {
			return err
		}
		if err := validateAppliedMigrations(applied, all); err != nil {
			return err
		}
		if !newDatabase && len(applied) == 0 {
			return fmt.Errorf("existing database migration history is missing version 1")
		}
	}
	if len(applied) == len(all) {
		return nil
	}
	if len(applied) > 0 {
		if _, err := createBackup(ctx, store, len(applied)+1); err != nil {
			return fmt.Errorf("create pre-migration backup: %w", err)
		}
	}

	conn, err := store.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserve migration connection: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_, _ = conn.ExecContext(context.Background(), "ROLLBACK")
		}
	}()

	if !hasMetadata {
		if _, err := conn.ExecContext(ctx, `
			CREATE TABLE schema_migrations (
				version INTEGER PRIMARY KEY CHECK (version > 0),
				name TEXT NOT NULL CHECK (length(name) > 0),
				checksum TEXT NOT NULL CHECK (length(checksum) = 64),
				applied_at TEXT NOT NULL CHECK (length(applied_at) > 0)
			) STRICT`); err != nil {
			return fmt.Errorf("create migration metadata: %w", err)
		}
	}
	for _, item := range all[len(applied):] {
		if _, err := conn.ExecContext(ctx, item.SQL); err != nil {
			return fmt.Errorf("apply migration %d %s: %w", item.Version, item.Name, err)
		}
		if _, err := conn.ExecContext(ctx,
			`INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES(?, ?, ?, ?)`,
			item.Version, item.Name, migrationChecksum(item.SQL), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return fmt.Errorf("record migration %d: %w", item.Version, err)
		}
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	committed = true
	return nil
}

type appliedMigration struct {
	Version  int
	Name     string
	Checksum string
}

func readAppliedMigrations(ctx context.Context, db *sql.DB) ([]appliedMigration, error) {
	rows, err := db.QueryContext(ctx, `SELECT version, name, checksum FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read migration metadata: %w", err)
	}
	defer rows.Close()
	var result []appliedMigration
	for rows.Next() {
		var item appliedMigration
		if err := rows.Scan(&item.Version, &item.Name, &item.Checksum); err != nil {
			return nil, fmt.Errorf("read migration metadata: %w", err)
		}
		result = append(result, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read migration metadata: %w", err)
	}
	return result, nil
}

func validateAppliedMigrations(applied []appliedMigration, all []migration) error {
	for _, item := range applied {
		if item.Version > len(all) {
			return fmt.Errorf("database schema is newer than this binary: version %d", item.Version)
		}
	}
	for i, item := range applied {
		wantVersion := i + 1
		if item.Version != wantVersion {
			return fmt.Errorf("database migration history has a missing or duplicate version at %d", wantVersion)
		}
		want := all[i]
		if item.Name != want.Name || item.Checksum != migrationChecksum(want.SQL) {
			return fmt.Errorf("migration %d name or checksum drift", item.Version)
		}
	}
	return nil
}

func tableExists(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var count int
	err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = ?`, name).Scan(&count)
	return count == 1, err
}

func createBackup(ctx context.Context, store *Store, nextVersion int) (string, error) {
	directory := filepath.Join(store.home, BackupDirectory)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		return "", err
	}
	name := fmt.Sprintf("qianshou-before-v%d-%s.db", nextVersion, time.Now().UTC().Format("20060102T150405.000000000Z"))
	path := filepath.Join(directory, name)
	if _, err := store.db.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return "", err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return "", err
	}
	backupURL := &url.URL{Scheme: "file", Path: path}
	backupQuery := backupURL.Query()
	backupQuery.Set("mode", "ro")
	backupURL.RawQuery = backupQuery.Encode()
	backup, err := sql.Open("sqlite", backupURL.String())
	if err != nil {
		return "", err
	}
	defer backup.Close()
	if err := integrityCheck(ctx, backup); err != nil {
		return "", fmt.Errorf("verify backup: %w", err)
	}
	return path, nil
}

func integrityCheck(ctx context.Context, db *sql.DB) error {
	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return err
	}
	if strings.ToLower(result) != "ok" {
		return fmt.Errorf("integrity_check returned %q", result)
	}
	return nil
}
