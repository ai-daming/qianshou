package capability

import (
	"reflect"
	"testing"
)

func TestDeriveFailsClosedWhenRequiredExternalFactsAreUnavailableOrContradictory(t *testing.T) {
	base := readyForReviewInput()
	cases := []struct {
		name   string
		mutate func(*Input)
		code   string
		source Source
	}{
		{"GitHub unavailable", func(in *Input) { in.GitHub.State = EvidenceUnavailable }, "GITHUB_FACTS_UNAVAILABLE", SourceGitHub},
		{"GitHub null", func(in *Input) { in.GitHub.Value = nil }, "GITHUB_FACTS_MISSING", SourceGitHub},
		{"GitHub contradictory", func(in *Input) { in.GitHub.State = EvidenceConflicting }, "GITHUB_FACTS_CONFLICTING", SourceGitHub},
		{"repository identity mismatch", func(in *Input) { in.GitHub.Value.RepositoryID = 999 }, "REPOSITORY_IDENTITY_MISMATCH", SourceReconciliation},
		{"Git unavailable", func(in *Input) { in.Git.State = EvidenceUnavailable }, "GIT_FACTS_UNAVAILABLE", SourceGit},
		{"baseline stale", func(in *Input) { in.GitHub.Value.IssueUpdatedAt = "newer" }, "BASELINE_STALE", SourceReconciliation},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			input := base
			input.GitHub.Value = cloneGitHub(base.GitHub.Value)
			input.Git.Value = cloneGit(base.Git.Value)
			tc.mutate(&input)
			got := Derive(input)
			if got.DerivedStage != nil {
				t.Fatalf("stage = %s, want nil", *got.DerivedStage)
			}
			if hasPrimary(got.AllowedActions) {
				t.Fatalf("fail-closed result exposed primary action: %+v", got.AllowedActions)
			}
			if !hasReason(got.BlockedReasons, tc.code, tc.source) {
				t.Fatalf("blocked reasons = %+v, want %s/%s", got.BlockedReasons, tc.code, tc.source)
			}
		})
	}
}

func TestAgentCompletionClaimCannotAdvanceStageWithoutExternalEvidence(t *testing.T) {
	input := readyForReviewInput()
	input.GitHub.Value.PullRequest = nil
	input.AgentClaimsCompleted = true
	got := Derive(input)
	if got.DerivedStage == nil || *got.DerivedStage != StageWorktreeReady {
		t.Fatalf("stage = %v, want WORKTREE_READY", got.DerivedStage)
	}
	if !reflect.DeepEqual(primaryActions(got.AllowedActions), []Action{ActionStartImplementation}) {
		t.Fatalf("actions = %+v", got.AllowedActions)
	}
}

func TestOpenStopPreservesDerivedStageButPausesDeliveryAction(t *testing.T) {
	input := readyForReviewInput()
	input.Ledger.OpenStopCount = 2
	got := Derive(input)
	if got.DerivedStage == nil || *got.DerivedStage != StageReadyForPRReview {
		t.Fatalf("stage = %v", got.DerivedStage)
	}
	if hasPrimary(got.AllowedActions) {
		t.Fatalf("stop condition did not pause primary actions: %+v", got.AllowedActions)
	}
	if !containsAction(got.AllowedActions, ActionResolveStop) || !containsAction(got.AllowedActions, ActionViewDiscussion) {
		t.Fatalf("side actions = %+v", got.AllowedActions)
	}
}

func TestReviewApprovalUnlocksIntegrationOnlyForCurrentHeadAndBaseline(t *testing.T) {
	base := approvedInput()
	got := Derive(base)
	if got.DerivedStage == nil || *got.DerivedStage != StageApproved ||
		!reflect.DeepEqual(primaryActions(got.AllowedActions), []Action{ActionRequestIntegration}) {
		t.Fatalf("approved result = %+v", got)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*Input)
		code   string
	}{
		{"head changed", func(in *Input) {
			in.GitHub.Value.PullRequest.HeadSHA = "new-head"
			in.Git.Value.HeadSHA = "new-head"
		}, "REVIEW_HEAD_STALE"},
		{"baseline changed", func(in *Input) { in.Ledger.CurrentBaselineID = "baseline-2" }, "REVIEW_BASELINE_STALE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := base
			input.GitHub.Value = cloneGitHub(base.GitHub.Value)
			input.Git.Value = cloneGit(base.Git.Value)
			tc.mutate(&input)
			got := Derive(input)
			if containsAction(got.AllowedActions, ActionRequestIntegration) {
				t.Fatalf("stale review unlocked integration: %+v", got)
			}
			if !hasReason(got.BlockedReasons, tc.code, SourceReconciliation) {
				t.Fatalf("blocked reasons = %+v", got.BlockedReasons)
			}
		})
	}
}

