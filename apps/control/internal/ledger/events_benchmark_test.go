package ledger

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestRunEventCursorQueryUsesRunSequenceIndex(t *testing.T) {
	store, run := seedRunningRun(t)
	if err := store.AppendVendorFrame(context.Background(), NewVendorFrame{RunID: run.ID, Sequence: 1,
		RawPayload: []byte("frame"), Channel: "stdout", ParseStatus: FrameParsed, NormalizerVersion: "v1"},
		[]NewRunEvent{{Sequence: 1, Kind: EventStatus, PayloadJSON: `{}`}}); err != nil {
		t.Fatal(err)
	}
	rows, err := store.DB().Query(`EXPLAIN QUERY PLAN
		SELECT run_id, sequence, source_frame_sequence, event_kind, payload_json, payload_sha256, occurred_at
		FROM run_events WHERE run_id = ? AND sequence > ? ORDER BY sequence LIMIT ?`, run.ID, 0, 101)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var details []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	plan := strings.Join(details, "\n")
	if !strings.Contains(plan, "SEARCH run_events") || !strings.Contains(plan, "run_id=? AND sequence>?") {
		t.Fatalf("cursor query does not use the run/sequence index:\n%s", plan)
	}
}

func BenchmarkRunEventCursor100K(b *testing.B) {
	store, runID := seedBenchmarkRun(b)
	ctx := context.Background()
	const total = 100_000
	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		b.Fatal(err)
	}
	frameStatement, err := tx.PrepareContext(ctx, `INSERT INTO vendor_frames(run_id, frame_sequence, raw_payload,
		payload_sha256, channel, received_at, parse_status, normalizer_version)
		VALUES(?, ?, ?, ?, 'stdout', ?, 'PARSED', 'benchmark-v1')`)
	if err != nil {
		b.Fatal(err)
	}
	eventStatement, err := tx.PrepareContext(ctx, `INSERT INTO run_events(run_id, sequence, source_frame_sequence,
		event_kind, payload_json, payload_sha256, occurred_at) VALUES(?, ?, ?, 'STATUS', '{}', ?, ?)`)
	if err != nil {
		b.Fatal(err)
	}
	hash := strings.Repeat("a", 64)
	now := nowText()
	for sequence := 1; sequence <= total; sequence++ {
		if _, err := frameStatement.ExecContext(ctx, runID, sequence, []byte(fmt.Sprintf("frame-%06d", sequence)), hash, now); err != nil {
			b.Fatalf("frame %d: %v", sequence, err)
		}
		if _, err := eventStatement.ExecContext(ctx, runID, sequence, sequence, hash, now); err != nil {
			b.Fatalf("event %d: %v", sequence, err)
		}
	}
	frameStatement.Close()
	eventStatement.Close()
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for iteration := 0; iteration < b.N; iteration++ {
		after, count := 0, 0
		for {
			page, err := store.ListRunEvents(ctx, runID, after, 1000)
			if err != nil {
				b.Fatal(err)
			}
			count += len(page.Events)
			if page.NextCursor == nil {
				break
			}
			after = *page.NextCursor
		}
		if count != total {
			b.Fatalf("traversed %d events, want %d", count, total)
		}
	}
}

func seedBenchmarkRun(b *testing.B) (*Store, string) {
	b.Helper()
	ctx := context.Background()
	store, err := Open(ctx, b.TempDir()+"/home")
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = store.Close() })
	project, err := store.CreateProject(ctx, NewProject{ID: "benchmark", RepositoryID: 1, CreationSlug: "owner/benchmark"})
	if err != nil {
		b.Fatal(err)
	}
	runner, err := store.CreateRunner(ctx, NewRunner{ID: "runner", DisplayName: "Benchmark"})
	if err != nil {
		b.Fatal(err)
	}
	binding, err := store.CreateRunnerProjectBinding(ctx, NewRunnerProjectBinding{ID: "binding", RunnerID: runner.ID,
		ProjectID: project.ID, MainCheckoutPath: "/benchmark", RepositoryIDAtBinding: project.RepositoryID})
	if err != nil {
		b.Fatal(err)
	}
	if err := store.EnsureWorkItem(ctx, project.ID, 1); err != nil {
		b.Fatal(err)
	}
	brief, err := store.CreateBriefVersion(ctx, NewBriefVersion{ID: "brief", ProjectID: project.ID, IssueNumber: 1, Content: "benchmark",
		SourceIssueUpdatedAt: "v1", SourceIssueBodySHA256: sha256Text("body")})
	if err != nil {
		b.Fatal(err)
	}
	track, baseline, err := store.StartTrack(ctx, NewTrack{ID: "track", ProjectID: project.ID, IssueNumber: 1},
		NewBaseline{ID: "baseline", AdoptionKey: "initial", IssueUpdatedAt: "v1", IssueBody: "body",
			BriefVersionID: brief.ID, ResolvedDoDJSON: `[]`})
	if err != nil {
		b.Fatal(err)
	}
	if err := store.BindTrack(ctx, track.ID, TrackBinding{RunnerProjectBindingID: binding.ID, WorkspacePath: "/benchmark/worktree",
		Branch: "benchmark", BaseBranch: "main", BaseSHA: "benchmark-sha"}); err != nil {
		b.Fatal(err)
	}
	conversation, err := store.CreateConversation(ctx, NewConversation{ID: "conversation", ProjectID: project.ID,
		IssueNumber: 1, Role: RoleImplementation, EngineID: "codex", RunnerProjectBindingID: binding.ID})
	if err != nil {
		b.Fatal(err)
	}
	run, err := store.QueueRun(ctx, NewAgentRun{ID: "run", ConversationID: conversation.ID, TrackID: track.ID,
		BaselineID: baseline.ID, CommandKey: "benchmark", CommandJSON: `{}`})
	if err != nil {
		b.Fatal(err)
	}
	return store, run.ID
}
