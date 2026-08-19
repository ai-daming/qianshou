package ledger

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateMigrationsRejectsGapDuplicateAndInvalidDefinition(t *testing.T) {
	cases := []struct {
		name       string
		migrations []migration
	}{
		{"missing first", []migration{{Version: 2, Name: "two", SQL: "SELECT 1"}}},
		{"gap", []migration{{Version: 1, Name: "one", SQL: "SELECT 1"}, {Version: 3, Name: "three", SQL: "SELECT 3"}}},
		{"duplicate", []migration{{Version: 1, Name: "one", SQL: "SELECT 1"}, {Version: 1, Name: "again", SQL: "SELECT 2"}}},
		{"missing name", []migration{{Version: 1, SQL: "SELECT 1"}}},
		{"missing sql", []migration{{Version: 1, Name: "one"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := validateMigrations(tc.migrations); err == nil {
				t.Fatal("invalid migration set accepted")
			}
		})
	}
}

func TestFailedInitialMigrationLeavesNoHalfDatabaseAndCanRetry(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	broken := []migration{{Version: 1, Name: "broken", SQL: `CREATE TABLE partial(value TEXT); NOT SQL;`}}
	if _, err := openWithMigrations(context.Background(), home, broken); err == nil {
		t.Fatal("broken initial migration succeeded")
	}
	if _, err := os.Stat(filepath.Join(home, DatabaseFilename)); !os.IsNotExist(err) {
		t.Fatalf("failed initial database still exists: %v", err)
	}
	fixed := []migration{{Version: 1, Name: "fixed", SQL: `CREATE TABLE complete(value TEXT);`}}
	store, err := openWithMigrations(context.Background(), home, fixed)
	if err != nil {
		t.Fatalf("retry after fixed migration: %v", err)
	}
	store.Close()
}

func TestFailedUpgradeRollsBackAndLeavesVerifiedBackup(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	v1 := []migration{{Version: 1, Name: "base", SQL: `CREATE TABLE durable(value TEXT NOT NULL); INSERT INTO durable VALUES ('kept');`}}
	store, err := openWithMigrations(context.Background(), home, v1)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	v2 := append(append([]migration{}, v1...), migration{Version: 2, Name: "broken", SQL: `CREATE TABLE half_written(value TEXT); THIS IS NOT SQL;`})
	if _, err := openWithMigrations(context.Background(), home, v2); err == nil {
		t.Fatal("broken migration succeeded")
	}

	db := openRawForTest(t, filepath.Join(home, DatabaseFilename))
	defer db.Close()
	var value string
	if err := db.QueryRow(`SELECT value FROM durable`).Scan(&value); err != nil || value != "kept" {
		t.Fatalf("durable row after rollback = %q, %v", value, err)
	}
	var halfWritten int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type='table' AND name='half_written'`).Scan(&halfWritten); err != nil {
		t.Fatal(err)
	}
	if halfWritten != 0 {
		t.Fatal("failed migration left a partial table")
	}

	backups, err := filepath.Glob(filepath.Join(home, BackupDirectory, "qianshou-before-v2-*.db"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, %v", backups, err)
	}
	assertMode(t, backups[0], 0o600)
	backup := openRawForTest(t, backups[0])
	defer backup.Close()
	assertPragma(t, backup, "integrity_check", "ok")
}

func TestUpgradeRefusesToProceedWhenBackupCannotBeCreated(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	v1 := []migration{{Version: 1, Name: "base", SQL: `CREATE TABLE durable(value TEXT NOT NULL);`}}
	store, err := openWithMigrations(context.Background(), home, v1)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()

	backupPath := filepath.Join(home, BackupDirectory)
	if err := os.WriteFile(backupPath, []byte("blocks directory creation"), 0o600); err != nil {
		t.Fatal(err)
	}
	v2 := append(v1, migration{Version: 2, Name: "next", SQL: `CREATE TABLE next_table(value TEXT);`})
	_, err = openWithMigrations(context.Background(), home, v2)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "backup") {
		t.Fatalf("Open error = %v, want backup failure", err)
	}
}

func TestOpenRejectsMissingAppliedMigration(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	all := []migration{
		{Version: 1, Name: "one", SQL: `CREATE TABLE one(value TEXT);`},
		{Version: 2, Name: "two", SQL: `CREATE TABLE two(value TEXT);`},
	}
	store, err := openWithMigrations(context.Background(), home, all)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	db := openRawForTest(t, filepath.Join(home, DatabaseFilename))
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE version = 1`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := openWithMigrations(context.Background(), home, all); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing migration error = %v", err)
	}
}

func TestOpenRejectsExistingDatabaseWithEmptyMigrationHistory(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	store, err := Open(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	db := openRawForTest(t, filepath.Join(home, DatabaseFilename))
	if _, err := db.Exec(`DELETE FROM schema_migrations`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(context.Background(), home); err == nil || !strings.Contains(err.Error(), "missing version 1") {
		t.Fatalf("empty migration history error = %v", err)
	}
}

func TestVerifiedBackupCanRestoreTheStoppedLedger(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	v1 := []migration{{Version: 1, Name: "base", SQL: `CREATE TABLE durable(value TEXT NOT NULL); INSERT INTO durable VALUES ('from-backup');`}}
	store, err := openWithMigrations(context.Background(), home, v1)
	if err != nil {
		t.Fatal(err)
	}
	store.Close()
	v2 := append(v1, migration{Version: 2, Name: "next", SQL: `CREATE TABLE next_table(value TEXT);`})
	store, err = openWithMigrations(context.Background(), home, v2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`UPDATE durable SET value = 'after-upgrade'`); err != nil {
		t.Fatal(err)
	}
	store.Close()
	backups, err := filepath.Glob(filepath.Join(home, BackupDirectory, "qianshou-before-v2-*.db"))
	if err != nil || len(backups) != 1 {
		t.Fatalf("backups = %v, %v", backups, err)
	}
	if err := restoreBackup(context.Background(), home, backups[0], v1); err != nil {
		t.Fatal(err)
	}
	restored, err := openWithMigrations(context.Background(), home, v1)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var value string
	if err := restored.DB().QueryRow(`SELECT value FROM durable`).Scan(&value); err != nil || value != "from-backup" {
		t.Fatalf("restored value = %q, %v", value, err)
	}
}

func openRawForTest(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatal(err)
	}
	return db
}
