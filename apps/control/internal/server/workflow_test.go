package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ai-daming/qianshou/apps/control/internal/capability"
	"github.com/ai-daming/qianshou/apps/control/internal/config"
	"github.com/ai-daming/qianshou/apps/control/internal/githubapi"
	"github.com/ai-daming/qianshou/apps/control/internal/ledger"
	"github.com/ai-daming/qianshou/apps/control/internal/localrunner"
)

type fakeWorkflowExecutor struct{}

func (fakeWorkflowExecutor) Execute(_ context.Context, request localrunner.ExecuteRequest, sink localrunner.FrameSink) (localrunner.ExecuteResult, error) {
	frames := []localrunner.ObservedFrame{
		{Sequence: 1, Raw: []byte("{\"type\":\"thread.started\",\"thread_id\":\"thread-1\"}\n"), Channel: "stdout", ParseStatus: ledger.FrameParsed,
			Event: ledger.NewRunEvent{Sequence: 1, Kind: ledger.EventStatus, PayloadJSON: `{"sessionId":"thread-1"}`}},
		{Sequence: 2, Raw: []byte("{\"type\":\"item.completed\",\"item\":{\"type\":\"agent_message\",\"text\":\"brief answer\"}}\n"), Channel: "stdout", ParseStatus: ledger.FrameParsed,
			Event: ledger.NewRunEvent{Sequence: 2, Kind: ledger.EventAgentMessage, PayloadJSON: `{"text":"brief answer"}`}},
	}
	for _, frame := range frames {
		if err := sink(frame); err != nil {
			return localrunner.ExecuteResult{}, err
		}
	}
	return localrunner.ExecuteResult{SessionID: "thread-1", Text: "brief answer"}, nil
}

func (fakeWorkflowExecutor) Cancel(string) bool { return true }

type blockingWorkflowExecutor struct {
	started chan struct{}
	cancel  chan struct{}
	once    sync.Once
}

type staticWorkflowExecutor struct {
	result localrunner.ExecuteResult
	err    error
}

func (executor staticWorkflowExecutor) Execute(context.Context, localrunner.ExecuteRequest, localrunner.FrameSink) (localrunner.ExecuteResult, error) {
	return executor.result, executor.err
}

func (staticWorkflowExecutor) Cancel(string) bool { return false }

func newBlockingWorkflowExecutor() *blockingWorkflowExecutor {
	return &blockingWorkflowExecutor{started: make(chan struct{}), cancel: make(chan struct{})}
}

func (executor *blockingWorkflowExecutor) Execute(_ context.Context, _ localrunner.ExecuteRequest, _ localrunner.FrameSink) (localrunner.ExecuteResult, error) {
	close(executor.started)
	<-executor.cancel
	return localrunner.ExecuteResult{}, localrunner.ErrCancelled
}

func (executor *blockingWorkflowExecutor) Cancel(string) bool {
	executor.once.Do(func() { close(executor.cancel) })
	return true
}

