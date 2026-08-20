// Package capability derives delivery state and legal actions from owned
// evidence. It is deliberately pure: none of its outputs are persisted.
package capability

type EvidenceState string

const (
	EvidenceComplete    EvidenceState = "COMPLETE"
	EvidenceMissing     EvidenceState = "MISSING"
	EvidenceUnavailable EvidenceState = "UNAVAILABLE"
	EvidenceConflicting EvidenceState = "CONFLICTING"
)

type Evidence[T any] struct {
	State EvidenceState
	Value *T
}

type Source string

const (
	SourceLedger         Source = "LEDGER"
	SourceGitHub         Source = "GITHUB"
	SourceGit            Source = "GIT"
	SourceTests          Source = "TESTS"
	SourceRunner         Source = "RUNNER"
	SourceReconciliation Source = "RECONCILIATION"
)

type Stage string

const (
	StageWaitingForWorktree Stage = "WAITING_FOR_WORKTREE"
	StageWorktreeReady      Stage = "WORKTREE_READY"
	StageImplementing       Stage = "IMPLEMENTING"
	StageReadyForPRReview   Stage = "READY_FOR_PR_REVIEW"
	StageReviewing          Stage = "REVIEWING"
	StageChangesRequested   Stage = "CHANGES_REQUESTED"
	StageApproved           Stage = "APPROVED"
	StageMergedToTarget     Stage = "MERGED_TO_TARGET"
	StageCleanupRequired    Stage = "CLEANUP_REQUIRED"
	StageCloseoutComplete   Stage = "CLOSEOUT_COMPLETE"
)

type Action string

const (
	ActionViewDiscussion      Action = "VIEW_DISCUSSION"
	ActionStartDiscussion     Action = "START_DISCUSSION"
	ActionResolveStop         Action = "RESOLVE_STOP"
	ActionCreateWorktree      Action = "CREATE_WORKTREE"
	ActionStartImplementation Action = "START_IMPLEMENTATION"
	ActionStartReview         Action = "START_REVIEW"
	ActionStartRepair         Action = "START_REPAIR"
	ActionRequestIntegration  Action = "REQUEST_INTEGRATION"
	ActionCleanupWorktree     Action = "CLEANUP_WORKTREE"
	ActionCloseTrack          Action = "CLOSE_TRACK"
)

func (a Action) Primary() bool {
	switch a {
	case ActionCreateWorktree, ActionStartImplementation, ActionStartReview, ActionStartRepair,
		ActionRequestIntegration, ActionCleanupWorktree, ActionCloseTrack:
		return true
	default:
		return false
	}
}

type PullRequestState string

const (
	PullRequestOpen   PullRequestState = "OPEN"
	PullRequestMerged PullRequestState = "MERGED"
	PullRequestClosed PullRequestState = "CLOSED"
)

type ReviewVerdict string

const (
	ReviewApproved         ReviewVerdict = "APPROVED"
	ReviewChangesRequested ReviewVerdict = "CHANGES_REQUESTED"
)

type LedgerFacts struct {
	ProjectRepositoryID    int64
	ActiveTrack            bool
	TrackTerminalKind      string
	CurrentBaselineID      string
	BaselineIssueUpdatedAt string
	WorktreeBound          bool
	BindingActive          bool
	OpenStopCount          int
	ImplementationRunning  bool
	ReviewRunning          bool
	LatestReview           *ReviewFacts
}

type ReviewFacts struct {
	BaselineID      string
	ReviewedHeadSHA string
	Verdict         ReviewVerdict
}

type GitHubFacts struct {
	RepositoryID                int64
	IssueUpdatedAt              string
	IssueOpen                   bool
	DependenciesReady           bool
	PullRequestEvidenceComplete bool
	PullRequest                 *PullRequestFacts
}

type PullRequestFacts struct {
	Number       int
	HeadSHA      string
	State        PullRequestState
	ChecksPassed bool
}

type GitFacts struct {
	WorktreePresent bool
	BindingMatches  bool
	HeadSHA         string
	Dirty           bool
}

type TestFacts struct {
	RequiredPassed bool
	HeadSHA        string
}

