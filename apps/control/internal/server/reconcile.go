package server

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/ai-daming/qianshou/apps/control/internal/capability"
	"github.com/ai-daming/qianshou/apps/control/internal/config"
	"github.com/ai-daming/qianshou/apps/control/internal/githubapi"
	"github.com/ai-daming/qianshou/apps/control/internal/ledger"
	"github.com/ai-daming/qianshou/apps/control/internal/localrunner"
)

type evidenceSource struct {
	Source     capability.Source        `json:"source"`
	Kind       string                   `json:"kind"`
	State      capability.EvidenceState `json:"state"`
	ObservedAt string                   `json:"observedAt"`
}

type workspaceReconciliation struct {
	Issue           *githubapi.Issue
	Input           capability.Input
	Result          capability.Result
	EvidenceSources []evidenceSource
}

func (runtime *workflowRuntime) reconcileWorkspace(ctx context.Context, project ledger.Project, issueNumber int, workspace ledger.IssueWorkspace) workspaceReconciliation {
	observedAt := time.Now().UTC().Format(time.RFC3339Nano)
	reconciled := workspaceReconciliation{EvidenceSources: []evidenceSource{
		{Source: capability.SourceLedger, Kind: "ISSUE_WORKSPACE", State: capability.EvidenceComplete, ObservedAt: observedAt},
		{Source: capability.SourceGitHub, Kind: "ISSUE_AND_DEPENDENCIES", State: capability.EvidenceMissing, ObservedAt: observedAt},
		{Source: capability.SourceGitHub, Kind: "PULL_REQUEST", State: capability.EvidenceMissing, ObservedAt: observedAt},
		{Source: capability.SourceGit, Kind: "MAIN_CHECKOUT", State: capability.EvidenceMissing, ObservedAt: observedAt},
		{Source: capability.SourceRunner, Kind: "EMBEDDED_RUNNER", State: capability.EvidenceMissing, ObservedAt: observedAt},
		{Source: capability.SourceTests, Kind: "REQUIRED_TESTS", State: capability.EvidenceMissing, ObservedAt: observedAt},
	}}

	reconciled.Input.Ledger = ledgerCapabilityFacts(project, workspace)
	reconciled.Input.Tests = capability.Evidence[capability.TestFacts]{State: capability.EvidenceMissing}
	reconciled.Input.GitHub = capability.Evidence[capability.GitHubFacts]{State: capability.EvidenceMissing}
	reconciled.Input.Git = capability.Evidence[capability.GitFacts]{State: capability.EvidenceMissing}
	reconciled.Input.Runner = capability.Evidence[capability.RunnerFacts]{State: capability.EvidenceMissing}
	bindingPresent, bindingComplete := trackBindingState(workspace.ActiveTrack)
	if bindingPresent && !bindingComplete {
		reconciled.EvidenceSources[0].State = capability.EvidenceConflicting
	}

	repository, repositoryErr := runtime.facts.GetRepositoryByID(ctx, project.RepositoryID)
	switch {
	case repositoryErr != nil:
		reconciled.Input.GitHub.State = capability.EvidenceUnavailable
		reconciled.EvidenceSources[1].State = capability.EvidenceUnavailable
	case repository.ID != project.RepositoryID || strings.TrimSpace(repository.NameWithOwner) == "":
		reconciled.Input.GitHub.State = capability.EvidenceConflicting
		reconciled.EvidenceSources[1].State = capability.EvidenceConflicting
	default:
		issue, issueErr := runtime.facts.GetIssue(ctx, repository.NameWithOwner, issueNumber)
		switch {
		case issueErr != nil:
			reconciled.Input.GitHub.State = capability.EvidenceUnavailable
			reconciled.EvidenceSources[1].State = capability.EvidenceUnavailable
		case issue.Number != issueNumber:
			reconciled.Input.GitHub.State = capability.EvidenceConflicting
			reconciled.EvidenceSources[1].State = capability.EvidenceConflicting
		case strings.TrimSpace(issue.UpdatedAt) == "" || issue.Dependency.Status == githubapi.DependencyError:
			reconciled.Issue = &issue
			reconciled.Input.GitHub.State = capability.EvidenceMissing
			reconciled.EvidenceSources[1].State = capability.EvidenceMissing
		case issue.Dependency.Status != githubapi.DependencyReady && issue.Dependency.Status != githubapi.DependencyBlocked:
			reconciled.Issue = &issue
			reconciled.Input.GitHub.State = capability.EvidenceConflicting
			reconciled.EvidenceSources[1].State = capability.EvidenceConflicting
		default:
			reconciled.Issue = &issue
			reconciled.Input.GitHub = capability.Evidence[capability.GitHubFacts]{State: capability.EvidenceComplete,
				Value: &capability.GitHubFacts{RepositoryID: repository.ID, IssueUpdatedAt: issue.UpdatedAt,
					IssueOpen: strings.EqualFold(issue.State, "OPEN"), DependenciesReady: issue.Dependency.Status == githubapi.DependencyReady,
					PullRequestEvidenceComplete: false}}
			reconciled.EvidenceSources[1].State = capability.EvidenceComplete
		}
	}

	binding, bindingErr := runtime.store.GetActiveRunnerProjectBinding(ctx, runtime.config.Runner.ID, project.ID)
	switch {
	case strings.TrimSpace(runtime.config.Runner.ID) == "":
		reconciled.Input.Runner.State = capability.EvidenceMissing
	case errors.Is(bindingErr, ledger.ErrNotFound):
		reconciled.Input.Runner.State = capability.EvidenceMissing
	case bindingErr != nil:
		reconciled.Input.Runner.State = capability.EvidenceUnavailable
		reconciled.EvidenceSources[4].State = capability.EvidenceUnavailable
	case repositoryErr != nil || repository.ID != project.RepositoryID || strings.TrimSpace(repository.NameWithOwner) == "":
		reconciled.Input.Runner.State = capability.EvidenceUnavailable
		reconciled.EvidenceSources[4].State = capability.EvidenceUnavailable
	default:
		_, trustErr := localrunner.ValidateMainCheckout(ctx, runtime.config, binding, project.RepositoryID, repository.NameWithOwner)
		if trustErr != nil {
			reconciled.Input.Runner.State = capability.EvidenceConflicting
			reconciled.Input.Git.State = capability.EvidenceConflicting
			reconciled.EvidenceSources[3].State = capability.EvidenceConflicting
			reconciled.EvidenceSources[4].State = capability.EvidenceConflicting
		} else {
			bindingMatches := (!bindingPresent || bindingComplete) &&
				(workspace.ActiveTrack == nil || workspace.ActiveTrack.RunnerProjectBindingID == nil ||
					*workspace.ActiveTrack.RunnerProjectBindingID == binding.ID)
			reconciled.Input.Ledger.BindingActive = bindingMatches
			reconciled.Input.Runner = capability.Evidence[capability.RunnerFacts]{State: capability.EvidenceComplete,
				Value: &capability.RunnerFacts{Online: true, BindingAllowed: bindingMatches, EngineAllowed: hasEnabledEngine(runtime.config)}}
			reconciled.EvidenceSources[4].State = capability.EvidenceComplete
			if workspace.ActiveTrack != nil && workspace.ActiveTrack.WorkspacePath != nil {
				reconciled.Input.Git.State = capability.EvidenceMissing
				reconciled.EvidenceSources[3].Kind = "TRACK_WORKTREE"
			} else {
				reconciled.Input.Git = capability.Evidence[capability.GitFacts]{State: capability.EvidenceComplete,
					Value: &capability.GitFacts{WorktreePresent: false}}
				reconciled.EvidenceSources[3].State = capability.EvidenceComplete
			}
		}
	}

	reconciled.Result = capability.Derive(reconciled.Input)
	return reconciled
}

