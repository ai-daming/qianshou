package ledger

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestProjectCatalogUsesRepositoryIDAsImmutableIdentity(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	want := NewProject{ID: "qianshou", RepositoryID: 12345, CreationSlug: "ai-daming/qianshou"}
	first, err := store.CreateProject(ctx, want)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.CreateProject(ctx, want)
	if err != nil {
		t.Fatalf("idempotent retry: %v", err)
	}
	if first != second {
		t.Fatalf("retry changed project: first=%+v second=%+v", first, second)
	}
	for _, conflicting := range []NewProject{
		{ID: "qianshou", RepositoryID: 99999, CreationSlug: "other/repository"},
		{ID: "renamed-project", RepositoryID: 12345, CreationSlug: "renamed/repository"},
		{ID: "QIANSHOU", RepositoryID: 88888, CreationSlug: "other/case-collision"},
	} {
		if _, err := store.CreateProject(ctx, conflicting); !errors.Is(err, ErrConflict) {
			t.Fatalf("CreateProject(%+v) error = %v, want conflict", conflicting, err)
		}
	}

	projects, err := store.ListProjects(ctx)
	if err != nil || len(projects) != 1 || projects[0].RepositoryID != 12345 {
		t.Fatalf("ListProjects = %+v, %v", projects, err)
	}
}