type RunnerFacts struct {
	Online         bool
	BindingAllowed bool
	EngineAllowed  bool
}

type Input struct {
	Ledger               LedgerFacts
	GitHub               Evidence[GitHubFacts]
	Git                  Evidence[GitFacts]
	Tests                Evidence[TestFacts]
	Runner               Evidence[RunnerFacts]
	AgentClaimsCompleted bool
}

type BlockedReason struct {
	Code    string `json:"code"`
	Source  Source `json:"source"`
	Message string `json:"message"`
}

type Result struct {
	DerivedStage   *Stage          `json:"derivedStage"`
	AllowedActions []Action        `json:"allowedActions"`
	BlockedReasons []BlockedReason `json:"blockedReasons"`
}

func Derive(input Input) Result {
	result := Result{AllowedActions: []Action{ActionViewDiscussion}, BlockedReasons: []BlockedReason{}}
	runnerAllowed := runnerAllowsExecution(input.Runner, &result)
	if input.Ledger.BindingActive && runnerAllowed {
		result.AllowedActions = append(result.AllowedActions, ActionStartDiscussion)
	}
	if !input.Ledger.ActiveTrack {
		return blockedWithoutStage(result, "ACTIVE_TRACK_MISSING", SourceLedger, "No active DeliveryTrack exists for this WorkItem.")
	}
	if input.Ledger.ProjectRepositoryID <= 0 || input.Ledger.CurrentBaselineID == "" || input.Ledger.BaselineIssueUpdatedAt == "" {
		return blockedWithoutStage(result, "LEDGER_INVARIANT_MISSING", SourceLedger, "The active Track does not have a complete current Baseline and repository identity.")
	}
	if input.Ledger.OpenStopCount < 0 || (input.Ledger.WorktreeBound && !input.Ledger.BindingActive) {
		return blockedWithoutStage(result, "LEDGER_INVARIANT_CONFLICT", SourceLedger, "Ledger lifecycle fields contradict each other.")
	}
	if input.Ledger.TrackTerminalKind != "" {
		return blockedWithoutStage(result, "ACTIVE_TRACK_TERMINAL_CONFLICT", SourceLedger, "A Track cannot be both active and terminal.")
	}
	if input.Ledger.OpenStopCount > 0 {
		result.AllowedActions = append(result.AllowedActions, ActionResolveStop)
	}
	if reason := requiredEvidenceReason(input.GitHub.State, input.GitHub.Value == nil, SourceGitHub); reason != nil {
		result.BlockedReasons = append(result.BlockedReasons, *reason)
		return result
	}
	if reason := requiredEvidenceReason(input.Git.State, input.Git.Value == nil, SourceGit); reason != nil {
		result.BlockedReasons = append(result.BlockedReasons, *reason)
		return result
	}
	github := input.GitHub.Value
	git := input.Git.Value
	if github.RepositoryID != input.Ledger.ProjectRepositoryID {
		return blockedWithoutStage(result, "REPOSITORY_IDENTITY_MISMATCH", SourceReconciliation, "GitHub repository ID does not match the central Project identity.")
	}
	if github.IssueUpdatedAt != input.Ledger.BaselineIssueUpdatedAt {
		return blockedWithoutStage(result, "BASELINE_STALE", SourceReconciliation, "The current GitHub Issue changed after the adopted DeliveryBaseline.")
	}
	if input.Ledger.WorktreeBound && !github.PullRequestEvidenceComplete {
		return blockedWithoutStage(result, "PULL_REQUEST_FACTS_MISSING", SourceGitHub, "Current pull request existence has not been read completely.")
	}
	cleanedAfterMerge := github.PullRequest != nil && github.PullRequest.State == PullRequestMerged && !git.WorktreePresent
	if input.Ledger.WorktreeBound && (!git.WorktreePresent || !git.BindingMatches) && !cleanedAfterMerge {
		return blockedWithoutStage(result, "WORKTREE_BINDING_CONFLICT", SourceReconciliation, "Current Git facts contradict the frozen Track worktree binding.")
	}
	if github.PullRequest != nil && github.PullRequest.State == PullRequestOpen && git.HeadSHA != github.PullRequest.HeadSHA {
		return blockedWithoutStage(result, "PR_HEAD_GIT_MISMATCH", SourceReconciliation, "Current Git worktree head does not match the pull request head.")
	}

	var primary Action
	switch {
	case !input.Ledger.WorktreeBound:
		stage := StageWaitingForWorktree
		result.DerivedStage = &stage
		primary = ActionCreateWorktree
	case input.Ledger.ImplementationRunning:
		stage := StageImplementing
		result.DerivedStage = &stage
	case github.PullRequest == nil:
		stage := StageWorktreeReady
		result.DerivedStage = &stage
		primary = ActionStartImplementation
	case github.PullRequest.State == PullRequestMerged:
		if git.WorktreePresent {
			stage := StageCleanupRequired
			result.DerivedStage = &stage
			primary = ActionCleanupWorktree
		} else {
			stage := StageCloseoutComplete
			result.DerivedStage = &stage
			primary = ActionCloseTrack
		}
	case github.PullRequest.State != PullRequestOpen:
		return blockedWithoutStage(result, "PULL_REQUEST_NOT_DELIVERABLE", SourceGitHub, "The current pull request is closed without verified merge evidence.")
	case input.Ledger.ReviewRunning:
		stage := StageReviewing
		result.DerivedStage = &stage
	case input.Ledger.LatestReview == nil:
		stage := StageReadyForPRReview
		result.DerivedStage = &stage
		primary = ActionStartReview
	default:
		review := input.Ledger.LatestReview
		if review.BaselineID == "" || review.ReviewedHeadSHA == "" {
			return blockedWithoutStage(result, "REVIEW_EVIDENCE_INCOMPLETE", SourceLedger, "The latest Review is missing its Baseline or reviewed head SHA.")
		} else if review.BaselineID != input.Ledger.CurrentBaselineID {
			stage := StageReadyForPRReview
			result.DerivedStage = &stage
			result.BlockedReasons = append(result.BlockedReasons, BlockedReason{Code: "REVIEW_BASELINE_STALE", Source: SourceReconciliation,
				Message: "The latest Review consumed an older DeliveryBaseline."})
			primary = ActionStartReview
		} else if review.ReviewedHeadSHA != github.PullRequest.HeadSHA {
			stage := StageReadyForPRReview
			result.DerivedStage = &stage
			result.BlockedReasons = append(result.BlockedReasons, BlockedReason{Code: "REVIEW_HEAD_STALE", Source: SourceReconciliation,
				Message: "The pull request head changed after the latest Review."})
			primary = ActionStartReview
		} else if review.Verdict == ReviewChangesRequested {
			stage := StageChangesRequested
			result.DerivedStage = &stage
			primary = ActionStartRepair
		} else if review.Verdict == ReviewApproved {
			stage := StageApproved
			result.DerivedStage = &stage
			if testsAllowIntegration(input.Tests, github.PullRequest.HeadSHA, github.PullRequest.ChecksPassed, &result) {
				primary = ActionRequestIntegration
			}
		} else {
			return blockedWithoutStage(result, "REVIEW_VERDICT_CONFLICTING", SourceLedger, "The latest Review verdict is not understood.")
		}
	}
	if requiresOpenIssue(primary) && !github.IssueOpen {
		result.BlockedReasons = append(result.BlockedReasons, BlockedReason{Code: "ISSUE_NOT_OPEN", Source: SourceGitHub,
			Message: "The GitHub Issue is not open for delivery."})
		primary = ""
	}
	if requiresOpenIssue(primary) && !github.DependenciesReady {
		result.BlockedReasons = append(result.BlockedReasons, BlockedReason{Code: "DEPENDENCIES_BLOCKED", Source: SourceGitHub,
			Message: "Current GitHub dependencies do not allow delivery to proceed."})
		primary = ""
	}
	if git.Dirty && (primary == ActionStartReview || primary == ActionRequestIntegration) {
		result.BlockedReasons = append(result.BlockedReasons, BlockedReason{Code: "WORKTREE_DIRTY", Source: SourceGit,
			Message: "The worktree is dirty, so Review or integration evidence would not bind to one reproducible head."})
		primary = ""
	}

	if input.Ledger.OpenStopCount > 0 {
		result.BlockedReasons = append(result.BlockedReasons, BlockedReason{Code: "OPEN_STOP_CONDITION", Source: SourceLedger,
			Message: "An explicit StopCondition pauses delivery actions without replacing the derived Stage."})
		primary = ""
	}
	if requiresRunner(primary) && !runnerAllowed {
		primary = ""
	}
	if primary != "" {
		result.AllowedActions = append(result.AllowedActions, primary)
	}
	return result
}

