package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// RestoreBackup replaces the stopped central ledger with a verified backup.
// It is an explicit offline recovery operation, never an automatic down
// migration.
func RestoreBackup(ctx context.Context, home, backupPath string) error {
	return restoreBackup(ctx, home, backupPath, migrations())
}

func restoreBackup(ctx context.Context, home, backupPath string, compatible []migration) error {
	if err := validateMigrations(compatible); err != nil {
		return err
	}
	home = filepath.Clean(home)
	backupPath = filepath.Clean(backupPath)
	if !filepath.IsAbs(home) || !filepath.IsAbs(backupPath) {
		return fmt.Errorf("home and backup path must be absolute")
	}
	backupRoot := filepath.Join(home, BackupDirectory)
	relative, err := filepath.Rel(backupRoot, backupPath)
	if err != nil || relative == "." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || relative == ".." {
		return fmt.Errorf("backup must be a file inside the central backup directory")
	}
	lock, err := acquireOwnershipLock(home)
	if err != nil {
		return fmt.Errorf("offline restore requires the central server to be stopped: %w", err)
	}
	defer releaseOwnershipLock(lock)

	backup, err := openReadOnlySQLite(backupPath)
	if err != nil {
		return fmt.Errorf("open restore backup: %w", err)
	}
	defer backup.Close()
	if err := integrityCheck(ctx, backup); err != nil {
		return fmt.Errorf("backup integrity check failed: %w", err)
	}
	applied, err := readAppliedMigrations(ctx, backup)
	if err != nil {
		return err
	}
	if err := validateAppliedMigrations(applied, compatible); err != nil {
		return fmt.Errorf("backup migration history is incompatible: %w", err)
	}

	temporary := filepath.Join(home, fmt.Sprintf(".qianshou-restore-%d.db", time.Now().UnixNano()))
	defer os.Remove(temporary)
	if _, err := backup.ExecContext(ctx, `VACUUM INTO ?`, temporary); err != nil {
		return fmt.Errorf("materialize verified restore database: %w", err)
	}
	if err := os.Chmod(temporary, 0o600); err != nil {
		return fmt.Errorf("secure restored database: %w", err)
	}
	verified, err := openReadOnlySQLite(temporary)
	if err != nil {
		return fmt.Errorf("open materialized restore database: %w", err)
	}
	if err := integrityCheck(ctx, verified); err != nil {
		verified.Close()
		return fmt.Errorf("materialized restore integrity check failed: %w", err)
	}
	verified.Close()

	dbPath := filepath.Join(home, DatabaseFilename)
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.Remove(dbPath + suffix); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove stale SQLite %s before restore: %w", suffix, err)
		}
	}
	if err := os.Rename(temporary, dbPath); err != nil {
		return fmt.Errorf("atomically install restored database: %w", err)
	}
	if err := os.Chmod(dbPath, 0o600); err != nil {
		return fmt.Errorf("secure installed restored database: %w", err)
	}
	return nil
}

func openReadOnlySQLite(path string) (*sql.DB, error) {
	u := &url.URL{Scheme: "file", Path: path}
	query := u.Query()
	query.Set("mode", "ro")
	u.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}
