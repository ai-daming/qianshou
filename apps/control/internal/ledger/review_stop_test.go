package ledger

import (
	"context"
	"errors"
	"testing"
)

func TestReviewFreezesCurrentBaselineAndHead(t *testing.T) {
	store := openTestStore(t)
	seed := seedTrack(t, store, "track-1")
	ctx := context.Background()
	input := NewReviewRound{ID: "review-1", TrackID: seed.track.ID, BaselineID: seed.baseline.ID,
		PullRequestNumber: 42, ReviewedHeadSHA: "abc123", CriterionResultsJSON: `[{"criterion":"tests","passed":true}]`,
		Verdict: ReviewApproved, FindingsJSON: `[]`}
	review, err := store.RecordReviewRound(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := store.RecordReviewRound(ctx, input)
	if err != nil || retry.PayloadSHA256 != review.PayloadSHA256 {
		t.Fatalf("review retry = %+v, %v", retry, err)
	}
	input.ReviewedHeadSHA = "different"
	if _, err := store.RecordReviewRound(ctx, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed review error = %v", err)
	}
	if _, err := store.AppendBaseline(ctx, seed.track.ID, NewBaseline{ID: "baseline-2", AdoptionKey: "new-scope",
		BriefVersionID: seed.brief.ID, IssueUpdatedAt: "2026-08-19T02:00:00Z", IssueBody: "changed", ResolvedDoDJSON: `[]`}); err != nil {
		t.Fatal(err)
	}
	input.ID = "review-stale"
	input.ReviewedHeadSHA = "abc123"
	if _, err := store.RecordReviewRound(ctx, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale baseline review error = %v", err)
	}
}

func TestReviewRejectsAmbiguousOrWrongShapedJSON(t *testing.T) {
	store := openTestStore(t)
	seed := seedTrack(t, store, "track-1")
	base := NewReviewRound{ID: "review-bad", TrackID: seed.track.ID, BaselineID: seed.baseline.ID,
		PullRequestNumber: 42, ReviewedHeadSHA: "abc123", Verdict: ReviewApproved, FindingsJSON: `[]`}
	for _, criteria := range []string{`null`, `{}`, `[{"passed":true,"passed":false}]`, `[] {}`} {
		base.CriterionResultsJSON = criteria
		if _, err := store.RecordReviewRound(context.Background(), base); !errors.Is(err, ErrInvariant) {
			t.Fatalf("criteria %q error = %v", criteria, err)
		}
	}
}

func TestStopConditionResolutionIsOneShotAndIdempotent(t *testing.T) {
	store := openTestStore(t)
	seed := seedTrack(t, store, "track-1")
	ctx := context.Background()
	input := NewStopCondition{ID: "stop-1", TrackID: seed.track.ID, BaselineID: seed.baseline.ID,
		Kind: "SCOPE_DECISION", Reason: "Need a human decision", EvidenceJSON: `{"issueUpdatedAt":"2026-08-19T00:00:00Z"}`}
	stop, err := store.OpenStopCondition(ctx, input)
	if err != nil || !stop.Open() {
		t.Fatalf("OpenStopCondition = %+v, %v", stop, err)
	}
	if _, err := store.ResolveStopCondition(ctx, stop.ID, "WHATEVER", `{}`); !errors.Is(err, ErrInvariant) {
		t.Fatalf("unsupported resolution error = %v", err)
	}
	resolved, err := store.ResolveStopCondition(ctx, stop.ID, StopAdoptNewBaseline, `{"baselineId":"baseline-1"}`)
	if err != nil || resolved.Open() {
		t.Fatalf("ResolveStopCondition = %+v, %v", resolved, err)
	}
	retry, err := store.ResolveStopCondition(ctx, stop.ID, StopAdoptNewBaseline, `{"baselineId":"baseline-1"}`)
	if err != nil || retry.ResolvedAt == nil {
		t.Fatalf("resolution retry = %+v, %v", retry, err)
	}
	if _, err := store.ResolveStopCondition(ctx, stop.ID, StopContinue, `{}`); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed resolution error = %v", err)
	}
}