func TestDiscussionBriefAdoptionDriftFlow(t *testing.T) {
	store := testCatalog(t)
	addProject(t, store, "qianshou", 101, "ai-daming/qianshou")
	root := t.TempDir()
	checkout := filepath.Join(root, "qianshou")
	initWorkflowRepository(t, checkout)
	facts := testFacts()
	facts.issue = githubapi.Issue{Number: 6, Title: "Discussion", State: "OPEN", Labels: []string{"type:feature"},
		Body: "Goal v1", UpdatedAt: "2026-08-19T15:09:52Z", Dependency: githubapi.Dependency{Status: githubapi.DependencyReady}}
	cfg := config.Config{Version: 1, Runner: config.Runner{ID: "runner-1", AllowedRoots: []string{root}},
		Engines: []config.Engine{{ID: "codex", Adapter: "codex", Command: "codex"}}}
	h := handlerWithWorkflow(store, facts, cfg, fakeWorkflowExecutor{}, context.Background())
	contract := loadOpenAPIContract(t)
	assertContract := func(method, path string, response *httptest.ResponseRecorder) {
		t.Helper()
		request := httptest.NewRequest(method, "http://127.0.0.1:41727"+path, nil)
		if err := validateRecordedResponse(t.Context(), contract, request, response); err != nil {
			t.Fatalf("%s %s response does not match OpenAPI: %v\n%s", method, path, err, response.Body.String())
		}
	}

	binding := postJSON(t, h, "/api/v1/projects/qianshou/runner-binding", `{"mainCheckoutPath":`+jsonString(checkout)+`}`)
	if binding.Code != http.StatusCreated || !strings.Contains(binding.Body.String(), `"runnerId":"runner-1"`) {
		t.Fatalf("binding = %d %s", binding.Code, binding.Body.String())
	}
	assertContract(http.MethodPost, "/api/v1/projects/qianshou/runner-binding", binding)
	bindingRetry := postJSON(t, h, "/api/v1/projects/qianshou/runner-binding", `{"mainCheckoutPath":`+jsonString(checkout)+`}`)
	if bindingRetry.Code != http.StatusOK {
		t.Fatalf("idempotent binding = %d %s", bindingRetry.Code, bindingRetry.Body.String())
	}
	conversation := postJSON(t, h, "/api/v1/projects/qianshou/issues/6/conversations", `{"engineId":"codex","idempotencyKey":"conversation-1"}`)
	if conversation.Code != http.StatusCreated {
		t.Fatalf("conversation = %d %s", conversation.Code, conversation.Body.String())
	}
	assertContract(http.MethodPost, "/api/v1/projects/qianshou/issues/6/conversations", conversation)
	var conversationBody struct {
		ID string `json:"id"`
	}
	decodeResponse(t, conversation, &conversationBody)
	run := postJSON(t, h, "/api/v1/projects/qianshou/issues/6/conversations/"+conversationBody.ID+"/runs", `{"prompt":"help me refine","idempotencyKey":"run-1"}`)
	if run.Code != http.StatusAccepted {
		t.Fatalf("run = %d %s", run.Code, run.Body.String())
	}
	assertContract(http.MethodPost, "/api/v1/projects/qianshou/issues/6/conversations/"+conversationBody.ID+"/runs", run)
	var runBody struct {
		ID string `json:"id"`
	}
	decodeResponse(t, run, &runBody)

	deadline := time.Now().Add(2 * time.Second)
	for {
		workspace := getJSON(t, h, "/api/v1/projects/qianshou/issues/6/workspace", nil)
		if strings.Contains(workspace.Body.String(), `"state":"COMPLETED"`) {
			assertContract(http.MethodGet, "/api/v1/projects/qianshou/issues/6/workspace", workspace)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("run did not finish: %s", workspace.Body.String())
		}
		time.Sleep(10 * time.Millisecond)
	}

	events := getJSON(t, h, "/api/v1/projects/qianshou/issues/6/runs/"+runBody.ID+"/events?after=0&limit=2", nil)
	if events.Code != http.StatusOK || !strings.Contains(events.Body.String(), `"nextCursor":2`) {
		t.Fatalf("events = %d %s", events.Code, events.Body.String())
	}
	assertContract(http.MethodGet, "/api/v1/projects/qianshou/issues/6/runs/"+runBody.ID+"/events?after=0&limit=2", events)
	brief := postJSON(t, h, "/api/v1/projects/qianshou/issues/6/briefs", `{"content":"adopt this brief","sourceConversationId":`+jsonString(conversationBody.ID)+`,"idempotencyKey":"brief-1","expectedIssueUpdatedAt":"2026-08-19T15:09:52Z","expectedIssueBodySha256":`+jsonString(testSHA("Goal v1"))+`}`)
	if brief.Code != http.StatusCreated {
		t.Fatalf("brief = %d %s", brief.Code, brief.Body.String())
	}
	assertContract(http.MethodPost, "/api/v1/projects/qianshou/issues/6/briefs", brief)
	var briefBody struct {
		ID string `json:"id"`
	}
	decodeResponse(t, brief, &briefBody)

	adoptionRequest := `{"briefVersionId":` + jsonString(briefBody.ID) + `,"adoptionKey":"adopt-1","expectedIssueUpdatedAt":"2026-08-19T15:09:52Z","expectedIssueBodySha256":` + jsonString(testSHA("Goal v1")) + `,"issueDoD":[]}`
	adoption := postJSON(t, h, "/api/v1/projects/qianshou/issues/6/adoptions", adoptionRequest)
	if adoption.Code != http.StatusCreated || !strings.Contains(adoption.Body.String(), `"issueBody":"Goal v1"`) {
		t.Fatalf("adoption = %d %s", adoption.Code, adoption.Body.String())
	}
	assertContract(http.MethodPost, "/api/v1/projects/qianshou/issues/6/adoptions", adoption)
	retry := postJSON(t, h, "/api/v1/projects/qianshou/issues/6/adoptions", adoptionRequest)
	if retry.Code != http.StatusOK {
		t.Fatalf("idempotent adoption = %d %s", retry.Code, retry.Body.String())
	}
	authority := getJSON(t, h, "/api/v1/projects/qianshou/issues/6/workspace", nil)
	var authorityBody struct {
		DerivedStage    *string  `json:"derivedStage"`
		AllowedActions  []string `json:"allowedActions"`
		EvidenceSources []struct {
			Source     string `json:"source"`
			State      string `json:"state"`
			ObservedAt string `json:"observedAt"`
		} `json:"evidenceSources"`
		BlockedReasons []struct {
			Code   string `json:"code"`
			Source string `json:"source"`
		} `json:"blockedReasons"`
	}
	decodeResponse(t, authority, &authorityBody)
	if authorityBody.DerivedStage == nil || *authorityBody.DerivedStage != "WAITING_FOR_WORKTREE" ||
		!containsString(authorityBody.AllowedActions, "CREATE_WORKTREE") {
		t.Fatalf("authoritative workspace = %+v\n%s", authorityBody, authority.Body.String())
	}
	if !completeEvidenceSource(authorityBody.EvidenceSources, "LEDGER") ||
		!completeEvidenceSource(authorityBody.EvidenceSources, "GITHUB") ||
		!completeEvidenceSource(authorityBody.EvidenceSources, "GIT") ||
		!completeEvidenceSource(authorityBody.EvidenceSources, "RUNNER") {
		t.Fatalf("evidence sources = %+v", authorityBody.EvidenceSources)
	}

	facts.issue.Dependency = githubapi.Dependency{Status: githubapi.DependencyError,
		Error: &githubapi.DependencyErrorDetail{Code: "DEPENDENCY_FACTS_UNAVAILABLE", Message: "dependency node missing"}}
	incomplete := getJSON(t, h, "/api/v1/projects/qianshou/issues/6/workspace", nil)
	decodeResponse(t, incomplete, &authorityBody)
	if authorityBody.DerivedStage != nil || containsString(authorityBody.AllowedActions, "CREATE_WORKTREE") ||
		!hasPublicReason(authorityBody.BlockedReasons, "GITHUB_FACTS_MISSING", "GITHUB") {
		t.Fatalf("incomplete dependency evidence did not fail closed: %+v\n%s", authorityBody, incomplete.Body.String())
	}
	facts.issue.Dependency = githubapi.Dependency{Status: githubapi.DependencyReady}
	conflictingAdoption := postJSON(t, h, "/api/v1/projects/qianshou/issues/6/adoptions", `{"briefVersionId":`+jsonString(briefBody.ID)+`,"adoptionKey":"adopt-1","expectedIssueUpdatedAt":"2026-08-19T15:09:52Z","expectedIssueBodySha256":`+jsonString(testSHA("Goal v1"))+`,"issueDoD":[{"id":"D-1","description":"different","verificationMethod":"PR_REVIEW","requiredEvidence":"review","required":true}]}`)
	if conflictingAdoption.Code != http.StatusConflict {
		t.Fatalf("conflicting adoption = %d %s", conflictingAdoption.Code, conflictingAdoption.Body.String())
	}

	facts.issue.Body = "Goal v2"
	facts.issue.UpdatedAt = "2026-08-19T16:00:00Z"
	staleBrief := postJSON(t, h, "/api/v1/projects/qianshou/issues/6/adoptions", `{"briefVersionId":`+jsonString(briefBody.ID)+`,"adoptionKey":"adopt-stale-brief","expectedIssueUpdatedAt":"2026-08-19T16:00:00Z","expectedIssueBodySha256":`+jsonString(testSHA("Goal v2"))+`,"issueDoD":[]}`)
	if staleBrief.Code != http.StatusConflict || !strings.Contains(staleBrief.Body.String(), `"code":"BRIEF_EVIDENCE_STALE"`) {
		t.Fatalf("stale brief adoption = %d %s", staleBrief.Code, staleBrief.Body.String())
	}
	for i := 0; i < 2; i++ {
		workspace := getJSON(t, h, "/api/v1/projects/qianshou/issues/6/workspace", nil)
		if workspace.Code != http.StatusOK || strings.Count(workspace.Body.String(), `"kind":"SCOPE_CHANGE"`) != 1 ||
			!strings.Contains(workspace.Body.String(), `"deliveryPaused":true`) ||
			!strings.Contains(workspace.Body.String(), `"derivedStage":"WAITING_FOR_WORKTREE"`) {
			t.Fatalf("drift workspace %d = %d %s", i, workspace.Code, workspace.Body.String())
		}
	}
	stored, err := store.GetIssueWorkspace(context.Background(), "qianshou", 6)
	if err != nil || len(stored.StopConditions) != 1 {
		t.Fatalf("stored stops = %+v, %v", stored.StopConditions, err)
	}
	stopID := stored.StopConditions[0].ID
	resolvePath := "/api/v1/projects/qianshou/issues/6/stops/" + stopID + "/resolve"
	unsupportedResolution := postJSON(t, h, resolvePath, `{"resolution":"BOGUS","outcome":{}}`)
	assertWorkflowError(t, unsupportedResolution, http.StatusBadRequest, "INVALID_REQUEST")
	resolved := postJSON(t, h, resolvePath, `{"resolution":"ADOPT_NEW_BASELINE","outcome":{"note":"adopt a new brief"}}`)
	if resolved.Code != http.StatusOK || !strings.Contains(resolved.Body.String(), `"state":"RESOLVED"`) {
		t.Fatalf("resolved stop = %d %s", resolved.Code, resolved.Body.String())
	}
	assertContract(http.MethodPost, resolvePath, resolved)
	resolvedRetry := postJSON(t, h, resolvePath, `{"resolution":"ADOPT_NEW_BASELINE","outcome":{"note":"adopt a new brief"}}`)
	if resolvedRetry.Code != http.StatusOK {
		t.Fatalf("idempotent stop resolution = %d %s", resolvedRetry.Code, resolvedRetry.Body.String())
	}
	conflictingResolution := postJSON(t, h, resolvePath, `{"resolution":"CONTINUE","outcome":{"note":"different"}}`)
	if conflictingResolution.Code != http.StatusConflict {
		t.Fatalf("conflicting stop resolution = %d %s", conflictingResolution.Code, conflictingResolution.Body.String())
	}
	facts.err = context.DeadlineExceeded
	unavailable := getJSON(t, h, "/api/v1/projects/qianshou/issues/6/workspace", nil)
	decodeResponse(t, unavailable, &authorityBody)
	if unavailable.Code != http.StatusOK || authorityBody.DerivedStage != nil ||
		!evidenceSourceHasState(authorityBody.EvidenceSources, "GITHUB", "UNAVAILABLE") ||
		!hasPublicReason(authorityBody.BlockedReasons, "GITHUB_FACTS_UNAVAILABLE", "GITHUB") {
		t.Fatalf("unavailable workspace = %d %s", unavailable.Code, unavailable.Body.String())
	}
	assertContract(http.MethodGet, "/api/v1/projects/qianshou/issues/6/workspace", unavailable)
	stored, _ = store.GetIssueWorkspace(context.Background(), "qianshou", 6)
	if len(stored.StopConditions) != 1 {
		t.Fatalf("GitHub failure persisted another stop: %+v", stored.StopConditions)
	}
}

