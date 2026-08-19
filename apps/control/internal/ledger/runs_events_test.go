package ledger

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestRunLifecycleIsDerivedAndCommandKeyIsIdempotent(t *testing.T) {
	store := openTestStore(t)
	seed := seedTrack(t, store, "track-1")
	conversation := createImplementationConversation(t, store, seed)
	ctx := context.Background()
	input := NewAgentRun{ID: "run-1", ConversationID: conversation.ID, TrackID: seed.track.ID,
		BaselineID: seed.baseline.ID, CommandKey: "command-1", CommandJSON: `{"prompt":"implement"}`}
	run, err := store.QueueRun(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if run.State() != RunQueued {
		t.Fatalf("state = %s", run.State())
	}
	retry, err := store.QueueRun(ctx, input)
	if err != nil || retry.State() != RunQueued {
		t.Fatalf("retry = %+v, %v", retry, err)
	}
	input.CommandJSON = `{"prompt":"different"}`
	if _, err := store.QueueRun(ctx, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("command conflict error = %v", err)
	}

	run, err = store.StartRun(ctx, run.ID)
	if err != nil || run.State() != RunRunning {
		t.Fatalf("StartRun = %+v, %v", run, err)
	}
	run, err = store.FinishRun(ctx, run.ID, RunCompleted, `{"summary":"done"}`)
	if err != nil || run.State() != RunCompleted {
		t.Fatalf("FinishRun = %+v, %v", run, err)
	}
	if _, err := store.FinishRun(ctx, run.ID, RunFailed, `{"error":"changed"}`); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed terminal outcome error = %v", err)
	}
}

func TestRunRequiresCurrentBaselineAndOneUnfinishedRunPerConversation(t *testing.T) {
	store := openTestStore(t)
	seed := seedTrack(t, store, "track-1")
	conversation := createImplementationConversation(t, store, seed)
	ctx := context.Background()
	first := NewAgentRun{ID: "run-1", ConversationID: conversation.ID, TrackID: seed.track.ID,
		BaselineID: seed.baseline.ID, CommandKey: "command-1", CommandJSON: `{}`}
	if _, err := store.QueueRun(ctx, first); err != nil {
		t.Fatal(err)
	}
	second := first
	second.ID, second.CommandKey = "run-2", "command-2"
	if _, err := store.QueueRun(ctx, second); !errors.Is(err, ErrConflict) {
		t.Fatalf("second unfinished run error = %v", err)
	}
	if _, err := store.StartRun(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinishRun(ctx, first.ID, RunCompleted, `{}`); err != nil {
		t.Fatal(err)
	}

	newBaseline, err := store.AppendBaseline(ctx, seed.track.ID, NewBaseline{ID: "baseline-2", AdoptionKey: "scope-2",
		BriefVersionID: seed.brief.ID, IssueUpdatedAt: "2026-08-19T02:00:00Z", IssueBody: "changed", ResolvedDoDJSON: `[]`})
	if err != nil {
		t.Fatal(err)
	}
	second.BaselineID = seed.baseline.ID
	if _, err := store.QueueRun(ctx, second); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale baseline run error = %v", err)
	}
	second.BaselineID = newBaseline.ID
	if _, err := store.QueueRun(ctx, second); err != nil {
		t.Fatalf("current baseline run: %v", err)
	}
}

func TestDeliveryRunRequiresMatchingBoundTrackAndConversationWorkItem(t *testing.T) {
	store := openTestStore(t)
	seed := seedTrack(t, store, "track-a")
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, NewConversation{ID: "conversation-a", ProjectID: seed.project.ID,
		IssueNumber: seed.issueNumber, Role: RoleImplementation, EngineID: "codex", RunnerProjectBindingID: seed.binding.ID})
	if err != nil {
		t.Fatal(err)
	}
	input := NewAgentRun{ID: "run-unbound", ConversationID: conversation.ID, TrackID: seed.track.ID,
		BaselineID: seed.baseline.ID, CommandKey: "unbound", CommandJSON: `{}`}
	if _, err := store.QueueRun(ctx, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("unbound delivery run error = %v", err)
	}

	if err := store.BindTrack(ctx, seed.track.ID, TrackBinding{RunnerProjectBindingID: seed.binding.ID,
		WorkspacePath: "/allowed/worktrees/a", Branch: "a", BaseBranch: "main", BaseSHA: "a"}); err != nil {
		t.Fatal(err)
	}
	projectB, err := store.CreateProject(ctx, NewProject{ID: "other", RepositoryID: 222, CreationSlug: "owner/other"})
	if err != nil {
		t.Fatal(err)
	}
	bindingB, err := store.CreateRunnerProjectBinding(ctx, NewRunnerProjectBinding{ID: "binding-b", RunnerID: seed.runner.ID,
		ProjectID: projectB.ID, MainCheckoutPath: "/allowed/other", RepositoryIDAtBinding: projectB.RepositoryID})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureWorkItem(ctx, projectB.ID, 5); err != nil {
		t.Fatal(err)
	}
	conversationB, err := store.CreateConversation(ctx, NewConversation{ID: "conversation-b", ProjectID: projectB.ID,
		IssueNumber: 5, Role: RoleImplementation, EngineID: "codex", RunnerProjectBindingID: bindingB.ID})
	if err != nil {
		t.Fatal(err)
	}
	input.ID, input.CommandKey, input.ConversationID = "run-mismatch", "mismatch", conversationB.ID
	if _, err := store.QueueRun(ctx, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("cross-WorkItem run error = %v", err)
	}
}