func requiresOpenIssue(action Action) bool {
	switch action {
	case ActionCreateWorktree, ActionStartImplementation, ActionStartReview, ActionStartRepair, ActionRequestIntegration:
		return true
	default:
		return false
	}
}

func requiresRunner(action Action) bool {
	switch action {
	case ActionCreateWorktree, ActionStartImplementation, ActionStartReview, ActionStartRepair,
		ActionRequestIntegration, ActionCleanupWorktree:
		return true
	default:
		return false
	}
}

func requiredEvidenceReason(state EvidenceState, nilValue bool, source Source) *BlockedReason {
	prefix := string(source)
	switch {
	case state == EvidenceConflicting:
		return &BlockedReason{Code: prefix + "_FACTS_CONFLICTING", Source: source, Message: "Required facts contradict each other."}
	case state == EvidenceUnavailable:
		return &BlockedReason{Code: prefix + "_FACTS_UNAVAILABLE", Source: source, Message: "Required current facts are unavailable."}
	case state != EvidenceComplete || nilValue:
		return &BlockedReason{Code: prefix + "_FACTS_MISSING", Source: source, Message: "Required current facts are missing or null."}
	default:
		return nil
	}
}

func runnerAllowsExecution(evidence Evidence[RunnerFacts], result *Result) bool {
	if evidence.State != EvidenceComplete || evidence.Value == nil || !evidence.Value.Online {
		result.BlockedReasons = append(result.BlockedReasons, BlockedReason{Code: "RUNNER_UNAVAILABLE", Source: SourceRunner,
			Message: "The selected Runner is not currently available."})
		return false
	}
	if !evidence.Value.BindingAllowed || !evidence.Value.EngineAllowed {
		result.BlockedReasons = append(result.BlockedReasons, BlockedReason{Code: "RUNNER_PERMISSION_DENIED", Source: SourceRunner,
			Message: "Runner-local roots, adapter, or executable policy denies this action."})
		return false
	}
	return true
}