func TestWorkflowRejectsBlankMutationEvidenceBeforeWriting(t *testing.T) {
	store := testCatalog(t)
	addProject(t, store, "qianshou", 101, "ai-daming/qianshou")
	root := t.TempDir()
	checkout := filepath.Join(root, "qianshou")
	initWorkflowRepository(t, checkout)
	facts := testFacts()
	facts.issue = githubapi.Issue{Number: 6, Title: "Discussion", State: "OPEN", Body: "Goal v1",
		UpdatedAt: "2026-08-19T15:09:52Z", Dependency: githubapi.Dependency{Status: githubapi.DependencyReady}}
	cfg := config.Config{Version: 1, Runner: config.Runner{ID: "runner-1", AllowedRoots: []string{root}},
		Engines: []config.Engine{{ID: "codex", Adapter: "codex", Command: "codex"}}}
	h := handlerWithWorkflow(store, facts, cfg, fakeWorkflowExecutor{}, context.Background())

	blankBinding := postJSON(t, h, "/api/v1/projects/qianshou/runner-binding", `{"mainCheckoutPath":" "}`)
	assertWorkflowError(t, blankBinding, http.StatusBadRequest, "INVALID_REQUEST")

	binding := postJSON(t, h, "/api/v1/projects/qianshou/runner-binding", `{"mainCheckoutPath":`+jsonString(checkout)+`}`)
	if binding.Code != http.StatusCreated {
		t.Fatalf("binding = %d %s", binding.Code, binding.Body.String())
	}
	blankConversation := postJSON(t, h, "/api/v1/projects/qianshou/issues/6/conversations", `{"engineId":"codex","idempotencyKey":" "}`)
	assertWorkflowError(t, blankConversation, http.StatusBadRequest, "INVALID_REQUEST")

	conversation := postJSON(t, h, "/api/v1/projects/qianshou/issues/6/conversations", `{"engineId":"codex","idempotencyKey":"conversation-1"}`)
	var conversationBody struct {
		ID string `json:"id"`
	}
	decodeResponse(t, conversation, &conversationBody)
	blankRun := postJSON(t, h, "/api/v1/projects/qianshou/issues/6/conversations/"+conversationBody.ID+"/runs", `{"prompt":" ","idempotencyKey":"run-blank"}`)
	assertWorkflowError(t, blankRun, http.StatusBadRequest, "INVALID_REQUEST")

	blankBrief := postJSON(t, h, "/api/v1/projects/qianshou/issues/6/briefs", `{"content":" ","sourceConversationId":`+jsonString(conversationBody.ID)+`,"idempotencyKey":"brief-blank","expectedIssueUpdatedAt":"2026-08-19T15:09:52Z","expectedIssueBodySha256":`+jsonString(testSHA("Goal v1"))+`}`)
	assertWorkflowError(t, blankBrief, http.StatusBadRequest, "INVALID_REQUEST")

	workspace, err := store.GetIssueWorkspace(context.Background(), "qianshou", 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspace.Conversations) != 1 || len(workspace.Runs) != 0 || len(workspace.BriefVersions) != 0 {
		t.Fatalf("invalid requests changed durable state: %+v", workspace)
	}
}

