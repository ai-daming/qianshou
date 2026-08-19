package ledger

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestNonConstraintDatabaseFailureIsNotBusinessConflict(t *testing.T) {
	store := openTestStore(t)
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	err := store.EnsureWorkItem(context.Background(), "qianshou", 5)
	if err == nil || errors.Is(err, ErrConflict) || errors.Is(err, ErrInvariant) {
		t.Fatalf("closed database error was misclassified: %v", err)
	}
	if !strings.Contains(err.Error(), "ensure work item") {
		t.Fatalf("database failure lost operation context: %v", err)
	}
}

func TestPreflightReadFailureDoesNotFallThroughToConflict(t *testing.T) {
	store := openTestStore(t)
	seed := seedThroughBrief(t, store)
	if _, err := store.DB().Exec(`DROP TABLE delivery_tracks`); err != nil {
		t.Fatal(err)
	}
	_, _, err := store.StartTrack(context.Background(),
		NewTrack{ID: "track-1", ProjectID: seed.project.ID, IssueNumber: seed.issueNumber},
		NewBaseline{ID: "baseline-1", AdoptionKey: "initial", BriefVersionID: seed.brief.ID,
			IssueUpdatedAt: "2026-08-19T00:00:00Z", IssueBody: "frozen", ResolvedDoDJSON: `[]`})
	if err == nil || errors.Is(err, ErrConflict) {
		t.Fatalf("preflight read failure was misclassified: %v", err)
	}
	if !strings.Contains(err.Error(), "read existing track") {
		t.Fatalf("preflight read failure lost operation context: %v", err)
	}
}