func testsAllowIntegration(evidence Evidence[TestFacts], headSHA string, checksPassed bool, result *Result) bool {
	if !checksPassed {
		result.BlockedReasons = append(result.BlockedReasons, BlockedReason{Code: "GITHUB_CHECKS_NOT_PASSED", Source: SourceGitHub,
			Message: "Current GitHub checks have not passed."})
		return false
	}
	if evidence.State != EvidenceComplete || evidence.Value == nil {
		result.BlockedReasons = append(result.BlockedReasons, BlockedReason{Code: "TEST_FACTS_UNAVAILABLE", Source: SourceTests,
			Message: "Required current test evidence is unavailable."})
		return false
	}
	if !evidence.Value.RequiredPassed || evidence.Value.HeadSHA != headSHA {
		result.BlockedReasons = append(result.BlockedReasons, BlockedReason{Code: "TEST_EVIDENCE_STALE_OR_FAILED", Source: SourceReconciliation,
			Message: "Required tests did not pass for the current pull request head."})
		return false
	}
	return true
}

func blockedWithoutStage(result Result, code string, source Source, message string) Result {
	result.DerivedStage = nil
	result.AllowedActions = keepSideActions(result.AllowedActions)
	result.BlockedReasons = append(result.BlockedReasons, BlockedReason{Code: code, Source: source, Message: message})
	return result
}

func keepSideActions(actions []Action) []Action {
	result := make([]Action, 0, len(actions))
	for _, action := range actions {
		if !action.Primary() {
			result = append(result, action)
		}
	}
	return result
}