func TestDiscussionRunCancellationIsExplicitAndContractValid(t *testing.T) {
	store := testCatalog(t)
	addProject(t, store, "qianshou", 101, "ai-daming/qianshou")
	root := t.TempDir()
	checkout := filepath.Join(root, "qianshou")
	initWorkflowRepository(t, checkout)
	facts := testFacts()
	facts.issue = githubapi.Issue{Number: 6, Title: "Discussion", State: "OPEN", Body: "Goal v1",
		UpdatedAt: "2026-08-19T15:09:52Z", Dependency: githubapi.Dependency{Status: githubapi.DependencyReady}}
	cfg := config.Config{Version: 1, Runner: config.Runner{ID: "runner-1", AllowedRoots: []string{root}},
		Engines: []config.Engine{{ID: "codex", Adapter: "codex", Command: "codex"}}}
	executor := newBlockingWorkflowExecutor()
	h := handlerWithWorkflow(store, facts, cfg, executor, context.Background())

	binding := postJSON(t, h, "/api/v1/projects/qianshou/runner-binding", `{"mainCheckoutPath":`+jsonString(checkout)+`}`)
	if binding.Code != http.StatusCreated {
		t.Fatalf("binding = %d %s", binding.Code, binding.Body.String())
	}
	conversation := postJSON(t, h, "/api/v1/projects/qianshou/issues/6/conversations", `{"engineId":"codex","idempotencyKey":"conversation-cancel"}`)
	var conversationBody struct {
		ID string `json:"id"`
	}
	decodeResponse(t, conversation, &conversationBody)
	run := postJSON(t, h, "/api/v1/projects/qianshou/issues/6/conversations/"+conversationBody.ID+"/runs", `{"prompt":"wait","idempotencyKey":"run-cancel"}`)
	var runBody struct {
		ID string `json:"id"`
	}
	decodeResponse(t, run, &runBody)
	select {
	case <-executor.started:
	case <-time.After(time.Second):
		t.Fatal("executor did not start")
	}
	concurrentRun := postJSON(t, h, "/api/v1/projects/qianshou/issues/6/conversations/"+conversationBody.ID+"/runs", `{"prompt":"second","idempotencyKey":"run-concurrent"}`)
	assertWorkflowError(t, concurrentRun, http.StatusConflict, "RUN_CONFLICT")
	cancelPath := "/api/v1/projects/qianshou/issues/6/runs/" + runBody.ID + "/cancel"
	cancelled := postJSON(t, h, cancelPath, `{}`)
	if cancelled.Code != http.StatusAccepted || !strings.Contains(cancelled.Body.String(), `"cancellationRequested":true`) {
		t.Fatalf("cancel = %d %s", cancelled.Code, cancelled.Body.String())
	}
	contract := loadOpenAPIContract(t)
	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:41727"+cancelPath, nil)
	if err := validateRecordedResponse(t.Context(), contract, request, cancelled); err != nil {
		t.Fatalf("cancel response does not match OpenAPI: %v\n%s", err, cancelled.Body.String())
	}

	deadline := time.Now().Add(time.Second)
	for {
		workspace, err := store.GetIssueWorkspace(context.Background(), "qianshou", 6)
		if err != nil {
			t.Fatal(err)
		}
		if len(workspace.Runs) == 1 && workspace.Runs[0].State() == ledger.RunCancelled {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cancelled run did not become terminal: %+v", workspace.Runs)
		}
		time.Sleep(10 * time.Millisecond)
	}
	second := postJSON(t, h, cancelPath, `{}`)
	assertWorkflowError(t, second, http.StatusConflict, "RUN_NOT_CANCELLABLE")
}

