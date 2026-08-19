package localrunner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ai-daming/qianshou/apps/control/internal/config"
	"github.com/ai-daming/qianshou/apps/control/internal/ledger"
)

func TestCLIExecutorParsesCodexFramesWithoutChangingRawEvidence(t *testing.T) {
	script := executableScript(t, `
read prompt
printf '%s\n' '{"type":"thread.started","thread_id":"thread-1"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"answer"}}'
`)
	executor := NewCLIExecutor()
	var frames []ObservedFrame
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		RunID: "run-1", Engine: config.Engine{ID: "codex", Adapter: "codex", Command: script},
		Role: ledger.RoleDiscussion, CWD: t.TempDir(), Prompt: "question",
	}, func(frame ObservedFrame) error {
		frames = append(frames, frame)
		return nil
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if result.SessionID != "thread-1" || result.Text != "answer" {
		t.Fatalf("result = %+v", result)
	}
	if len(frames) != 2 || string(frames[0].Raw) != "{\"type\":\"thread.started\",\"thread_id\":\"thread-1\"}\n" {
		t.Fatalf("raw frames = %#v", frames)
	}
	if frames[0].Event.Kind != ledger.EventStatus || frames[1].Event.Kind != ledger.EventAgentMessage {
		t.Fatalf("events = %+v", frames)
	}
}

func TestCLIExecutorBuildsResumeCommandsAndClaudeResult(t *testing.T) {
	argsPath := filepath.Join(t.TempDir(), "args")
	script := executableScript(t, `
printf '%s' "$*" > "`+argsPath+`"
cat >/dev/null
printf '%s\n' '{"type":"system","subtype":"init","session_id":"session-1"}'
printf '%s\n' '{"type":"result","session_id":"session-1","result":"done","is_error":false}'
`)
	executor := NewCLIExecutor()
	result, err := executor.Execute(context.Background(), ExecuteRequest{
		RunID: "run-2", Engine: config.Engine{ID: "claude", Adapter: "claude", Command: script},
		Role: ledger.RoleDiscussion, CWD: t.TempDir(), Prompt: "continue", SessionID: "session-1",
	}, func(ObservedFrame) error { return nil })
	if err != nil || result.SessionID != "session-1" || result.Text != "done" {
		t.Fatalf("result = %+v, %v", result, err)
	}
	args, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"--resume session-1", "--output-format stream-json", "--permission-mode plan"} {
		if !strings.Contains(string(args), required) {
			t.Fatalf("args %q missing %q", args, required)
		}
	}
}

func TestCLIExecutorCancellationIsExplicit(t *testing.T) {
	script := executableScript(t, `
cat >/dev/null
sleep 30
`)
	executor := NewCLIExecutor()
	done := make(chan error, 1)
	go func() {
		_, err := executor.Execute(context.Background(), ExecuteRequest{
			RunID: "run-cancel", Engine: config.Engine{ID: "codex", Adapter: "codex", Command: script},
			Role: ledger.RoleDiscussion, CWD: t.TempDir(), Prompt: "wait",
		}, func(ObservedFrame) error { return nil })
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for !executor.Cancel("run-cancel") && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case err := <-done:
		if !errors.Is(err, ErrCancelled) {
			t.Fatalf("cancel error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled process did not stop")
	}
}

func TestNormalizerCoversSupportedCodexAndClaudeFrames(t *testing.T) {
	tests := []struct {
		name    string
		adapter string
		raw     string
		kind    ledger.EventKind
		failed  bool
		text    string
	}{
		{name: "invalid", adapter: "codex", raw: "not-json\n", kind: ledger.EventError, failed: true},
		{name: "codex tool call", adapter: "codex", raw: `{"type":"item.started","item":{"type":"command_execution"}}`, kind: ledger.EventToolCall},
		{name: "codex command result", adapter: "codex", raw: `{"type":"item.completed","item":{"type":"command_execution"}}`, kind: ledger.EventToolResult},
		{name: "codex mcp result", adapter: "codex", raw: `{"type":"item.completed","item":{"type":"mcp_tool_call"}}`, kind: ledger.EventToolResult},
		{name: "codex unknown item", adapter: "codex", raw: `{"type":"item.completed","item":{"type":"reasoning"}}`, kind: ledger.EventStatus},
		{name: "codex turn", adapter: "codex", raw: `{"type":"turn.completed","usage":{"input_tokens":3}}`, kind: ledger.EventResult},
		{name: "codex error", adapter: "codex", raw: `{"type":"error","message":"bad"}`, kind: ledger.EventError},
		{name: "codex status", adapter: "codex", raw: `{"type":"other"}`, kind: ledger.EventStatus},
		{name: "claude assistant", adapter: "claude", raw: `{"type":"assistant","session_id":"s","message":{"content":[{"text":"one"},{"text":"two"}]}}`, kind: ledger.EventAgentMessage},
		{name: "claude status", adapter: "claude", raw: `{"type":"system","session_id":"s"}`, kind: ledger.EventStatus},
		{name: "claude result", adapter: "claude", raw: `{"type":"result","session_id":"s","result":"done","is_error":false}`, kind: ledger.EventResult, text: "done"},
		{name: "claude error", adapter: "claude", raw: `{"type":"result","session_id":"s","result":"failed","is_error":true}`, kind: ledger.EventError, text: "failed"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frame := normalizeFrame(tc.adapter, []byte(tc.raw), 7)
			if frame.Event.Kind != tc.kind || (frame.ParseStatus == ledger.FrameFailed) != tc.failed || frame.finalText != tc.text {
				t.Fatalf("frame = %+v", frame)
			}
		})
	}
}