func TestRunnerPermissionBlocksExecutionWithoutErasingReliableStage(t *testing.T) {
	input := readyForReviewInput()
	input.GitHub.Value.PullRequest = nil
	input.Runner.State = EvidenceUnavailable
	got := Derive(input)
	if got.DerivedStage == nil || *got.DerivedStage != StageWorktreeReady {
		t.Fatalf("stage = %v", got.DerivedStage)
	}
	if hasPrimary(got.AllowedActions) || !hasReason(got.BlockedReasons, "RUNNER_UNAVAILABLE", SourceRunner) {
		t.Fatalf("result = %+v", got)
	}
}

func TestEveryResultHasAtMostOnePrimaryDeliveryAction(t *testing.T) {
	inputs := []Input{readyForReviewInput(), approvedInput()}
	worktree := readyForReviewInput()
	worktree.GitHub.Value.PullRequest = nil
	inputs = append(inputs, worktree)
	waiting := worktree
	waiting.Ledger.WorktreeBound = false
	waiting.Git.Value.WorktreePresent = false
	inputs = append(inputs, waiting)
	for _, input := range inputs {
		if got := primaryActions(Derive(input).AllowedActions); len(got) > 1 {
			t.Fatalf("primary actions = %+v", got)
		}
	}
}

func TestClosedIssueOrOfflineRunnerCannotStrandLedgerOnlyCloseout(t *testing.T) {
	input := approvedInput()
	input.GitHub.Value.IssueOpen = false
	input.GitHub.Value.DependenciesReady = false
	input.GitHub.Value.PullRequest.State = PullRequestMerged
	input.Git.Value.WorktreePresent = false
	input.Runner.State = EvidenceUnavailable
	got := Derive(input)
	if got.DerivedStage == nil || *got.DerivedStage != StageCloseoutComplete {
		t.Fatalf("stage = %v", got.DerivedStage)
	}
	if !reflect.DeepEqual(primaryActions(got.AllowedActions), []Action{ActionCloseTrack}) {
		t.Fatalf("closeout actions = %+v reasons=%+v", got.AllowedActions, got.BlockedReasons)
	}
}

func readyForReviewInput() Input {
	return Input{
		Ledger: LedgerFacts{ProjectRepositoryID: 101, ActiveTrack: true, CurrentBaselineID: "baseline-1",
			BaselineIssueUpdatedAt: "issue-v1", WorktreeBound: true, BindingActive: true},
		GitHub: Evidence[GitHubFacts]{State: EvidenceComplete, Value: &GitHubFacts{RepositoryID: 101,
			IssueUpdatedAt: "issue-v1", IssueOpen: true, DependenciesReady: true,
			PullRequest: &PullRequestFacts{Number: 42, HeadSHA: "head-1", State: PullRequestOpen}}},
		Git: Evidence[GitFacts]{State: EvidenceComplete, Value: &GitFacts{WorktreePresent: true,
			BindingMatches: true, HeadSHA: "head-1", Dirty: false}},
		Tests:  Evidence[TestFacts]{State: EvidenceComplete, Value: &TestFacts{RequiredPassed: true, HeadSHA: "head-1"}},
		Runner: Evidence[RunnerFacts]{State: EvidenceComplete, Value: &RunnerFacts{Online: true, BindingAllowed: true, EngineAllowed: true}},
	}
}

func approvedInput() Input {
	input := readyForReviewInput()
	input.Ledger.LatestReview = &ReviewFacts{BaselineID: "baseline-1", ReviewedHeadSHA: "head-1", Verdict: ReviewApproved}
	input.GitHub.Value.PullRequest.ChecksPassed = true
	return input
}

func cloneGitHub(value *GitHubFacts) *GitHubFacts {
	if value == nil {
		return nil
	}
	copy := *value
	if value.PullRequest != nil {
		pr := *value.PullRequest
		copy.PullRequest = &pr
	}
	return &copy
}
func cloneGit(value *GitFacts) *GitFacts {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func primaryActions(actions []Action) []Action {
	result := []Action{}
	for _, action := range actions {
		if action.Primary() {
			result = append(result, action)
		}
	}
	return result
}
func hasPrimary(actions []Action) bool { return len(primaryActions(actions)) != 0 }
func containsAction(actions []Action, want Action) bool {
	for _, action := range actions {
		if action == want {
			return true
		}
	}
	return false
}
func hasReason(reasons []BlockedReason, code string, source Source) bool {
	for _, reason := range reasons {
		if reason.Code == code && reason.Source == source {
			return true
		}
	}
	return false
}
