package ledger

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"strings"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	Version int
	Name    string
	SQL     string
}

func migrations() []migration {
	sql, err := migrationFiles.ReadFile("migrations/001_initial.sql")
	if err != nil {
		panic(fmt.Sprintf("embedded migration missing: %v", err))
	}
	return []migration{{Version: 1, Name: "initial", SQL: string(sql)}}
}

func validateMigrations(all []migration) error {
	if len(all) == 0 {
		return fmt.Errorf("migration set is empty")
	}
	for i, item := range all {
		want := i + 1
		if item.Version != want {
			return fmt.Errorf("migration sequence has a gap or duplicate at position %d: got version %d, want %d", i, item.Version, want)
		}
		if strings.TrimSpace(item.Name) == "" {
			return fmt.Errorf("migration %d name is required", item.Version)
		}
		if strings.TrimSpace(item.SQL) == "" {
			return fmt.Errorf("migration %d SQL is required", item.Version)
		}
	}
	return nil
}

func migrationChecksum(sql string) string {
	digest := sha256.Sum256([]byte(sql))
	return hex.EncodeToString(digest[:])
}