func TestReopenInterruptsRunningButNeverStartsQueuedRun(t *testing.T) {
	home := t.TempDir() + "/home"
	store, err := Open(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	seed := seedTrack(t, store, "track-1")
	conversation1 := createImplementationConversation(t, store, seed)
	running, err := store.QueueRun(context.Background(), NewAgentRun{ID: "running", ConversationID: conversation1.ID,
		TrackID: seed.track.ID, BaselineID: seed.baseline.ID, CommandKey: "running", CommandJSON: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.StartRun(context.Background(), running.ID); err != nil {
		t.Fatal(err)
	}
	conversation2, err := store.CreateConversation(context.Background(), NewConversation{ID: "conversation-2",
		ProjectID: seed.project.ID, IssueNumber: seed.issueNumber, Role: RoleImplementation,
		EngineID: "codex", RunnerProjectBindingID: seed.binding.ID})
	if err != nil {
		t.Fatal(err)
	}
	queued, err := store.QueueRun(context.Background(), NewAgentRun{ID: "queued", ConversationID: conversation2.ID,
		TrackID: seed.track.ID, BaselineID: seed.baseline.ID, CommandKey: "queued", CommandJSON: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(context.Background(), home)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	running, _ = reopened.GetRun(context.Background(), running.ID)
	queued, _ = reopened.GetRun(context.Background(), queued.ID)
	if running.State() != RunInterrupted {
		t.Fatalf("orphan running state = %s", running.State())
	}
	if queued.State() != RunQueued || queued.StartedAt != nil {
		t.Fatalf("queued run was started or changed: %+v", queued)
	}
}

func TestVendorFrameAndEventsAreAtomicExactAndContiguous(t *testing.T) {
	store, run := seedRunningRun(t)
	ctx := context.Background()
	raw := []byte("{\"private\":\"unredacted-user-data\"}\n")
	frame := NewVendorFrame{RunID: run.ID, Sequence: 1, RawPayload: raw, Channel: "stdout",
		ParseStatus: FrameParsed, NormalizerVersion: "v1"}
	events := []NewRunEvent{
		{Sequence: 1, Kind: EventAgentMessage, PayloadJSON: `{"text":"hello"}`},
		{Sequence: 2, Kind: EventToolCall, PayloadJSON: `{"name":"shell"}`},
	}
	if err := store.AppendVendorFrame(ctx, frame, events); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendVendorFrame(ctx, frame, events); err != nil {
		t.Fatalf("identical frame retry: %v", err)
	}
	stored, err := store.GetVendorFrame(ctx, run.ID, 1)
	if err != nil || !bytes.Equal(stored.RawPayload, raw) {
		t.Fatalf("raw frame = %q, %v", stored.RawPayload, err)
	}
	changed := frame
	changed.RawPayload = []byte("redacted")
	if err := store.AppendVendorFrame(ctx, changed, events); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed raw retry error = %v", err)
	}
	gap := NewVendorFrame{RunID: run.ID, Sequence: 3, RawPayload: []byte("gap"), Channel: "stdout",
		ParseStatus: FrameIgnored, NormalizerVersion: "v1"}
	if err := store.AppendVendorFrame(ctx, gap, nil); !errors.Is(err, ErrConflict) {
		t.Fatalf("frame gap error = %v", err)
	}
	badEvents := NewVendorFrame{RunID: run.ID, Sequence: 2, RawPayload: []byte("event-gap"), Channel: "stdout",
		ParseStatus: FrameParsed, NormalizerVersion: "v1"}
	if err := store.AppendVendorFrame(ctx, badEvents, []NewRunEvent{{Sequence: 4, Kind: EventStatus, PayloadJSON: `{}`}}); !errors.Is(err, ErrConflict) {
		t.Fatalf("event gap error = %v", err)
	}
	if _, err := store.GetVendorFrame(ctx, run.ID, 2); !errors.Is(err, ErrNotFound) {
		t.Fatalf("failed transaction persisted raw frame: %v", err)
	}
}

func TestIgnoredAndFailedFramesHaveExplicitNormalizationOutcomes(t *testing.T) {
	store, run := seedRunningRun(t)
	ctx := context.Background()
	ignored := NewVendorFrame{RunID: run.ID, Sequence: 1, RawPayload: []byte("noise"), Channel: "stderr",
		ParseStatus: FrameIgnored, NormalizerVersion: "v1"}
	if err := store.AppendVendorFrame(ctx, ignored, nil); err != nil {
		t.Fatal(err)
	}
	failed := NewVendorFrame{RunID: run.ID, Sequence: 2, RawPayload: []byte("broken"), Channel: "stdout",
		ParseStatus: FrameFailed, NormalizerVersion: "v1", ParseError: "invalid frame"}
	if err := store.AppendVendorFrame(ctx, failed, []NewRunEvent{{Sequence: 1, Kind: EventError, PayloadJSON: `{"error":"invalid frame"}`}}); err != nil {
		t.Fatal(err)
	}
	invalid := NewVendorFrame{RunID: run.ID, Sequence: 3, RawPayload: []byte("broken"), Channel: "stdout",
		ParseStatus: FrameFailed, NormalizerVersion: "v1", ParseError: "invalid"}
	if err := store.AppendVendorFrame(ctx, invalid, nil); !errors.Is(err, ErrInvariant) {
		t.Fatalf("FAILED frame without ERROR event = %v", err)
	}
}

func TestSyntheticEventHasNoVendorFrameSource(t *testing.T) {
	store, run := seedRunningRun(t)
	input := NewRunEvent{Sequence: 1, Kind: EventStatus, PayloadJSON: `{"status":"queued"}`}
	if err := store.AppendSyntheticEvent(context.Background(), input, run.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.AppendSyntheticEvent(context.Background(), input, run.ID); err != nil {
		t.Fatalf("synthetic retry: %v", err)
	}
	page, err := store.ListRunEvents(context.Background(), run.ID, 0, 10)
	if err != nil || len(page.Events) != 1 || page.Events[0].SourceFrameSequence != nil {
		t.Fatalf("events = %+v, %v", page.Events, err)
	}
}

func TestRunEventCursorTraversesBeyondOneHundred(t *testing.T) {
	store, run := seedRunningRun(t)
	ctx := context.Background()
	const total = 257
	for i := 1; i <= total; i++ {
		frame := NewVendorFrame{RunID: run.ID, Sequence: i, RawPayload: []byte(fmt.Sprintf("frame-%d", i)),
			Channel: "stdout", ParseStatus: FrameParsed, NormalizerVersion: "v1"}
		if err := store.AppendVendorFrame(ctx, frame, []NewRunEvent{{Sequence: i, Kind: EventStatus, PayloadJSON: fmt.Sprintf(`{"n":%d}`, i)}}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	var after int
	var got []RunEvent
	for {
		page, err := store.ListRunEvents(ctx, run.ID, after, 37)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, page.Events...)
		if page.NextCursor == nil {
			break
		}
		after = *page.NextCursor
	}
	if len(got) != total || got[0].Sequence != 1 || got[total-1].Sequence != total {
		t.Fatalf("traversed %d events, first=%d last=%d", len(got), got[0].Sequence, got[len(got)-1].Sequence)
	}
}

func createImplementationConversation(t *testing.T, store *Store, seed testSeed) Conversation {
	t.Helper()
	if err := store.BindTrack(context.Background(), seed.track.ID, TrackBinding{RunnerProjectBindingID: seed.binding.ID,
		WorkspacePath: "/allowed/worktrees/issue-5", Branch: "issue-5", BaseBranch: "main", BaseSHA: "abc123"}); err != nil {
		t.Fatal(err)
	}
	conversation, err := store.CreateConversation(context.Background(), NewConversation{ID: "conversation-1",
		ProjectID: seed.project.ID, IssueNumber: seed.issueNumber, Role: RoleImplementation,
		EngineID: "codex", RunnerProjectBindingID: seed.binding.ID})
	if err != nil {
		t.Fatal(err)
	}
	return conversation
}

func seedRunningRun(t *testing.T) (*Store, AgentRun) {
	t.Helper()
	store := openTestStore(t)
	seed := seedTrack(t, store, "track-1")
	conversation := createImplementationConversation(t, store, seed)
	run, err := store.QueueRun(context.Background(), NewAgentRun{ID: "run-1", ConversationID: conversation.ID,
		TrackID: seed.track.ID, BaselineID: seed.baseline.ID, CommandKey: "command-1", CommandJSON: `{}`})
	if err != nil {
		t.Fatal(err)
	}
	run, err = store.StartRun(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	return store, run
}
