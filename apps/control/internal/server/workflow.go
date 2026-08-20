package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/ai-daming/qianshou/apps/control/internal/config"
	"github.com/ai-daming/qianshou/apps/control/internal/dod"
	"github.com/ai-daming/qianshou/apps/control/internal/githubapi"
	"github.com/ai-daming/qianshou/apps/control/internal/ledger"
	"github.com/ai-daming/qianshou/apps/control/internal/localrunner"
	"github.com/ai-daming/qianshou/apps/control/internal/strictjson"
)

const discussionRunTimeout = 30 * time.Minute

type workflowLedger interface {
	projectCatalog
	CreateRunner(context.Context, ledger.NewRunner) (ledger.Runner, error)
	CreateRunnerProjectBinding(context.Context, ledger.NewRunnerProjectBinding) (ledger.RunnerProjectBinding, error)
	GetActiveRunnerProjectBinding(context.Context, string, string) (ledger.RunnerProjectBinding, error)
	EnsureWorkItem(context.Context, string, int) error
	CreateConversation(context.Context, ledger.NewConversation) (ledger.Conversation, error)
	CreateBriefVersion(context.Context, ledger.NewBriefVersion) (ledger.BriefVersion, error)
	GetIssueWorkspace(context.Context, string, int) (ledger.IssueWorkspace, error)
	StartTrack(context.Context, ledger.NewTrack, ledger.NewBaseline) (ledger.DeliveryTrack, ledger.DeliveryBaseline, error)
	AppendBaseline(context.Context, string, ledger.NewBaseline) (ledger.DeliveryBaseline, error)
	QueueRun(context.Context, ledger.NewAgentRun) (ledger.AgentRun, error)
	StartRun(context.Context, string) (ledger.AgentRun, error)
	FinishRun(context.Context, string, ledger.RunState, string) (ledger.AgentRun, error)
	SetVendorSession(context.Context, string, string) error
	AppendSyntheticEvent(context.Context, ledger.NewRunEvent, string) error
	AppendVendorFrame(context.Context, ledger.NewVendorFrame, []ledger.NewRunEvent) error
	ListRunEvents(context.Context, string, int, int) (ledger.RunEventPage, error)
	OpenStopCondition(context.Context, ledger.NewStopCondition) (ledger.StopCondition, error)
	ResolveStopCondition(context.Context, string, string, string) (ledger.StopCondition, error)
}

type workflowExecutor interface {
	Execute(context.Context, localrunner.ExecuteRequest, localrunner.FrameSink) (localrunner.ExecuteResult, error)
	Cancel(string) bool
}

type workflowRuntime struct {
	store    workflowLedger
	facts    factsReader
	config   config.Config
	executor workflowExecutor
	context  context.Context
	timeout  time.Duration
}

func handlerWithWorkflow(store workflowLedger, facts factsReader, cfg config.Config, executor workflowExecutor, runtimeContext context.Context) http.Handler {
	return handlerWithFactsTimeoutAndWorkflow(store, facts, githubFactsTimeout, &workflowRuntime{
		store: store, facts: facts, config: cfg, executor: executor, context: runtimeContext, timeout: githubFactsTimeout,
	})
}

func registerWorkflowRoutes(mux *http.ServeMux, runtime *workflowRuntime) {
	if runtime == nil || runtime.store == nil {
		return
	}
	mux.HandleFunc("POST /api/v1/projects/{projectID}/runner-binding", runtime.createRunnerBinding)
	mux.HandleFunc("GET /api/v1/projects/{projectID}/issues/{issueNumber}/workspace", runtime.getWorkspace)
	mux.HandleFunc("POST /api/v1/projects/{projectID}/issues/{issueNumber}/conversations", runtime.createConversation)
	mux.HandleFunc("POST /api/v1/projects/{projectID}/issues/{issueNumber}/conversations/{conversationID}/runs", runtime.startDiscussionRun)
	mux.HandleFunc("POST /api/v1/projects/{projectID}/issues/{issueNumber}/runs/{runID}/cancel", runtime.cancelRun)
	mux.HandleFunc("GET /api/v1/projects/{projectID}/issues/{issueNumber}/runs/{runID}/events", runtime.listRunEvents)
	mux.HandleFunc("POST /api/v1/projects/{projectID}/issues/{issueNumber}/briefs", runtime.createBrief)
	mux.HandleFunc("POST /api/v1/projects/{projectID}/issues/{issueNumber}/adoptions", runtime.adoptBrief)
	mux.HandleFunc("POST /api/v1/projects/{projectID}/issues/{issueNumber}/stops/{stopID}/resolve", runtime.resolveStop)
}

