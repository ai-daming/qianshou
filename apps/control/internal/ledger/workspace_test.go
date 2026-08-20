package ledger

import (
	"context"
	"testing"
)

func TestIssueWorkspaceReconstructsDurableDiscussionAndDeliveryArtifacts(t *testing.T) {
	store := openTestStore(t)
	seed := seedTrack(t, store, "track-1")
	ctx := context.Background()
	conversation, err := store.CreateConversation(ctx, NewConversation{
		ID: "discussion-1", ProjectID: seed.project.ID, IssueNumber: seed.issueNumber,
		Role: RoleDiscussion, EngineID: "codex", RunnerProjectBindingID: seed.binding.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.QueueRun(ctx, NewAgentRun{ID: "run-1", ConversationID: conversation.ID,
		CommandKey: "discussion-command-1", CommandJSON: `{"prompt":"hello"}`})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.StartRun(ctx, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.FinishRun(ctx, run.ID, RunCompleted, `{"ok":true}`); err != nil {
		t.Fatal(err)
	}
	stop, err := store.OpenStopCondition(ctx, NewStopCondition{ID: "stop-1", TrackID: seed.track.ID,
		BaselineID: seed.baseline.ID, Kind: "SCOPE_CHANGE", Reason: "body changed", EvidenceJSON: `{}`})
	if err != nil {
		t.Fatal(err)
	}

	workspace, err := store.GetIssueWorkspace(ctx, seed.project.ID, seed.issueNumber)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Conversations) != 1 || workspace.Conversations[0].ID != conversation.ID {
		t.Fatalf("conversations = %+v", workspace.Conversations)
	}
	if len(workspace.BriefVersions) != 1 || workspace.BriefVersions[0].ID != seed.brief.ID {
		t.Fatalf("brief versions = %+v", workspace.BriefVersions)
	}
	if workspace.ActiveTrack == nil || workspace.ActiveTrack.ID != seed.track.ID {
		t.Fatalf("active track = %+v", workspace.ActiveTrack)
	}
	if len(workspace.Baselines) != 1 || workspace.Baselines[0].ID != seed.baseline.ID {
		t.Fatalf("baselines = %+v", workspace.Baselines)
	}
	if len(workspace.Runs) != 1 || workspace.Runs[0].ID != run.ID || workspace.Runs[0].State() != RunCompleted {
		t.Fatalf("runs = %+v", workspace.Runs)
	}
	if len(workspace.StopConditions) != 1 || workspace.StopConditions[0].ID != stop.ID {
		t.Fatalf("stops = %+v", workspace.StopConditions)
	}
}

func TestGetActiveRunnerProjectBindingFailsClosed(t *testing.T) {
	store := openTestStore(t)
	seed := seedThroughBrief(t, store)
	ctx := context.Background()

	binding, err := store.GetActiveRunnerProjectBinding(ctx, seed.runner.ID, seed.project.ID)
	if err != nil || binding.ID != seed.binding.ID {
		t.Fatalf("active binding = %+v, %v", binding, err)
	}
	if err := store.RetireRunnerProjectBinding(ctx, seed.binding.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetActiveRunnerProjectBinding(ctx, seed.runner.ID, seed.project.ID); err != ErrNotFound {
		t.Fatalf("retired binding error = %v, want ErrNotFound", err)
	}
}