func TestWorkflowAttacksFailClosed(t *testing.T) {
	store := testCatalog(t)
	addProject(t, store, "qianshou", 101, "ai-daming/qianshou")
	root := t.TempDir()
	checkout := filepath.Join(root, "qianshou")
	initWorkflowRepository(t, checkout)
	facts := testFacts()
	facts.issue = githubapi.Issue{Number: 6, Title: "Discussion", State: "OPEN", Body: "Goal v1",
		UpdatedAt: "2026-08-19T15:09:52Z", Dependency: githubapi.Dependency{Status: githubapi.DependencyReady}}
	cfg := config.Config{Version: 1, Runner: config.Runner{ID: "runner-1", AllowedRoots: []string{root}},
		Engines: []config.Engine{{ID: "codex", Adapter: "codex", Command: "codex"}}}
	h := handlerWithWorkflow(store, facts, cfg, fakeWorkflowExecutor{}, context.Background())

	assertWorkflowError(t, postJSON(t, h, "/api/v1/projects/qianshou/runner-binding", `{`), http.StatusBadRequest, "INVALID_REQUEST")
	binding := postJSON(t, h, "/api/v1/projects/qianshou/runner-binding", `{"mainCheckoutPath":`+jsonString(checkout)+`}`)
	if binding.Code != http.StatusCreated {
		t.Fatalf("binding = %d %s", binding.Code, binding.Body.String())
	}
	otherCheckout := filepath.Join(root, "other")
	initWorkflowRepository(t, otherCheckout)
	assertWorkflowError(t, postJSON(t, h, "/api/v1/projects/qianshou/runner-binding", `{"mainCheckoutPath":`+jsonString(otherCheckout)+`}`), http.StatusConflict, "RUNNER_BINDING_CONFLICT")
	assertWorkflowError(t, postJSON(t, h, "/api/v1/projects/qianshou/issues/6/conversations", `{"engineId":"claude","idempotencyKey":"disabled"}`), http.StatusConflict, "DISCUSSION_TARGET_INVALID")

	conversation := postJSON(t, h, "/api/v1/projects/qianshou/issues/6/conversations", `{"engineId":"codex","idempotencyKey":"conversation-attacks"}`)
	var conversationBody struct {
		ID string `json:"id"`
	}
	decodeResponse(t, conversation, &conversationBody)
	assertWorkflowError(t, postJSON(t, h, "/api/v1/projects/qianshou/issues/6/conversations/missing/runs", `{"prompt":"x","idempotencyKey":"missing"}`), http.StatusNotFound, "CONVERSATION_NOT_FOUND")

	run := postJSON(t, h, "/api/v1/projects/qianshou/issues/6/conversations/"+conversationBody.ID+"/runs", `{"prompt":"x","idempotencyKey":"run-attacks"}`)
	var runBody struct {
		ID string `json:"id"`
	}
	decodeResponse(t, run, &runBody)
	deadline := time.Now().Add(time.Second)
	for {
		workspace, _ := store.GetIssueWorkspace(context.Background(), "qianshou", 6)
		if len(workspace.Runs) == 1 && workspace.Runs[0].State() == ledger.RunCompleted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("run did not complete")
		}
		time.Sleep(10 * time.Millisecond)
	}
	runRetry := postJSON(t, h, "/api/v1/projects/qianshou/issues/6/conversations/"+conversationBody.ID+"/runs", `{"prompt":"x","idempotencyKey":"run-attacks"}`)
	if runRetry.Code != http.StatusOK {
		t.Fatalf("terminal run retry = %d %s", runRetry.Code, runRetry.Body.String())
	}
	assertWorkflowError(t, getJSON(t, h, "/api/v1/projects/qianshou/issues/6/runs/missing/events", nil), http.StatusNotFound, "RUN_NOT_FOUND")
	assertWorkflowError(t, getJSON(t, h, "/api/v1/projects/qianshou/issues/6/runs/"+runBody.ID+"/events?after=-1", nil), http.StatusBadRequest, "INVALID_CURSOR")
	assertWorkflowError(t, getJSON(t, h, "/api/v1/projects/qianshou/issues/6/runs/"+runBody.ID+"/events?limit=0", nil), http.StatusBadRequest, "INVALID_LIMIT")
	assertWorkflowError(t, getJSON(t, h, "/api/v1/projects/qianshou/issues/6/runs/"+runBody.ID+"/events?limit=1001", nil), http.StatusBadRequest, "INVALID_LIMIT")

	correctHash := testSHA("Goal v1")
	assertWorkflowError(t, postJSON(t, h, "/api/v1/projects/qianshou/issues/6/briefs", `{"content":"brief","sourceConversationId":"missing","idempotencyKey":"bad-source","expectedIssueUpdatedAt":"2026-08-19T15:09:52Z","expectedIssueBodySha256":`+jsonString(correctHash)+`}`), http.StatusConflict, "BRIEF_SOURCE_INVALID")
	assertWorkflowError(t, postJSON(t, h, "/api/v1/projects/qianshou/issues/6/briefs", `{"content":"brief","sourceConversationId":`+jsonString(conversationBody.ID)+`,"idempotencyKey":"stale","expectedIssueUpdatedAt":"old","expectedIssueBodySha256":`+jsonString(correctHash)+`}`), http.StatusConflict, "ISSUE_EVIDENCE_STALE")
	validBrief := postJSON(t, h, "/api/v1/projects/qianshou/issues/6/briefs", `{"content":"brief","sourceConversationId":`+jsonString(conversationBody.ID)+`,"idempotencyKey":"brief-conflict","expectedIssueUpdatedAt":"2026-08-19T15:09:52Z","expectedIssueBodySha256":`+jsonString(correctHash)+`}`)
	if validBrief.Code != http.StatusCreated {
		t.Fatalf("valid brief = %d %s", validBrief.Code, validBrief.Body.String())
	}
	assertWorkflowError(t, postJSON(t, h, "/api/v1/projects/qianshou/issues/6/briefs", `{"content":"different","sourceConversationId":`+jsonString(conversationBody.ID)+`,"idempotencyKey":"brief-conflict","expectedIssueUpdatedAt":"2026-08-19T15:09:52Z","expectedIssueBodySha256":`+jsonString(correctHash)+`}`), http.StatusConflict, "BRIEF_CONFLICT")
	assertWorkflowError(t, postJSON(t, h, "/api/v1/projects/qianshou/issues/6/adoptions", `{"briefVersionId":"missing","adoptionKey":"bad-dod","expectedIssueUpdatedAt":"2026-08-19T15:09:52Z","expectedIssueBodySha256":`+jsonString(correctHash)+`,"issueDoD":{}}`), http.StatusBadRequest, "INVALID_ISSUE_DOD")
	assertWorkflowError(t, postJSON(t, h, "/api/v1/projects/qianshou/issues/6/adoptions", `{"briefVersionId":"missing","adoptionKey":"stale-preview","expectedIssueUpdatedAt":"old","expectedIssueBodySha256":`+jsonString(correctHash)+`,"issueDoD":[]}`), http.StatusConflict, "ISSUE_EVIDENCE_STALE")
	assertWorkflowError(t, postJSON(t, h, "/api/v1/projects/qianshou/issues/6/adoptions", `{"briefVersionId":"missing","adoptionKey":"missing-brief","expectedIssueUpdatedAt":"2026-08-19T15:09:52Z","expectedIssueBodySha256":`+jsonString(correctHash)+`,"issueDoD":[]}`), http.StatusConflict, "BRIEF_NOT_FOUND")
	assertWorkflowError(t, postJSON(t, h, "/api/v1/projects/qianshou/issues/6/stops/missing/resolve", `{"resolution":"CONTINUE","outcome":{}}`), http.StatusNotFound, "STOP_NOT_FOUND")
	assertWorkflowError(t, postJSON(t, h, "/api/v1/projects/qianshou/issues/6/stops/missing/resolve", `{"resolution":"CONTINUE","outcome":null}`), http.StatusBadRequest, "INVALID_REQUEST")
	assertWorkflowError(t, getJSON(t, h, "/api/v1/projects/qianshou/issues/0/workspace", nil), http.StatusBadRequest, "INVALID_ISSUE_NUMBER")
	assertWorkflowError(t, postJSON(t, h, "/api/v1/projects/qianshou/issues/0/conversations", `{"engineId":"codex","idempotencyKey":"invalid-issue"}`), http.StatusBadRequest, "INVALID_ISSUE_NUMBER")
	assertWorkflowError(t, getJSON(t, h, "/api/v1/projects/missing/issues/6/workspace", nil), http.StatusNotFound, "PROJECT_NOT_FOUND")

	withoutBinding := testCatalog(t)
	addProject(t, withoutBinding, "qianshou", 101, "ai-daming/qianshou")
	hWithoutBinding := handlerWithWorkflow(withoutBinding, facts, cfg, fakeWorkflowExecutor{}, context.Background())
	assertWorkflowError(t, postJSON(t, hWithoutBinding, "/api/v1/projects/qianshou/issues/6/conversations", `{"engineId":"codex","idempotencyKey":"no-binding"}`), http.StatusConflict, "RUNNER_BINDING_REQUIRED")

	hWithoutRunner := handlerWithWorkflow(store, facts, config.Config{}, fakeWorkflowExecutor{}, context.Background())
	assertWorkflowError(t, postJSON(t, hWithoutRunner, "/api/v1/projects/qianshou/runner-binding", `{"mainCheckoutPath":`+jsonString(checkout)+`}`), http.StatusConflict, "RUNNER_NOT_CONFIGURED")
	facts.issue.UpdatedAt = ""
	assertWorkflowError(t, postJSON(t, h, "/api/v1/projects/qianshou/issues/6/briefs", `{"content":"brief","sourceConversationId":`+jsonString(conversationBody.ID)+`,"idempotencyKey":"missing-updated","expectedIssueUpdatedAt":"x","expectedIssueBodySha256":`+jsonString(correctHash)+`}`), http.StatusBadGateway, "GITHUB_FACTS_UNAVAILABLE")
	assertWorkflowError(t, postJSON(t, h, "/api/v1/projects/qianshou/issues/6/adoptions", `{"briefVersionId":"missing","adoptionKey":"missing-updated","expectedIssueUpdatedAt":"x","expectedIssueBodySha256":`+jsonString(correctHash)+`,"issueDoD":[]}`), http.StatusBadGateway, "GITHUB_FACTS_UNAVAILABLE")
	facts.issue.UpdatedAt = "2026-08-19T15:09:52Z"
	facts.issue.Number = 7
	assertWorkflowError(t, postJSON(t, h, "/api/v1/projects/qianshou/issues/6/conversations", `{"engineId":"codex","idempotencyKey":"contradictory-issue"}`), http.StatusBadGateway, "GITHUB_FACTS_UNAVAILABLE")
	facts.issue.Number = 6
	facts.repositories[101] = githubapi.Repository{ID: 999, NameWithOwner: "ai-daming/qianshou"}
	assertWorkflowError(t, postJSON(t, h, "/api/v1/projects/qianshou/issues/6/conversations", `{"engineId":"codex","idempotencyKey":"repository-mismatch"}`), http.StatusBadGateway, "GITHUB_FACTS_UNAVAILABLE")
	facts.repositories[101] = githubapi.Repository{ID: 101, NameWithOwner: "ai-daming/qianshou"}
	facts.err = context.DeadlineExceeded
	assertWorkflowError(t, postJSON(t, h, "/api/v1/projects/qianshou/issues/6/conversations", `{"engineId":"codex","idempotencyKey":"github-down"}`), http.StatusBadGateway, "GITHUB_FACTS_UNAVAILABLE")
}