func (runtime *workflowRuntime) createRunnerBinding(w http.ResponseWriter, r *http.Request) {
	var request struct {
		MainCheckoutPath string `json:"mainCheckoutPath"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	if !requireMutationFields(w, request.MainCheckoutPath) {
		return
	}
	project, repository, ok := runtime.currentProject(w, r, r.PathValue("projectID"))
	if !ok {
		return
	}
	if strings.TrimSpace(runtime.config.Runner.ID) == "" {
		writeError(w, http.StatusConflict, "RUNNER_NOT_CONFIGURED", "The embedded Runner is not configured.")
		return
	}
	existing, existingErr := runtime.store.GetActiveRunnerProjectBinding(r.Context(), runtime.config.Runner.ID, project.ID)
	if existingErr == nil {
		candidate := existing
		candidate.MainCheckoutPath = request.MainCheckoutPath
		canonicalPath, err := localrunner.ValidateMainCheckout(r.Context(), runtime.config, candidate, project.RepositoryID, repository.NameWithOwner)
		if err != nil {
			writeError(w, http.StatusConflict, "RUNNER_BINDING_INVALID", err.Error())
			return
		}
		if existing.MainCheckoutPath != canonicalPath {
			writeError(w, http.StatusConflict, "RUNNER_BINDING_CONFLICT", "This Runner already has a different active main-checkout binding for the Project.")
			return
		}
		if _, err := localrunner.ValidateMainCheckout(r.Context(), runtime.config, existing, project.RepositoryID, repository.NameWithOwner); err != nil {
			writeError(w, http.StatusConflict, "RUNNER_BINDING_INVALID", err.Error())
			return
		}
		writeJSON(w, http.StatusOK, publicBinding(existing))
		return
	}
	if !errors.Is(existingErr, ledger.ErrNotFound) {
		writeError(w, http.StatusInternalServerError, "LEDGER_UNAVAILABLE", "The active Runner binding could not be read.")
		return
	}
	bindingInput := ledger.NewRunnerProjectBinding{ID: stableID("binding", runtime.config.Runner.ID, project.ID, request.MainCheckoutPath),
		RunnerID: runtime.config.Runner.ID, ProjectID: project.ID, MainCheckoutPath: request.MainCheckoutPath,
		RepositoryIDAtBinding: project.RepositoryID}
	candidate := ledger.RunnerProjectBinding{ID: bindingInput.ID, RunnerID: bindingInput.RunnerID, ProjectID: bindingInput.ProjectID,
		MainCheckoutPath: bindingInput.MainCheckoutPath, RepositoryIDAtBinding: bindingInput.RepositoryIDAtBinding}
	canonicalPath, err := localrunner.ValidateMainCheckout(r.Context(), runtime.config, candidate, project.RepositoryID, repository.NameWithOwner)
	if err != nil {
		writeError(w, http.StatusConflict, "RUNNER_BINDING_INVALID", err.Error())
		return
	}
	bindingInput.MainCheckoutPath = canonicalPath
	if _, err := runtime.store.CreateRunner(r.Context(), ledger.NewRunner{ID: runtime.config.Runner.ID, DisplayName: runtime.config.Runner.ID}); err != nil {
		writeLedgerMutationError(w, err, "RUNNER_CONFLICT", "The configured Runner identity conflicts with the ledger.")
		return
	}
	binding, err := runtime.store.CreateRunnerProjectBinding(r.Context(), bindingInput)
	if err != nil {
		writeLedgerMutationError(w, err, "RUNNER_BINDING_CONFLICT", "The main-checkout binding conflicts with an active binding.")
		return
	}
	writeJSON(w, http.StatusCreated, publicBinding(binding))
}

func (runtime *workflowRuntime) createConversation(w http.ResponseWriter, r *http.Request) {
	var request struct {
		EngineID       string `json:"engineId"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	if !requireMutationFields(w, request.EngineID, request.IdempotencyKey) {
		return
	}
	project, repository, _, issueNumber, ok := runtime.currentIssue(w, r)
	if !ok {
		return
	}
	binding, err := runtime.store.GetActiveRunnerProjectBinding(r.Context(), runtime.config.Runner.ID, project.ID)
	if err != nil {
		writeError(w, http.StatusConflict, "RUNNER_BINDING_REQUIRED", "Establish a valid main-checkout binding before starting Discussion.")
		return
	}
	if _, err := localrunner.ResolveDiscussionTarget(r.Context(), runtime.config, binding, project.RepositoryID, repository.NameWithOwner, request.EngineID); err != nil {
		writeError(w, http.StatusConflict, "DISCUSSION_TARGET_INVALID", err.Error())
		return
	}
	if err := runtime.store.EnsureWorkItem(r.Context(), project.ID, issueNumber); err != nil {
		writeLedgerMutationError(w, err, "WORK_ITEM_CONFLICT", "The Issue workspace could not be established.")
		return
	}
	id := stableID("conversation", project.ID, strconv.Itoa(issueNumber), request.IdempotencyKey)
	conversation, err := runtime.store.CreateConversation(r.Context(), ledger.NewConversation{ID: id, ProjectID: project.ID,
		IssueNumber: issueNumber, Role: ledger.RoleDiscussion, EngineID: request.EngineID, RunnerProjectBindingID: binding.ID})
	if err != nil {
		writeLedgerMutationError(w, err, "CONVERSATION_CONFLICT", "The conversation idempotency key conflicts with different evidence.")
		return
	}
	writeJSON(w, http.StatusCreated, publicConversation(conversation, "IDLE"))
}

func (runtime *workflowRuntime) startDiscussionRun(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Prompt         string `json:"prompt"`
		IdempotencyKey string `json:"idempotencyKey"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	if !requireMutationFields(w, request.Prompt, request.IdempotencyKey) {
		return
	}
	project, repository, _, issueNumber, ok := runtime.currentIssue(w, r)
	if !ok {
		return
	}
	workspace, err := runtime.store.GetIssueWorkspace(r.Context(), project.ID, issueNumber)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LEDGER_UNAVAILABLE", "The Issue workspace could not be read.")
		return
	}
	conversation, found := findConversation(workspace.Conversations, r.PathValue("conversationID"))
	if !found || conversation.Role != ledger.RoleDiscussion || conversation.ArchivedAt != nil {
		writeError(w, http.StatusNotFound, "CONVERSATION_NOT_FOUND", "The active Discussion conversation was not found.")
		return
	}
	binding, err := runtime.store.GetActiveRunnerProjectBinding(r.Context(), runtime.config.Runner.ID, project.ID)
	if err != nil || binding.ID != conversation.RunnerProjectBindingID {
		writeError(w, http.StatusConflict, "RUNNER_BINDING_INVALID", "The conversation's Runner binding is missing or no longer active.")
		return
	}
	target, err := localrunner.ResolveDiscussionTarget(r.Context(), runtime.config, binding, project.RepositoryID, repository.NameWithOwner, conversation.EngineID)
	if err != nil {
		writeError(w, http.StatusConflict, "DISCUSSION_TARGET_INVALID", err.Error())
		return
	}
	commandKey := stableID("command", project.ID, strconv.Itoa(issueNumber), request.IdempotencyKey)
	runID := stableID("run", commandKey)
	commandJSON, _ := json.Marshal(map[string]any{"kind": "DISCUSSION_TURN", "promptSHA256": shaText(request.Prompt)})
	run, err := runtime.store.QueueRun(r.Context(), ledger.NewAgentRun{ID: runID, ConversationID: conversation.ID,
		CommandKey: commandKey, CommandJSON: string(commandJSON)})
	if err != nil {
		writeLedgerMutationError(w, err, "RUN_CONFLICT", "The run idempotency key conflicts or this conversation already has an unfinished run.")
		return
	}
	if run.State() != ledger.RunQueued {
		writeJSON(w, http.StatusOK, publicRun(run))
		return
	}
	started, err := runtime.store.StartRun(r.Context(), run.ID)
	if err != nil {
		writeLedgerMutationError(w, err, "RUN_CONFLICT", "The queued run could not be started.")
		return
	}
	userPayload, _ := json.Marshal(map[string]string{"text": request.Prompt})
	if err := runtime.store.AppendSyntheticEvent(r.Context(), ledger.NewRunEvent{Sequence: 1, Kind: ledger.EventUserMessage, PayloadJSON: string(userPayload)}, run.ID); err != nil {
		_, _ = runtime.store.FinishRun(r.Context(), run.ID, ledger.RunFailed, `{"reason":"the user message event could not be persisted"}`)
		writeError(w, http.StatusInternalServerError, "LEDGER_UNAVAILABLE", "The Discussion run evidence could not be persisted.")
		return
	}
	requestCopy := localrunner.ExecuteRequest{RunID: run.ID, Engine: target.Engine, Role: conversation.Role,
		CWD: target.CheckoutPath, Prompt: request.Prompt, SessionID: pointerValue(conversation.VendorSessionID)}
	go runtime.executeDiscussionRun(conversation.ID, requestCopy)
	writeJSON(w, http.StatusAccepted, publicRun(started))
}

func (runtime *workflowRuntime) executeDiscussionRun(conversationID string, request localrunner.ExecuteRequest) {
	ctx, cancel := context.WithTimeout(runtime.context, discussionRunTimeout)
	defer cancel()
	result, err := runtime.executor.Execute(ctx, request, func(frame localrunner.ObservedFrame) error {
		event := frame.Event
		event.Sequence++ // sequence 1 is the user message
		return runtime.store.AppendVendorFrame(ctx, ledger.NewVendorFrame{RunID: request.RunID, Sequence: frame.Sequence,
			RawPayload: frame.Raw, Channel: frame.Channel, ParseStatus: frame.ParseStatus,
			NormalizerVersion: localrunner.NormalizerVersion, ParseError: frame.ParseError}, []ledger.NewRunEvent{event})
	})
	if err != nil {
		state := ledger.RunFailed
		if errors.Is(err, localrunner.ErrCancelled) {
			state = ledger.RunCancelled
		} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			state = ledger.RunInterrupted
		}
		detail, _ := json.Marshal(map[string]string{"reason": publicRunFailure(err)})
		_, _ = runtime.store.FinishRun(context.Background(), request.RunID, state, string(detail))
		return
	}
	if err := runtime.store.SetVendorSession(ctx, conversationID, result.SessionID); err != nil {
		detail, _ := json.Marshal(map[string]string{"reason": "the vendor session identity conflicted with durable conversation evidence"})
		_, _ = runtime.store.FinishRun(context.Background(), request.RunID, ledger.RunFailed, string(detail))
		return
	}
	detail, _ := json.Marshal(map[string]string{"result": result.Text})
	_, _ = runtime.store.FinishRun(context.Background(), request.RunID, ledger.RunCompleted, string(detail))
}

func (runtime *workflowRuntime) cancelRun(w http.ResponseWriter, r *http.Request) {
	projectID, issueNumber, workspace, ok := runtime.localWorkspace(w, r)
	if !ok {
		return
	}
	_ = projectID
	run, found := findRun(workspace.Runs, r.PathValue("runID"))
	if !found {
		writeError(w, http.StatusNotFound, "RUN_NOT_FOUND", "The Discussion run was not found.")
		return
	}
	if run.State() != ledger.RunRunning || !runtime.executor.Cancel(run.ID) {
		writeError(w, http.StatusConflict, "RUN_NOT_CANCELLABLE", "Only a currently owned running process can be cancelled.")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"runId": run.ID, "issueNumber": issueNumber, "cancellationRequested": true})
}

func (runtime *workflowRuntime) listRunEvents(w http.ResponseWriter, r *http.Request) {
	_, _, workspace, ok := runtime.localWorkspace(w, r)
	if !ok {
		return
	}
	if _, found := findRun(workspace.Runs, r.PathValue("runID")); !found {
		writeError(w, http.StatusNotFound, "RUN_NOT_FOUND", "The Discussion run was not found.")
		return
	}
	after, err := nonNegativeQuery(r.URL.Query().Get("after"), 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_CURSOR", "The event cursor must be a non-negative integer.")
		return
	}
	limit, err := positiveQuery(r.URL.Query().Get("limit"), 100)
	if err != nil || limit > 1000 {
		writeError(w, http.StatusBadRequest, "INVALID_LIMIT", "The event page limit must be between 1 and 1000.")
		return
	}
	page, err := runtime.store.ListRunEvents(r.Context(), r.PathValue("runID"), after, limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LEDGER_UNAVAILABLE", "Run events could not be read.")
		return
	}
	events := make([]map[string]any, 0, len(page.Events))
	for _, event := range page.Events {
		events = append(events, map[string]any{"sequence": event.Sequence, "sourceFrameSequence": event.SourceFrameSequence,
			"kind": event.Kind, "payload": json.RawMessage(event.PayloadJSON), "occurredAt": event.OccurredAt})
	}
	writeJSON(w, http.StatusOK, map[string]any{"runId": r.PathValue("runID"), "events": events, "nextCursor": page.NextCursor})
}

func (runtime *workflowRuntime) createBrief(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Content                 string `json:"content"`
		SourceConversationID    string `json:"sourceConversationId"`
		IdempotencyKey          string `json:"idempotencyKey"`
		ExpectedIssueUpdatedAt  string `json:"expectedIssueUpdatedAt"`
		ExpectedIssueBodySHA256 string `json:"expectedIssueBodySha256"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	if !requireMutationFields(w, request.Content, request.SourceConversationID, request.IdempotencyKey, request.ExpectedIssueUpdatedAt) {
		return
	}
	if !validSHA256(request.ExpectedIssueBodySHA256) {
		writeInvalidMutationRequest(w)
		return
	}
	project, _, issue, issueNumber, ok := runtime.currentIssue(w, r)
	if !ok {
		return
	}
	if issue.UpdatedAt == "" {
		writeError(w, http.StatusBadGateway, "GITHUB_FACTS_UNAVAILABLE", "GitHub Issue updatedAt is missing, so the BriefVersion source cannot be frozen.")
		return
	}
	if request.ExpectedIssueUpdatedAt != issue.UpdatedAt || !strings.EqualFold(request.ExpectedIssueBodySHA256, shaText(issue.Body)) {
		writeError(w, http.StatusConflict, "ISSUE_EVIDENCE_STALE", "The GitHub Issue changed before the BriefVersion could be created. Refresh Discussion first.")
		return
	}
	workspace, err := runtime.store.GetIssueWorkspace(r.Context(), project.ID, issueNumber)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LEDGER_UNAVAILABLE", "The Issue workspace could not be read.")
		return
	}
	conversation, found := findConversation(workspace.Conversations, request.SourceConversationID)
	if !found || conversation.Role != ledger.RoleDiscussion {
		writeError(w, http.StatusConflict, "BRIEF_SOURCE_INVALID", "A BriefVersion must come from a Discussion conversation in this Issue workspace.")
		return
	}
	id := stableID("brief", project.ID, strconv.Itoa(issueNumber), request.IdempotencyKey)
	brief, err := runtime.store.CreateBriefVersion(r.Context(), ledger.NewBriefVersion{ID: id, ProjectID: project.ID,
		IssueNumber: issueNumber, Content: request.Content, SourceIssueUpdatedAt: issue.UpdatedAt,
		SourceIssueBodySHA256: shaText(issue.Body)})
	if err != nil {
		writeLedgerMutationError(w, err, "BRIEF_CONFLICT", "The brief idempotency key conflicts with different content.")
		return
	}
	writeJSON(w, http.StatusCreated, publicBrief(brief, "DRAFT"))
}

