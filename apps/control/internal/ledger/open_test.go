package ledger

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestOpenCreatesExactSchemaAndRequiredPragmas(t *testing.T) {
	home := filepath.Join(t.TempDir(), "qianshou-home")
	store, err := Open(context.Background(), home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	rows, err := store.DB().Query(`
		SELECT name
		FROM sqlite_schema
		WHERE type = 'table' AND name NOT LIKE 'sqlite_%'
		ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	want := []string{
		"agent_runs",
		"brief_versions",
		"conversations",
		"delivery_baselines",
		"delivery_tracks",
		"projects",
		"review_rounds",
		"run_events",
		"runner_project_bindings",
		"runners",
		"schema_migrations",
		"stop_conditions",
		"vendor_frames",
		"work_items",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("tables = %#v, want %#v", got, want)
	}

	assertPragma(t, store.DB(), "foreign_keys", "1")
	assertPragma(t, store.DB(), "journal_mode", "wal")
	assertPragma(t, store.DB(), "synchronous", "2") // FULL
	assertPragma(t, store.DB(), "busy_timeout", "5000")
	assertPragma(t, store.DB(), "integrity_check", "ok")
}

func TestOpenUsesSensitiveFilesystemPermissions(t *testing.T) {
	home := filepath.Join(t.TempDir(), "nested", "qianshou-home")
	store, err := Open(context.Background(), home)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer store.Close()

	assertMode(t, home, 0o700)
	assertMode(t, filepath.Join(home, DatabaseFilename), 0o600)

	if _, err := store.DB().Exec(`CREATE TEMP TABLE force_wal(value TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := store.secureFiles(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		path := filepath.Join(home, DatabaseFilename) + suffix
		if _, err := os.Stat(path); err == nil {
			assertMode(t, path, 0o600)
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
}

func TestOpenRejectsChecksumDriftAndNewerDatabase(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate string
		want   string
	}{
		{"checksum drift", `UPDATE schema_migrations SET checksum = printf('%064d', 0) WHERE version = 1`, "checksum"},
		{"newer database", `INSERT INTO schema_migrations(version, name, checksum, applied_at) VALUES(999, 'future', printf('%064d', 9), '2026-08-19T00:00:00Z')`, "newer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "home")
			store, err := Open(context.Background(), home)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.DB().Exec(tc.mutate); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}

			_, err = Open(context.Background(), home)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), tc.want) {
				t.Fatalf("Open error = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestOpenRejectsCorruptDatabase(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	if err := os.MkdirAll(home, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, DatabaseFilename), []byte("not sqlite"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), home); err == nil {
		t.Fatal("corrupt database accepted")
	}
}

func TestOnlyOneCentralStoreMayOwnTheLedger(t *testing.T) {
	home := filepath.Join(t.TempDir(), "home")
	first, err := Open(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), home); err == nil || !strings.Contains(err.Error(), "already owned") {
		t.Fatalf("second central owner error = %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(context.Background(), home)
	if err != nil {
		t.Fatalf("open after owner exit: %v", err)
	}
	second.Close()
}

func assertPragma(t *testing.T, db *sql.DB, name, want string) {
	t.Helper()
	var got string
	if err := db.QueryRow("PRAGMA " + name).Scan(&got); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	if strings.ToLower(got) != want {
		t.Fatalf("PRAGMA %s = %q, want %q", name, got, want)
	}
}

func assertMode(t *testing.T, path string, want os.FileMode) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != want {
		t.Fatalf("%s mode = %o, want %o", path, got, want)
	}
}