func TestReviewAndStopRejectMismatchedOrTerminalTrackEvidence(t *testing.T) {
	store := openTestStore(t)
	seed := seedTrack(t, store, "track-1")
	ctx := context.Background()
	if err := store.TerminateTrack(ctx, seed.track.ID, "ABANDONED"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordReviewRound(ctx, NewReviewRound{ID: "late-review", TrackID: seed.track.ID,
		BaselineID: seed.baseline.ID, PullRequestNumber: 1, ReviewedHeadSHA: "head",
		CriterionResultsJSON: `[]`, Verdict: ReviewApproved, FindingsJSON: `[]`}); !errors.Is(err, ErrConflict) {
		t.Fatalf("terminal Track review error = %v", err)
	}
	if _, err := store.OpenStopCondition(ctx, NewStopCondition{ID: "late-stop", TrackID: seed.track.ID,
		BaselineID: seed.baseline.ID, Kind: "LATE", Reason: "late", EvidenceJSON: `{}`}); !errors.Is(err, ErrConflict) {
		t.Fatalf("terminal Track stop error = %v", err)
	}
}

func TestActiveTrackAndRunBlockArchiveOrRetirement(t *testing.T) {
	store := openTestStore(t)
	seed := seedTrack(t, store, "track-1")
	ctx := context.Background()
	if err := store.ArchiveProject(ctx, seed.project.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("archive active project error = %v", err)
	}
	if err := store.BindTrack(ctx, seed.track.ID, TrackBinding{RunnerProjectBindingID: seed.binding.ID,
		WorkspacePath: "/allowed/worktrees/issue-5", Branch: "issue-5", BaseBranch: "main", BaseSHA: "abc123"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RetireRunnerProjectBinding(ctx, seed.binding.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("retire active binding error = %v", err)
	}
	conversation := createImplementationConversation(t, store, seed)
	if _, err := store.QueueRun(ctx, NewAgentRun{ID: "run-1", ConversationID: conversation.ID, TrackID: seed.track.ID,
		BaselineID: seed.baseline.ID, CommandKey: "command-1", CommandJSON: `{}`}); err != nil {
		t.Fatal(err)
	}
	if err := store.RetireRunner(ctx, seed.runner.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("retire runner with unfinished run error = %v", err)
	}
}

func TestTerminalOrRetiredObjectsCannotGainNewExecutionBindings(t *testing.T) {
	store := openTestStore(t)
	seed := seedTrack(t, store, "track-1")
	ctx := context.Background()
	if err := store.TerminateTrack(ctx, seed.track.ID, "ABANDONED"); err != nil {
		t.Fatal(err)
	}
	if err := store.BindTrack(ctx, seed.track.ID, TrackBinding{RunnerProjectBindingID: seed.binding.ID,
		WorkspacePath: "/late", Branch: "late", BaseBranch: "main", BaseSHA: "late"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("terminal Track binding error = %v", err)
	}
	if err := store.RetireRunnerProjectBinding(ctx, seed.binding.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.RetireRunner(ctx, seed.runner.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateRunnerProjectBinding(ctx, NewRunnerProjectBinding{ID: "late-binding", RunnerID: seed.runner.ID,
		ProjectID: seed.project.ID, MainCheckoutPath: "/late", RepositoryIDAtBinding: seed.project.RepositoryID}); !errors.Is(err, ErrConflict) {
		t.Fatalf("retired Runner binding error = %v", err)
	}
}

func TestArchivedConversationCannotAcquireVendorSession(t *testing.T) {
	store := openTestStore(t)
	seed := seedTrack(t, store, "track-1")
	conversation, err := store.CreateConversation(context.Background(), NewConversation{ID: "discussion",
		ProjectID: seed.project.ID, IssueNumber: seed.issueNumber, Role: RoleDiscussion,
		EngineID: "codex", RunnerProjectBindingID: seed.binding.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.ArchiveConversation(context.Background(), conversation.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.SetVendorSession(context.Background(), conversation.ID, "too-late"); err == nil {
		t.Fatal("archived conversation acquired a vendor session")
	}
}