func (runtime *workflowRuntime) adoptBrief(w http.ResponseWriter, r *http.Request) {
	var request struct {
		BriefVersionID          string          `json:"briefVersionId"`
		AdoptionKey             string          `json:"adoptionKey"`
		ExpectedIssueUpdatedAt  string          `json:"expectedIssueUpdatedAt"`
		ExpectedIssueBodySHA256 string          `json:"expectedIssueBodySha256"`
		IssueDoD                json.RawMessage `json:"issueDoD"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	if !requireMutationFields(w, request.BriefVersionID, request.AdoptionKey, request.ExpectedIssueUpdatedAt) {
		return
	}
	if !validSHA256(request.ExpectedIssueBodySHA256) {
		writeInvalidMutationRequest(w)
		return
	}
	project, _, issue, issueNumber, ok := runtime.currentIssue(w, r)
	if !ok {
		return
	}
	if strings.TrimSpace(issue.UpdatedAt) == "" {
		writeError(w, http.StatusBadGateway, "GITHUB_FACTS_UNAVAILABLE", "GitHub Issue updatedAt is missing, so adoption cannot freeze trustworthy evidence.")
		return
	}
	currentHash := shaText(issue.Body)
	if request.ExpectedIssueUpdatedAt != issue.UpdatedAt || !strings.EqualFold(request.ExpectedIssueBodySHA256, currentHash) {
		writeError(w, http.StatusConflict, "ISSUE_EVIDENCE_STALE", "The GitHub Issue changed after the adoption preview. Refresh Discussion before adopting.")
		return
	}
	var criteria []dod.Criterion
	if len(request.IssueDoD) == 0 || strictjson.Decode(request.IssueDoD, &criteria, true) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ISSUE_DOD", "issueDoD must be an explicit array of structured Issue criteria.")
		return
	}
	if problems := dod.ValidateIssueCriteria(criteria); len(problems) != 0 {
		writeError(w, http.StatusBadRequest, "INVALID_ISSUE_DOD", strings.Join(problems, "; "))
		return
	}
	dodJSON, _ := json.Marshal(criteria)
	workspace, err := runtime.store.GetIssueWorkspace(r.Context(), project.ID, issueNumber)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LEDGER_UNAVAILABLE", "The Issue workspace could not be read before adoption.")
		return
	}
	brief, found := findBrief(workspace.BriefVersions, request.BriefVersionID)
	if !found {
		writeError(w, http.StatusConflict, "BRIEF_NOT_FOUND", "The selected BriefVersion does not belong to this Issue workspace.")
		return
	}
	if brief.SourceIssueUpdatedAt != issue.UpdatedAt || brief.SourceIssueBodySHA256 != currentHash {
		writeError(w, http.StatusConflict, "BRIEF_EVIDENCE_STALE", "The selected BriefVersion was generated from different GitHub Issue evidence. Generate a new BriefVersion before adopting.")
		return
	}
	alreadyAdopted := false
	for _, baseline := range workspace.Baselines {
		if baseline.AdoptionKey == request.AdoptionKey {
			alreadyAdopted = true
		}
	}
	trackID := stableID("track", project.ID, strconv.Itoa(issueNumber))
	baselineInput := ledger.NewBaseline{ID: stableID("baseline", trackID, request.AdoptionKey), AdoptionKey: request.AdoptionKey,
		IssueUpdatedAt: issue.UpdatedAt, IssueBody: issue.Body, BriefVersionID: request.BriefVersionID, ResolvedDoDJSON: string(dodJSON)}
	var baseline ledger.DeliveryBaseline
	if workspace.ActiveTrack == nil {
		_, baseline, err = runtime.store.StartTrack(r.Context(), ledger.NewTrack{ID: trackID, ProjectID: project.ID, IssueNumber: issueNumber}, baselineInput)
	} else {
		baseline, err = runtime.store.AppendBaseline(r.Context(), workspace.ActiveTrack.ID, baselineInput)
	}
	if err != nil {
		writeLedgerMutationError(w, err, "ADOPTION_CONFLICT", "The adoption key, evidence, or active DeliveryTrack conflicts with another adoption.")
		return
	}
	status := http.StatusCreated
	if alreadyAdopted {
		status = http.StatusOK
	}
	writeJSON(w, status, publicBaseline(baseline))
}

func (runtime *workflowRuntime) resolveStop(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Resolution string          `json:"resolution"`
		Outcome    json.RawMessage `json:"outcome"`
	}
	if !decodeRequest(w, r, &request) {
		return
	}
	if !requireMutationFields(w, request.Resolution) {
		return
	}
	if len(request.Outcome) == 0 {
		writeInvalidMutationRequest(w)
		return
	}
	var outcome map[string]any
	if strictjson.Decode(request.Outcome, &outcome, true) != nil || outcome == nil {
		writeInvalidMutationRequest(w)
		return
	}
	_, _, workspace, ok := runtime.localWorkspace(w, r)
	if !ok {
		return
	}
	stop, found := findStop(workspace.StopConditions, r.PathValue("stopID"))
	if !found {
		writeError(w, http.StatusNotFound, "STOP_NOT_FOUND", "The StopCondition was not found in this Issue workspace.")
		return
	}
	resolved, err := runtime.store.ResolveStopCondition(r.Context(), stop.ID, request.Resolution, string(request.Outcome))
	if err != nil {
		writeLedgerMutationError(w, err, "STOP_RESOLUTION_CONFLICT", "The StopCondition already has a different one-shot resolution.")
		return
	}
	writeJSON(w, http.StatusOK, publicStop(resolved))
}

func (runtime *workflowRuntime) getWorkspace(w http.ResponseWriter, r *http.Request) {
	projectID, issueNumber, workspace, ok := runtime.localWorkspace(w, r)
	if !ok {
		return
	}
	project, err := runtime.store.GetProject(r.Context(), projectID)
	if err != nil {
		writeError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "The central Project was not found.")
		return
	}
	githubStatus := "CURRENT"
	blockedReasons := []map[string]string{}
	var issue *githubapi.Issue
	ctx, cancel := context.WithTimeout(r.Context(), runtime.timeout)
	defer cancel()
	repository, repoErr := runtime.facts.GetRepositoryByID(ctx, project.RepositoryID)
	if repoErr != nil || repository.ID != project.RepositoryID {
		githubStatus = "UNAVAILABLE"
		blockedReasons = append(blockedReasons, map[string]string{"code": "GITHUB_FACTS_UNAVAILABLE", "message": "Current GitHub repository and Issue facts are unavailable."})
	} else {
		current, issueErr := runtime.facts.GetIssue(ctx, repository.NameWithOwner, issueNumber)
		if issueErr != nil || current.Number != issueNumber || current.UpdatedAt == "" {
			githubStatus = "UNAVAILABLE"
			blockedReasons = append(blockedReasons, map[string]string{"code": "GITHUB_FACTS_UNAVAILABLE", "message": "Current GitHub repository and Issue facts are unavailable."})
		} else {
			issue = &current
			if len(workspace.Baselines) > 0 {
				latest := workspace.Baselines[len(workspace.Baselines)-1]
				if shaText(current.Body) != latest.IssueBodySHA256 {
					stop := scopeChangeStop(workspace.StopConditions, latest.ID)
					if stop == nil && workspace.ActiveTrack != nil {
						evidence, _ := json.Marshal(map[string]string{"baselineIssueBodySha256": latest.IssueBodySHA256,
							"currentIssueBodySha256": shaText(current.Body), "currentIssueUpdatedAt": current.UpdatedAt,
							"derivedStage": "WAITING_FOR_WORKTREE"})
						opened, openErr := runtime.store.OpenStopCondition(r.Context(), ledger.NewStopCondition{
							ID: stableID("stop", "scope-change", latest.ID), TrackID: workspace.ActiveTrack.ID, BaselineID: latest.ID,
							Kind: "SCOPE_CHANGE", Reason: "The current GitHub Issue body differs from the adopted DeliveryBaseline.", EvidenceJSON: string(evidence)})
						if openErr != nil {
							writeError(w, http.StatusInternalServerError, "LEDGER_UNAVAILABLE", "Scope drift was detected but its StopCondition could not be persisted.")
							return
						}
						workspace.StopConditions = append(workspace.StopConditions, opened)
					}
				}
			}
		}
	}
	writeJSON(w, http.StatusOK, publicWorkspace(projectID, issueNumber, githubStatus, issue, workspace, runtime.config, blockedReasons))
}

func (runtime *workflowRuntime) currentProject(w http.ResponseWriter, r *http.Request, projectID string) (ledger.Project, githubapi.Repository, bool) {
	project, err := runtime.store.GetProject(r.Context(), projectID)
	if errors.Is(err, ledger.ErrNotFound) || project.ArchivedAt != nil {
		writeError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "The central Project was not found.")
		return ledger.Project{}, githubapi.Repository{}, false
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LEDGER_UNAVAILABLE", "The central Project Catalog could not be read.")
		return ledger.Project{}, githubapi.Repository{}, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), runtime.timeout)
	defer cancel()
	repository, err := runtime.facts.GetRepositoryByID(ctx, project.RepositoryID)
	if err != nil || repository.ID != project.RepositoryID {
		writeGitHubFactsError(w, ctx, err, "Current GitHub repository identity could not be verified.")
		return ledger.Project{}, githubapi.Repository{}, false
	}
	return project, repository, true
}

func (runtime *workflowRuntime) currentIssue(w http.ResponseWriter, r *http.Request) (ledger.Project, githubapi.Repository, githubapi.Issue, int, bool) {
	issueNumber, err := positiveNumber(r.PathValue("issueNumber"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ISSUE_NUMBER", "Issue number must be a positive integer.")
		return ledger.Project{}, githubapi.Repository{}, githubapi.Issue{}, 0, false
	}
	project, repository, ok := runtime.currentProject(w, r, r.PathValue("projectID"))
	if !ok {
		return ledger.Project{}, githubapi.Repository{}, githubapi.Issue{}, 0, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), runtime.timeout)
	defer cancel()
	issue, err := runtime.facts.GetIssue(ctx, repository.NameWithOwner, issueNumber)
	if err != nil || issue.Number != issueNumber {
		writeGitHubFactsError(w, ctx, err, "Current GitHub Issue facts could not be read completely.")
		return ledger.Project{}, githubapi.Repository{}, githubapi.Issue{}, 0, false
	}
	return project, repository, issue, issueNumber, true
}

func (runtime *workflowRuntime) localWorkspace(w http.ResponseWriter, r *http.Request) (string, int, ledger.IssueWorkspace, bool) {
	issueNumber, err := positiveNumber(r.PathValue("issueNumber"))
	if err != nil {
		writeError(w, http.StatusBadRequest, "INVALID_ISSUE_NUMBER", "Issue number must be a positive integer.")
		return "", 0, ledger.IssueWorkspace{}, false
	}
	project, err := runtime.store.GetProject(r.Context(), r.PathValue("projectID"))
	if errors.Is(err, ledger.ErrNotFound) || project.ArchivedAt != nil {
		writeError(w, http.StatusNotFound, "PROJECT_NOT_FOUND", "The central Project was not found.")
		return "", 0, ledger.IssueWorkspace{}, false
	}
	workspace, err := runtime.store.GetIssueWorkspace(r.Context(), project.ID, issueNumber)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "LEDGER_UNAVAILABLE", "The Issue workspace could not be read.")
		return "", 0, ledger.IssueWorkspace{}, false
	}
	return project.ID, issueNumber, workspace, true
}

func decodeRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	body, err := io.ReadAll(io.LimitReader(r.Body, requestBodyLimit+1))
	if err != nil || len(body) > requestBodyLimit || strictjson.Decode(body, target, true) != nil {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "The request body is invalid, ambiguous, or too large.")
		return false
	}
	return true
}

func requireMutationFields(w http.ResponseWriter, values ...string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			writeInvalidMutationRequest(w)
			return false
		}
	}
	return true
}

func writeInvalidMutationRequest(w http.ResponseWriter) {
	writeError(w, http.StatusBadRequest, "INVALID_REQUEST", "Required mutation evidence must be non-empty and structurally valid.")
}

func validSHA256(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func stableID(prefix string, values ...string) string {
	digest := sha256.Sum256([]byte(strings.Join(values, "\x00")))
	return prefix + ":" + hex.EncodeToString(digest[:12])
}

func shaText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func publicBinding(value ledger.RunnerProjectBinding) map[string]any {
	return map[string]any{"id": value.ID, "runnerId": value.RunnerID, "projectId": value.ProjectID,
		"mainCheckoutPath": value.MainCheckoutPath, "repositoryIdAtBinding": value.RepositoryIDAtBinding, "createdAt": value.CreatedAt}
}

func publicConversation(value ledger.Conversation, status string) map[string]any {
	return map[string]any{"id": value.ID, "role": value.Role, "engineId": value.EngineID, "runnerProjectBindingId": value.RunnerProjectBindingID,
		"vendorSessionEstablished": value.VendorSessionID != nil, "status": status, "createdAt": value.CreatedAt, "archivedAt": value.ArchivedAt}
}

func publicBrief(value ledger.BriefVersion, status string) map[string]any {
	return map[string]any{"id": value.ID, "content": value.Content, "contentSha256": value.ContentSHA256,
		"sourceIssueUpdatedAt": value.SourceIssueUpdatedAt, "sourceIssueBodySha256": value.SourceIssueBodySHA256,
		"status": status, "createdAt": value.CreatedAt}
}

func publicRun(value ledger.AgentRun) map[string]any {
	return map[string]any{"id": value.ID, "conversationId": value.ConversationID, "state": value.State(), "queuedAt": value.QueuedAt,
		"startedAt": value.StartedAt, "terminalAt": value.TerminalAt, "terminalDetail": rawJSONPointer(value.TerminalDetailJSON)}
}

func publicBaseline(value ledger.DeliveryBaseline) map[string]any {
	return map[string]any{"id": value.ID, "trackId": value.TrackID, "sequence": value.Sequence, "adoptionKey": value.AdoptionKey,
		"issueUpdatedAt": value.IssueUpdatedAt, "issueBody": value.IssueBody, "issueBodySha256": value.IssueBodySHA256,
		"briefVersionId": value.BriefVersionID, "issueDoD": json.RawMessage(value.ResolvedDoDJSON),
		"payloadSha256": value.PayloadSHA256, "adoptedAt": value.CreatedAt}
}

func publicStop(value ledger.StopCondition) map[string]any {
	return map[string]any{"id": value.ID, "trackId": value.TrackID, "baselineId": value.BaselineID, "kind": value.Kind,
		"reason": value.Reason, "evidence": json.RawMessage(value.EvidenceJSON), "state": map[bool]string{true: "OPEN", false: "RESOLVED"}[value.Open()],
		"createdAt": value.CreatedAt, "resolution": value.Resolution, "outcome": rawJSONPointer(value.OutcomeJSON), "resolvedAt": value.ResolvedAt}
}

func publicWorkspace(projectID string, issueNumber int, githubStatus string, issue *githubapi.Issue, workspace ledger.IssueWorkspace, cfg config.Config, blocked []map[string]string) map[string]any {
	latestRun := map[string]ledger.AgentRun{}
	for _, run := range workspace.Runs {
		latestRun[run.ConversationID] = run
	}
	conversations := make([]map[string]any, 0, len(workspace.Conversations))
	for _, conversation := range workspace.Conversations {
		status := "IDLE"
		if run, ok := latestRun[conversation.ID]; ok {
			status = string(run.State())
		}
		conversations = append(conversations, publicConversation(conversation, status))
	}
	currentBrief := ""
	usedBriefs := map[string]bool{}
	if len(workspace.Baselines) > 0 {
		currentBrief = workspace.Baselines[len(workspace.Baselines)-1].BriefVersionID
		for _, baseline := range workspace.Baselines {
			usedBriefs[baseline.BriefVersionID] = true
		}
	}
	briefs := make([]map[string]any, 0, len(workspace.BriefVersions))
	for _, brief := range workspace.BriefVersions {
		status := "DRAFT"
		if brief.ID == currentBrief {
			status = "ADOPTED"
		} else if usedBriefs[brief.ID] {
			status = "SUPERSEDED"
		}
		briefs = append(briefs, publicBrief(brief, status))
	}
	runs := make([]map[string]any, 0, len(workspace.Runs))
	for _, run := range workspace.Runs {
		runs = append(runs, publicRun(run))
	}
	baselines := make([]map[string]any, 0, len(workspace.Baselines))
	for _, baseline := range workspace.Baselines {
		baselines = append(baselines, publicBaseline(baseline))
	}
	stops := make([]map[string]any, 0, len(workspace.StopConditions))
	deliveryPaused := false
	for _, stop := range workspace.StopConditions {
		stops = append(stops, publicStop(stop))
		if stop.Open() {
			deliveryPaused = true
		}
	}
	engines := make([]map[string]string, 0, len(cfg.Engines))
	for _, engine := range cfg.Engines {
		engines = append(engines, map[string]string{"id": engine.ID, "adapter": engine.Adapter})
	}
	var activeTrack any
	if workspace.ActiveTrack != nil {
		activeTrack = map[string]any{"id": workspace.ActiveTrack.ID, "lifecycle": "ACTIVE", "createdAt": workspace.ActiveTrack.CreatedAt}
	}
	var currentIssueBodySHA256 any
	if issue != nil {
		currentIssueBodySHA256 = shaText(issue.Body)
	}
	return map[string]any{"projectId": projectID, "issueNumber": issueNumber, "githubStatus": githubStatus, "issue": issue,
		"currentIssueBodySha256": currentIssueBodySHA256,
		"engines":                engines, "conversations": conversations, "briefVersions": briefs, "runs": runs,
		"delivery":       map[string]any{"activeTrack": activeTrack, "baselines": baselines, "deliveryPaused": deliveryPaused},
		"stopConditions": stops, "blockedReasons": blocked}
}

func writeLedgerMutationError(w http.ResponseWriter, err error, conflictCode, message string) {
	if errors.Is(err, ledger.ErrConflict) {
		writeError(w, http.StatusConflict, conflictCode, message)
		return
	}
	if errors.Is(err, ledger.ErrInvariant) {
		writeError(w, http.StatusBadRequest, "INVALID_REQUEST", message)
		return
	}
	writeError(w, http.StatusInternalServerError, "LEDGER_UNAVAILABLE", "The central ledger could not persist the requested action.")
}

func findConversation(values []ledger.Conversation, id string) (ledger.Conversation, bool) {
	for _, value := range values {
		if value.ID == id {
			return value, true
		}
	}
	return ledger.Conversation{}, false
}

func findRun(values []ledger.AgentRun, id string) (ledger.AgentRun, bool) {
	for _, value := range values {
		if value.ID == id {
			return value, true
		}
	}
	return ledger.AgentRun{}, false
}

func findBrief(values []ledger.BriefVersion, id string) (ledger.BriefVersion, bool) {
	for _, value := range values {
		if value.ID == id {
			return value, true
		}
	}
	return ledger.BriefVersion{}, false
}

func findStop(values []ledger.StopCondition, id string) (ledger.StopCondition, bool) {
	for _, value := range values {
		if value.ID == id {
			return value, true
		}
	}
	return ledger.StopCondition{}, false
}

func scopeChangeStop(values []ledger.StopCondition, baselineID string) *ledger.StopCondition {
	for index := range values {
		if values[index].Kind == "SCOPE_CHANGE" && pointerValue(values[index].BaselineID) == baselineID {
			return &values[index]
		}
	}
	return nil
}

func pointerValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func rawJSONPointer(value *string) any {
	if value == nil {
		return nil
	}
	return json.RawMessage(*value)
}

func nonNegativeQuery(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("invalid non-negative integer")
	}
	return value, nil
}

func positiveQuery(raw string, fallback int) (int, error) {
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid positive integer")
	}
	return value, nil
}

func publicRunFailure(err error) string {
	if errors.Is(err, localrunner.ErrCancelled) {
		return "cancelled by explicit user action"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "the configured Agent exceeded the run deadline"
	}
	return "the configured Agent failed; inspect offline server diagnostics"
}