func TestDiscussionRunRecordsInterruptedFailedAndSessionConflictOutcomes(t *testing.T) {
	tests := []struct {
		name            string
		executor        staticWorkflowExecutor
		existingSession string
		wantState       ledger.RunState
		wantDetail      string
	}{
		{name: "deadline", executor: staticWorkflowExecutor{err: context.DeadlineExceeded}, wantState: ledger.RunInterrupted, wantDetail: "exceeded"},
		{name: "failure", executor: staticWorkflowExecutor{err: errors.New("secret implementation detail")}, wantState: ledger.RunFailed, wantDetail: "inspect offline"},
		{name: "session conflict", executor: staticWorkflowExecutor{result: localrunner.ExecuteResult{SessionID: "new-session", Text: "answer"}}, existingSession: "old-session", wantState: ledger.RunFailed, wantDetail: "session identity conflicted"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := testCatalog(t)
			addProject(t, store, "qianshou", 101, "ai-daming/qianshou")
			ctx := context.Background()
			if _, err := store.CreateRunner(ctx, ledger.NewRunner{ID: "runner-1", DisplayName: "runner"}); err != nil {
				t.Fatal(err)
			}
			binding, err := store.CreateRunnerProjectBinding(ctx, ledger.NewRunnerProjectBinding{ID: "binding-1", RunnerID: "runner-1",
				ProjectID: "qianshou", MainCheckoutPath: "/work/qianshou", RepositoryIDAtBinding: 101})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.EnsureWorkItem(ctx, "qianshou", 6); err != nil {
				t.Fatal(err)
			}
			conversation, err := store.CreateConversation(ctx, ledger.NewConversation{ID: "conversation-1", ProjectID: "qianshou", IssueNumber: 6,
				Role: ledger.RoleDiscussion, EngineID: "codex", RunnerProjectBindingID: binding.ID})
			if err != nil {
				t.Fatal(err)
			}
			if tc.existingSession != "" {
				if err := store.SetVendorSession(ctx, conversation.ID, tc.existingSession); err != nil {
					t.Fatal(err)
				}
			}
			run, err := store.QueueRun(ctx, ledger.NewAgentRun{ID: "run-1", ConversationID: conversation.ID,
				CommandKey: "command-1", CommandJSON: `{}`})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := store.StartRun(ctx, run.ID); err != nil {
				t.Fatal(err)
			}
			runtime := workflowRuntime{store: store, executor: tc.executor, context: ctx}
			runtime.executeDiscussionRun(conversation.ID, localrunner.ExecuteRequest{RunID: run.ID})
			finished, err := store.GetRun(ctx, run.ID)
			if err != nil || finished.State() != tc.wantState || finished.TerminalDetailJSON == nil || !strings.Contains(*finished.TerminalDetailJSON, tc.wantDetail) {
				t.Fatalf("finished = %+v, err = %v", finished, err)
			}
		})
	}
}

func TestWorkflowHelperBoundaries(t *testing.T) {
	if validSHA256(strings.Repeat("z", 64)) || !validSHA256(strings.Repeat("a", 64)) {
		t.Fatal("SHA-256 syntax validation accepted invalid hex or rejected valid hex")
	}
	if value, err := nonNegativeQuery("", 9); err != nil || value != 9 {
		t.Fatalf("cursor fallback = %d, %v", value, err)
	}
	if _, err := nonNegativeQuery("nope", 0); err == nil {
		t.Fatal("non-numeric cursor was accepted")
	}
	if value, err := positiveQuery("", 7); err != nil || value != 7 {
		t.Fatalf("limit fallback = %d, %v", value, err)
	}
	if _, err := positiveQuery("nope", 0); err == nil {
		t.Fatal("non-numeric limit was accepted")
	}
	for err, want := range map[error]string{
		localrunner.ErrCancelled: "cancelled",
		context.DeadlineExceeded: "exceeded",
		errors.New("private"):    "inspect offline",
	} {
		if got := publicRunFailure(err); !strings.Contains(got, want) {
			t.Fatalf("public error %v = %q, want %q", err, got, want)
		}
	}
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{err: ledger.ErrConflict, status: http.StatusConflict, code: "CONFLICT"},
		{err: ledger.ErrInvariant, status: http.StatusBadRequest, code: "INVALID_REQUEST"},
		{err: errors.New("disk"), status: http.StatusInternalServerError, code: "LEDGER_UNAVAILABLE"},
	} {
		response := httptest.NewRecorder()
		writeLedgerMutationError(response, tc.err, "CONFLICT", "conflict")
		assertWorkflowError(t, response, tc.status, tc.code)
	}
}