func ledgerCapabilityFacts(project ledger.Project, workspace ledger.IssueWorkspace) capability.LedgerFacts {
	facts := capability.LedgerFacts{ProjectRepositoryID: project.RepositoryID, ActiveTrack: workspace.ActiveTrack != nil}
	if workspace.ActiveTrack != nil {
		if workspace.ActiveTrack.TerminalKind != nil {
			facts.TrackTerminalKind = *workspace.ActiveTrack.TerminalKind
		}
		facts.WorktreeBound, _ = trackBindingState(workspace.ActiveTrack)
	}
	if len(workspace.Baselines) > 0 {
		latest := workspace.Baselines[len(workspace.Baselines)-1]
		facts.CurrentBaselineID = latest.ID
		facts.BaselineIssueUpdatedAt = latest.IssueUpdatedAt
	}
	for _, stop := range workspace.StopConditions {
		if stop.Open() {
			facts.OpenStopCount++
		}
	}
	roles := map[string]ledger.Role{}
	for _, conversation := range workspace.Conversations {
		roles[conversation.ID] = conversation.Role
	}
	for _, run := range workspace.Runs {
		if run.State() != ledger.RunQueued && run.State() != ledger.RunRunning {
			continue
		}
		switch roles[run.ConversationID] {
		case ledger.RoleImplementation, ledger.RoleRepair:
			facts.ImplementationRunning = true
		case ledger.RoleReview:
			facts.ReviewRunning = true
		}
	}
	return facts
}

func trackBindingState(track *ledger.DeliveryTrack) (present bool, complete bool) {
	if track == nil {
		return false, false
	}
	fields := []*string{track.RunnerProjectBindingID, track.WorkspacePath, track.Branch, track.BaseBranch, track.BaseSHAAtBinding}
	count := 0
	for _, field := range fields {
		if field != nil && strings.TrimSpace(*field) != "" {
			count++
		}
	}
	return count > 0, count == len(fields)
}

func hasEnabledEngine(cfg config.Config) bool {
	for _, engine := range cfg.Engines {
		adapter := strings.ToLower(strings.TrimSpace(engine.Adapter))
		if strings.TrimSpace(engine.Command) != "" && (adapter == "codex" || adapter == "claude") {
			return true
		}
	}
	return false
}
