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
	definitions := []struct {
		version int
		name    string
		path    string
	}{
		{version: 1, name: "initial", path: "migrations/001_initial.sql"},
		{version: 2, name: "brief_issue_evidence", path: "migrations/002_brief_issue_evidence.sql"},
	}
	result := make([]migration, 0, len(definitions))
	for _, definition := range definitions {
		sql, err := migrationFiles.ReadFile(definition.path)
		if err != nil {
			panic(fmt.Sprintf("embedded migration missing: %v", err))
		}
		result = append(result, migration{Version: definition.version, Name: definition.name, SQL: string(sql)})
	}
	return result
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