func TestPartialTrackBindingIsContradictoryEvidence(t *testing.T) {
	path := "/work/partial"
	track := &ledger.DeliveryTrack{WorkspacePath: &path}
	present, complete := trackBindingState(track)
	if !present || complete {
		t.Fatalf("partial binding state = present %v complete %v", present, complete)
	}
	facts := ledgerCapabilityFacts(ledger.Project{RepositoryID: 101}, ledger.IssueWorkspace{ActiveTrack: track})
	facts.CurrentBaselineID = "baseline-1"
	facts.BaselineIssueUpdatedAt = "issue-v1"
	input := capability.Input{Ledger: facts,
		GitHub: capability.Evidence[capability.GitHubFacts]{State: capability.EvidenceComplete,
			Value: &capability.GitHubFacts{RepositoryID: 101, IssueUpdatedAt: "issue-v1", IssueOpen: true, DependenciesReady: true}},
		Git: capability.Evidence[capability.GitFacts]{State: capability.EvidenceComplete,
			Value: &capability.GitFacts{WorktreePresent: true}},
		Runner: capability.Evidence[capability.RunnerFacts]{State: capability.EvidenceComplete,
			Value: &capability.RunnerFacts{Online: true, BindingAllowed: true, EngineAllowed: true}}}
	got := capability.Derive(input)
	if got.DerivedStage != nil || containsCapabilityAction(got.AllowedActions, capability.ActionStartImplementation) ||
		!hasCapabilityReason(got.BlockedReasons, "LEDGER_INVARIANT_CONFLICT", capability.SourceLedger) {
		t.Fatalf("partial Track binding did not fail closed: %+v", got)
	}
}

func TestWorkspaceReconciliationFailsClosedAcrossSourceBoundaries(t *testing.T) {
	newRuntime := func(t *testing.T, withBinding bool) (*workflowRuntime, ledger.Project, ledger.IssueWorkspace, *fakeFacts, config.Config) {
		t.Helper()
		store := testCatalog(t)
		addProject(t, store, "qianshou", 101, "ai-daming/qianshou")
		root := t.TempDir()
		checkout := filepath.Join(root, "qianshou")
		initWorkflowRepository(t, checkout)
		cfg := config.Config{Version: 1, Runner: config.Runner{ID: "runner-1", AllowedRoots: []string{root}},
			Engines: []config.Engine{{ID: "codex", Adapter: "codex", Command: "codex"}}}
		if withBinding {
			if _, err := store.CreateRunner(t.Context(), ledger.NewRunner{ID: "runner-1", DisplayName: "runner"}); err != nil {
				t.Fatal(err)
			}
			if _, err := store.CreateRunnerProjectBinding(t.Context(), ledger.NewRunnerProjectBinding{ID: "binding-1",
				RunnerID: "runner-1", ProjectID: "qianshou", MainCheckoutPath: checkout, RepositoryIDAtBinding: 101}); err != nil {
				t.Fatal(err)
			}
		}
		facts := testFacts()
		facts.issue = githubapi.Issue{Number: 7, Title: "Slice 1", State: "OPEN", Body: "Goal", UpdatedAt: "issue-v1",
			Dependency: githubapi.Dependency{Status: githubapi.DependencyReady}}
		workspace := ledger.IssueWorkspace{ActiveTrack: &ledger.DeliveryTrack{ID: "track-1"},
			Baselines: []ledger.DeliveryBaseline{{ID: "baseline-1", IssueUpdatedAt: "issue-v1"}}}
		return &workflowRuntime{store: store, facts: facts, config: cfg}, ledger.Project{ID: "qianshou", RepositoryID: 101}, workspace, facts, cfg
	}

	t.Run("repository identity contradiction", func(t *testing.T) {
		runtime, project, workspace, facts, _ := newRuntime(t, false)
		facts.repositories[101] = githubapi.Repository{ID: 999, NameWithOwner: "ai-daming/qianshou"}
		got := runtime.reconcileWorkspace(t.Context(), project, 7, workspace)
		if !hasCapabilityReason(got.Result.BlockedReasons, "GITHUB_FACTS_CONFLICTING", capability.SourceGitHub) ||
			!sourceKindHasState(got.EvidenceSources, "GITHUB", "ISSUE_AND_DEPENDENCIES", "CONFLICTING") {
			t.Fatalf("repository contradiction = %+v", got)
		}
	})

	t.Run("repository facts unavailable", func(t *testing.T) {
		runtime, project, workspace, facts, _ := newRuntime(t, false)
		facts.err = context.DeadlineExceeded
		got := runtime.reconcileWorkspace(t.Context(), project, 7, workspace)
		if !hasCapabilityReason(got.Result.BlockedReasons, "GITHUB_FACTS_UNAVAILABLE", capability.SourceGitHub) ||
			!sourceKindHasState(got.EvidenceSources, "GITHUB", "ISSUE_AND_DEPENDENCIES", "UNAVAILABLE") {
			t.Fatalf("repository unavailable = %+v", got)
		}
	})

	t.Run("Issue identity contradiction", func(t *testing.T) {
		runtime, project, workspace, facts, _ := newRuntime(t, false)
		facts.issue.Number = 8
		got := runtime.reconcileWorkspace(t.Context(), project, 7, workspace)
		if !hasCapabilityReason(got.Result.BlockedReasons, "GITHUB_FACTS_CONFLICTING", capability.SourceGitHub) {
			t.Fatalf("Issue contradiction = %+v", got)
		}
	})

	t.Run("Issue version missing", func(t *testing.T) {
		runtime, project, workspace, facts, _ := newRuntime(t, false)
		facts.issue.UpdatedAt = ""
		got := runtime.reconcileWorkspace(t.Context(), project, 7, workspace)
		if !hasCapabilityReason(got.Result.BlockedReasons, "GITHUB_FACTS_MISSING", capability.SourceGitHub) {
			t.Fatalf("Issue version missing = %+v", got)
		}
	})

	t.Run("dependency state contradiction", func(t *testing.T) {
		runtime, project, workspace, facts, _ := newRuntime(t, false)
		facts.issue.Dependency = githubapi.Dependency{Status: "UNKNOWN"}
		got := runtime.reconcileWorkspace(t.Context(), project, 7, workspace)
		if !hasCapabilityReason(got.Result.BlockedReasons, "GITHUB_FACTS_CONFLICTING", capability.SourceGitHub) {
			t.Fatalf("dependency contradiction = %+v", got)
		}
	})

	t.Run("dependency evidence missing", func(t *testing.T) {
		runtime, project, workspace, facts, _ := newRuntime(t, true)
		facts.issue.Dependency = githubapi.Dependency{Status: githubapi.DependencyError}
		got := runtime.reconcileWorkspace(t.Context(), project, 7, workspace)
		if !hasCapabilityReason(got.Result.BlockedReasons, "GITHUB_FACTS_MISSING", capability.SourceGitHub) ||
			!sourceKindHasState(got.EvidenceSources, "GITHUB", "ISSUE_AND_DEPENDENCIES", "MISSING") {
			t.Fatalf("dependency missing = %+v", got)
		}
	})

	t.Run("main checkout trust contradiction", func(t *testing.T) {
		runtime, project, workspace, _, cfg := newRuntime(t, true)
		cfg.Runner.AllowedRoots = []string{t.TempDir()}
		runtime.config = cfg
		got := runtime.reconcileWorkspace(t.Context(), project, 7, workspace)
		if !hasCapabilityReason(got.Result.BlockedReasons, "GIT_FACTS_CONFLICTING", capability.SourceGit) ||
			!sourceKindHasState(got.EvidenceSources, "RUNNER", "EMBEDDED_RUNNER", "CONFLICTING") {
			t.Fatalf("trust contradiction = %+v", got)
		}
	})

	t.Run("bound worktree facts not yet implemented", func(t *testing.T) {
		runtime, project, workspace, _, _ := newRuntime(t, true)
		workspace.ActiveTrack.RunnerProjectBindingID = testStringPointer("binding-1")
		workspace.ActiveTrack.WorkspacePath = testStringPointer("/work/qianshou-7")
		workspace.ActiveTrack.Branch = testStringPointer("issue-7")
		workspace.ActiveTrack.BaseBranch = testStringPointer("main")
		workspace.ActiveTrack.BaseSHAAtBinding = testStringPointer("base-sha")
		got := runtime.reconcileWorkspace(t.Context(), project, 7, workspace)
		if !hasCapabilityReason(got.Result.BlockedReasons, "GIT_FACTS_MISSING", capability.SourceGit) ||
			!sourceKindHasState(got.EvidenceSources, "GIT", "TRACK_WORKTREE", "MISSING") {
			t.Fatalf("bound worktree gap = %+v", got)
		}
	})

	t.Run("runner has no enabled engine", func(t *testing.T) {
		runtime, project, workspace, _, cfg := newRuntime(t, true)
		cfg.Engines = nil
		runtime.config = cfg
		got := runtime.reconcileWorkspace(t.Context(), project, 7, workspace)
		if containsCapabilityAction(got.Result.AllowedActions, capability.ActionCreateWorktree) ||
			!hasCapabilityReason(got.Result.BlockedReasons, "RUNNER_PERMISSION_DENIED", capability.SourceRunner) {
			t.Fatalf("disabled engine = %+v", got)
		}
	})

	t.Run("runner identity missing", func(t *testing.T) {
		runtime, project, workspace, _, cfg := newRuntime(t, false)
		cfg.Runner.ID = ""
		runtime.config = cfg
		got := runtime.reconcileWorkspace(t.Context(), project, 7, workspace)
		if containsCapabilityAction(got.Result.AllowedActions, capability.ActionCreateWorktree) ||
			!hasCapabilityReason(got.Result.BlockedReasons, "RUNNER_UNAVAILABLE", capability.SourceRunner) {
			t.Fatalf("runner identity missing = %+v", got)
		}
	})
}