func TestRunnerDisplayNameIsMutableButIdentityIsNot(t *testing.T) {
	store := openTestStore(t)
	runner, err := store.CreateRunner(context.Background(), NewRunner{ID: "runner-1", DisplayName: "Old name"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateRunnerDisplayName(context.Background(), runner.ID, "New name"); err != nil {
		t.Fatal(err)
	}
	var name string
	if err := store.DB().QueryRow(`SELECT display_name FROM runners WHERE runner_id = ?`, runner.ID).Scan(&name); err != nil || name != "New name" {
		t.Fatalf("display name = %q, %v", name, err)
	}
	if _, err := store.DB().Exec(`UPDATE runners SET runner_id = 'different' WHERE runner_id = ?`, runner.ID); err == nil {
		t.Fatal("runner identity changed")
	}
}

func TestProjectAndWorkItemIdentityAreCaseInsensitive(t *testing.T) {
	store := openTestStore(t)
	project, err := store.CreateProject(context.Background(), NewProject{ID: "Qianshou", RepositoryID: 1, CreationSlug: "owner/qianshou"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureWorkItem(context.Background(), strings.ToLower(project.ID), 5); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureWorkItem(context.Background(), strings.ToUpper(project.ID), 5); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := store.DB().QueryRow(`SELECT count(*) FROM work_items WHERE issue_number = 5`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("case-variant WorkItems = %d, %v", count, err)
	}
}

func TestOneActiveTrackPerWorkItemUnderConcurrency(t *testing.T) {
	store := openTestStore(t)
	seed := seedThroughBrief(t, store)
	ctx := context.Background()

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, id := range []string{"track-a", "track-b"} {
		wg.Add(1)
		go func(trackID string) {
			defer wg.Done()
			<-start
			_, _, err := store.StartTrack(ctx,
				NewTrack{ID: trackID, ProjectID: seed.project.ID, IssueNumber: seed.issueNumber},
				NewBaseline{ID: "baseline-" + trackID, AdoptionKey: "adopt-" + trackID, BriefVersionID: seed.brief.ID, IssueUpdatedAt: "2026-08-19T00:00:00Z", IssueBody: "frozen", ResolvedDoDJSON: `["done"]`})
			errs <- err
		}(id)
	}
	close(start)
	wg.Wait()
	close(errs)
	var successes, conflicts int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestBaselineSequenceIsContinuousAndAdoptionIsObjectIdempotent(t *testing.T) {
	store := openTestStore(t)
	seed := seedTrack(t, store, "track-1")
	ctx := context.Background()

	input := NewBaseline{ID: "baseline-2", AdoptionKey: "scope-change-1", BriefVersionID: seed.brief.ID, IssueUpdatedAt: "2026-08-19T01:00:00Z", IssueBody: "new frozen body", ResolvedDoDJSON: `["new done"]`}
	baseline, err := store.AppendBaseline(ctx, seed.track.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if baseline.Sequence != 2 {
		t.Fatalf("sequence = %d, want 2", baseline.Sequence)
	}
	retry, err := store.AppendBaseline(ctx, seed.track.ID, input)
	if err != nil || retry != baseline {
		t.Fatalf("idempotent retry = %+v, %v", retry, err)
	}
	input.IssueBody = "contradictory body"
	if _, err := store.AppendBaseline(ctx, seed.track.ID, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting adoption error = %v", err)
	}

	if _, err := store.DB().Exec(`INSERT INTO delivery_baselines(
		baseline_id, track_id, sequence, adoption_key, issue_updated_at, issue_body,
		issue_body_sha256, brief_version_id, resolved_dod_json, payload_sha256, created_at
	) SELECT 'gap', ?, 4, 'gap', issue_updated_at, issue_body, issue_body_sha256,
		brief_version_id, resolved_dod_json, payload_sha256, created_at
	  FROM delivery_baselines WHERE baseline_id = ?`, seed.track.ID, baseline.ID); err == nil {
		t.Fatal("direct SQL inserted a baseline sequence gap")
	}
}

func TestTrackBindingIsAllOrNothingAndWriteOnce(t *testing.T) {
	store := openTestStore(t)
	seed := seedTrack(t, store, "track-1")
	ctx := context.Background()
	binding := TrackBinding{
		RunnerProjectBindingID: seed.binding.ID,
		WorkspacePath:          "/allowed/worktrees/issue-5",
		Branch:                 "ai-daming/m1-04",
		BaseBranch:             "main",
		BaseSHA:                "2c86922",
	}
	if err := store.BindTrack(ctx, seed.track.ID, binding); err != nil {
		t.Fatal(err)
	}
	if err := store.BindTrack(ctx, seed.track.ID, binding); err != nil {
		t.Fatalf("identical retry: %v", err)
	}
	binding.WorkspacePath = "/different/path"
	if err := store.BindTrack(ctx, seed.track.ID, binding); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed binding error = %v", err)
	}
	if _, err := store.DB().Exec(`UPDATE delivery_tracks SET workspace_path = '/tampered' WHERE track_id = ?`, seed.track.ID); err == nil {
		t.Fatal("direct SQL changed a frozen track binding")
	}
}

func TestConversationAffinityAndVendorSessionAreWriteOnce(t *testing.T) {
	store := openTestStore(t)
	seed := seedTrack(t, store, "track-1")
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, NewConversation{
		ID: "conversation-1", ProjectID: seed.project.ID, IssueNumber: seed.issueNumber,
		Role: RoleImplementation, EngineID: "codex", RunnerProjectBindingID: seed.binding.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetVendorSession(ctx, conversation.ID, "vendor-session-1"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetVendorSession(ctx, conversation.ID, "vendor-session-1"); err != nil {
		t.Fatalf("identical retry: %v", err)
	}
	if err := store.SetVendorSession(ctx, conversation.ID, "vendor-session-2"); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed vendor session error = %v", err)
	}
}

func openTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := Open(context.Background(), filepath.Join(t.TempDir(), "home"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

type testSeed struct {
	project     Project
	runner      Runner
	binding     RunnerProjectBinding
	issueNumber int
	brief       BriefVersion
	track       DeliveryTrack
	baseline    DeliveryBaseline
}

func seedThroughBrief(t *testing.T, store *Store) testSeed {
	t.Helper()
	ctx := context.Background()
	project, err := store.CreateProject(ctx, NewProject{ID: "qianshou", RepositoryID: 12345, CreationSlug: "ai-daming/qianshou"})
	if err != nil {
		t.Fatal(err)
	}
	runner, err := store.CreateRunner(ctx, NewRunner{ID: "runner-1", DisplayName: "MacBook"})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.CreateRunnerProjectBinding(ctx, NewRunnerProjectBinding{
		ID: "binding-1", RunnerID: runner.ID, ProjectID: project.ID,
		MainCheckoutPath: "/allowed/qianshou", RepositoryIDAtBinding: project.RepositoryID,
	})
	if err != nil {
		t.Fatal(err)
	}
	const issueNumber = 5
	if err := store.EnsureWorkItem(ctx, project.ID, issueNumber); err != nil {
		t.Fatal(err)
	}
	brief, err := store.CreateBriefVersion(ctx, NewBriefVersion{
		ID: "brief-1", ProjectID: project.ID, IssueNumber: issueNumber, Content: "adopted brief",
		SourceIssueUpdatedAt: "2026-08-19T00:00:00Z", SourceIssueBodySHA256: sha256Text("frozen"),
	})
	if err != nil {
		t.Fatal(err)
	}
	return testSeed{project: project, runner: runner, binding: binding, issueNumber: issueNumber, brief: brief}
}

func seedTrack(t *testing.T, store *Store, trackID string) testSeed {
	t.Helper()
	seed := seedThroughBrief(t, store)
	track, baseline, err := store.StartTrack(context.Background(),
		NewTrack{ID: trackID, ProjectID: seed.project.ID, IssueNumber: seed.issueNumber},
		NewBaseline{ID: "baseline-1", AdoptionKey: "initial", BriefVersionID: seed.brief.ID, IssueUpdatedAt: "2026-08-19T00:00:00Z", IssueBody: "frozen", ResolvedDoDJSON: `["done"]`})
	if err != nil {
		t.Fatal(err)
	}
	seed.track = track
	seed.baseline = baseline
	return seed
}
