package ledger

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestSchemaCarriesPartialUniquenessAndNoDerivedStateColumns(t *testing.T) {
	store := openTestStore(t)
	for _, index := range []string{
		"one_active_binding_per_runner_project",
		"one_active_binding_per_runner_path",
		"one_active_track_per_work_item",
		"one_unfinished_run_per_conversation",
		"stop_conditions_open_track",
	} {
		var ddl string
		if err := store.DB().QueryRow(`SELECT sql FROM sqlite_schema WHERE type='index' AND name = ?`, index).Scan(&ddl); err != nil {
			t.Fatalf("index %s: %v", index, err)
		}
		if !strings.Contains(strings.ToUpper(ddl), " WHERE ") {
			t.Fatalf("index %s is not partial: %s", index, ddl)
		}
	}
	rows, err := store.DB().Query(`SELECT name FROM pragma_table_info('delivery_tracks')
		UNION ALL SELECT name FROM pragma_table_info('brief_versions')
		UNION ALL SELECT name FROM pragma_table_info('stop_conditions')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{"status", "stage", "current_baseline_id", "allowed_actions", "blocked_reasons"} {
			if strings.EqualFold(column, forbidden) {
				t.Fatalf("derived or generic state column persisted: %s", column)
			}
		}
	}
}

func TestEveryBusinessForeignKeyRestrictsDeleteAndEveryBusinessTableRejectsHardDelete(t *testing.T) {
	store := openTestStore(t)
	tables := []string{"projects", "runners", "runner_project_bindings", "work_items", "conversations",
		"brief_versions", "delivery_baselines", "delivery_tracks", "agent_runs", "vendor_frames", "run_events",
		"review_rounds", "stop_conditions"}
	for _, table := range tables {
		rows, err := store.DB().Query(fmt.Sprintf(`PRAGMA foreign_key_list(%q)`, table))
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id, sequence int
			var parent, from, to, onUpdate, onDelete, match string
			if err := rows.Scan(&id, &sequence, &parent, &from, &to, &onUpdate, &onDelete, &match); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			if onDelete != "RESTRICT" {
				rows.Close()
				t.Fatalf("%s.%s delete action = %s", table, from, onDelete)
			}
		}
		rows.Close()
		var count int
		if err := store.DB().QueryRow(`SELECT count(*) FROM sqlite_schema
			WHERE type='trigger' AND tbl_name = ? AND name LIKE '%no_delete'`, table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("business table %s has %d hard-delete guards", table, count)
		}
	}

	project, err := store.CreateProject(context.Background(), NewProject{ID: "delete-test", RepositoryID: 7, CreationSlug: "owner/delete-test"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().Exec(`DELETE FROM projects WHERE project_id = ?`, project.ID); err == nil {
		t.Fatal("direct hard delete succeeded")
	}
}