func TestNormalizerKeepsRawEvidenceOfflineButRedactsBrowserEvents(t *testing.T) {
	tests := []struct {
		name    string
		adapter string
		raw     string
	}{
		{name: "codex agent message", adapter: "codex", raw: `{"type":"item.completed","item":{"type":"agent_message","text":"Bearer secret-value"}}`},
		{name: "claude result", adapter: "claude", raw: `{"type":"result","session_id":"s","result":"github_pat_secret-value","is_error":false}`},
		{name: "codex tool call", adapter: "codex", raw: `{"type":"item.started","item":{"type":"command_execution","command":"env","aggregated_output":"ghp_secret-value"}}`},
		{name: "codex tool result", adapter: "codex", raw: `{"type":"item.completed","item":{"type":"command_execution","status":"completed","exit_code":0,"aggregated_output":"sk-ant-secret-value"}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			frame := normalizeFrame(tc.adapter, []byte(tc.raw), 1)
			if !strings.Contains(string(frame.Raw), "secret-value") {
				t.Fatalf("raw evidence was altered: %q", frame.Raw)
			}
			for _, public := range []string{frame.Event.PayloadJSON, frame.finalText, frame.vendorError} {
				if strings.Contains(public, "secret-value") || strings.Contains(public, "aggregated_output") || strings.Contains(public, `"command":"env"`) {
					t.Fatalf("normalized browser-visible value leaks raw tool or secret data: %s", public)
				}
			}
		})
	}
}

func TestCLIExecutorFailsClosedOnAmbiguousOrIncompleteOutput(t *testing.T) {
	executor := NewCLIExecutor()
	cwd := t.TempDir()
	if _, err := executor.Execute(context.Background(), ExecuteRequest{}, nil); err == nil {
		t.Fatal("incomplete execution request was accepted")
	}
	if _, err := executor.Execute(context.Background(), ExecuteRequest{RunID: "x", CWD: cwd, Prompt: "p", Engine: config.Engine{Adapter: "other"}}, nil); err == nil {
		t.Fatal("unsupported adapter was accepted")
	}

	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "no session", body: `printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"answer"}}'`, want: "vendor session"},
		{name: "no final", body: `printf '%s\n' '{"type":"thread.started","thread_id":"thread-1"}'`, want: "final response"},
		{name: "changed session", body: `printf '%s\n' '{"type":"thread.started","thread_id":"one"}' '{"type":"thread.started","thread_id":"two"}'`, want: "session identity changed"},
		{name: "vendor error", body: `printf '%s\n' '{"type":"thread.started","thread_id":"one"}' '{"type":"error","message":"Bearer secret-value"}'`, want: "[redacted]"},
		{name: "process failure", body: `printf '%s' 'ghp_secret-value' >&2; exit 2`, want: "[redacted]"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			script := executableScript(t, "cat >/dev/null\n"+tc.body+"\n")
			_, err := executor.Execute(context.Background(), ExecuteRequest{RunID: tc.name, CWD: cwd, Prompt: "p",
				Engine: config.Engine{Adapter: "codex", Command: script}, Role: ledger.RoleDiscussion}, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestCLIExecutorStopsWhenFramePersistenceFails(t *testing.T) {
	script := executableScript(t, `
cat >/dev/null
printf '%s\n' '{"type":"thread.started","thread_id":"thread-1"}'
sleep 30
`)
	_, err := NewCLIExecutor().Execute(context.Background(), ExecuteRequest{RunID: "sink", CWD: t.TempDir(), Prompt: "p",
		Engine: config.Engine{Adapter: "codex", Command: script}, Role: ledger.RoleDiscussion}, func(ObservedFrame) error {
		return fmt.Errorf("ledger unavailable")
	})
	if err == nil || !strings.Contains(err.Error(), "persist agent frame") {
		t.Fatalf("error = %v", err)
	}
}

func TestExecutionArgumentsRespectRoleAndResumeBoundaries(t *testing.T) {
	tests := []struct {
		request ExecuteRequest
		want    string
	}{
		{request: ExecuteRequest{Engine: config.Engine{Adapter: "codex"}, CWD: "/work", Role: ledger.RoleImplementation}, want: "workspace-write"},
		{request: ExecuteRequest{Engine: config.Engine{Adapter: "codex"}, SessionID: "thread"}, want: "resume --json thread"},
		{request: ExecuteRequest{Engine: config.Engine{Adapter: "claude"}, Role: ledger.RoleRepair}, want: "acceptEdits"},
	}
	for _, tc := range tests {
		args, err := executionArgs(tc.request)
		if err != nil || !strings.Contains(strings.Join(args, " "), tc.want) {
			t.Fatalf("args = %v, err = %v, want %q", args, err, tc.want)
		}
	}
	if Cancelled := NewCLIExecutor().Cancel("missing"); Cancelled {
		t.Fatal("missing process reported as cancelled")
	}
	if err := killProcessGroup(nil); err != nil {
		t.Fatal(err)
	}
}

func executableScript(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agent")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nset -eu\n"+body), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