func assertWorkflowError(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status || !strings.Contains(response.Body.String(), `"code":"`+code+`"`) {
		t.Fatalf("error response = %d %s, want %d %s", response.Code, response.Body.String(), status, code)
	}
}

func postJSON(t *testing.T, h http.Handler, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

func decodeResponse(t *testing.T, rr *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.Unmarshal(rr.Body.Bytes(), target); err != nil {
		t.Fatalf("decode response: %v: %s", err, rr.Body.String())
	}
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func completeEvidenceSource(values []struct {
	Source     string `json:"source"`
	State      string `json:"state"`
	ObservedAt string `json:"observedAt"`
}, want string) bool {
	for _, value := range values {
		if value.Source == want && value.State == "COMPLETE" && value.ObservedAt != "" {
			return true
		}
	}
	return false
}

func evidenceSourceHasState(values []struct {
	Source     string `json:"source"`
	State      string `json:"state"`
	ObservedAt string `json:"observedAt"`
}, source, state string) bool {
	for _, value := range values {
		if value.Source == source && value.State == state && value.ObservedAt != "" {
			return true
		}
	}
	return false
}

func hasPublicReason(values []struct {
	Code   string `json:"code"`
	Source string `json:"source"`
}, code, source string) bool {
	for _, value := range values {
		if value.Code == code && value.Source == source {
			return true
		}
	}
	return false
}

func containsCapabilityAction(values []capability.Action, want capability.Action) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasCapabilityReason(values []capability.BlockedReason, code string, source capability.Source) bool {
	for _, value := range values {
		if value.Code == code && value.Source == source {
			return true
		}
	}
	return false
}

func sourceKindHasState(values []evidenceSource, source, kind, state string) bool {
	for _, value := range values {
		if string(value.Source) == source && value.Kind == kind && string(value.State) == state && value.ObservedAt != "" {
			return true
		}
	}
	return false
}

func testStringPointer(value string) *string { return &value }

func testSHA(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func initWorkflowRepository(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"}, {"config", "user.email", "test@example.com"}, {"config", "user.name", "Test"},
		{"remote", "add", "origin", "https://github.com/ai-daming/qianshou.git"},
	} {
		if output, err := exec.Command("git", append([]string{"-C", path}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
	if err := os.WriteFile(filepath.Join(path, "README.md"), []byte("test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "README.md"}, {"commit", "-m", "initial"}} {
		if output, err := exec.Command("git", append([]string{"-C", path}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, output)
		}
	}
}
