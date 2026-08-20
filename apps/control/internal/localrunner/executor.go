package localrunner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"syscall"

	"github.com/ai-daming/qianshou/apps/control/internal/config"
	"github.com/ai-daming/qianshou/apps/control/internal/ledger"
	"github.com/ai-daming/qianshou/apps/control/internal/strictjson"
)

const (
	maxVendorFrameBytes = 1 << 20
	maxStderrBytes      = 64 << 10
	NormalizerVersion   = "m1-v1"
)

var (
	ErrCancelled = errors.New("agent run cancelled by explicit user action")
	secretText   = regexp.MustCompile(`(?i)(?:Bearer\s+|ghp_|github_pat_|sk-ant-)[A-Za-z0-9._~+:/=-]+`)
)

type ExecuteRequest struct {
	RunID     string
	Engine    config.Engine
	Role      ledger.Role
	CWD       string
	Prompt    string
	SessionID string
}

type ExecuteResult struct {
	SessionID string
	Text      string
}

type ObservedFrame struct {
	Sequence    int
	Raw         []byte
	Channel     string
	ParseStatus ledger.FrameParseStatus
	ParseError  string
	Event       ledger.NewRunEvent
	sessionID   string
	finalText   string
	vendorError string
}

type FrameSink func(ObservedFrame) error

type CLIExecutor struct {
	mu        sync.Mutex
	active    map[string]*exec.Cmd
	cancelled map[string]bool
}

func NewCLIExecutor() *CLIExecutor {
	return &CLIExecutor{active: map[string]*exec.Cmd{}, cancelled: map[string]bool{}}
}

func (e *CLIExecutor) Execute(ctx context.Context, request ExecuteRequest, sink FrameSink) (ExecuteResult, error) {
	if strings.TrimSpace(request.RunID) == "" || strings.TrimSpace(request.CWD) == "" || strings.TrimSpace(request.Prompt) == "" {
		return ExecuteResult{}, fmt.Errorf("agent execution request is incomplete")
	}
	args, err := executionArgs(request)
	if err != nil {
		return ExecuteResult{}, err
	}
	command := exec.CommandContext(ctx, request.Engine.Command, args...)
	command.Dir = request.CWD
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := command.StdinPipe()
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("open agent stdin: %w", err)
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("open agent stdout: %w", err)
	}
	stderr, err := command.StderrPipe()
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("open agent stderr: %w", err)
	}
	if err := command.Start(); err != nil {
		return ExecuteResult{}, fmt.Errorf("start configured agent executable: %w", err)
	}
	e.mu.Lock()
	if _, exists := e.active[request.RunID]; exists {
		e.mu.Unlock()
		_ = killProcessGroup(command)
		_ = command.Wait()
		return ExecuteResult{}, fmt.Errorf("run id already owns an active process")
	}
	e.active[request.RunID] = command
	e.mu.Unlock()

	stderrDone := make(chan []byte, 1)
	go func() {
		value, _ := io.ReadAll(io.LimitReader(stderr, maxStderrBytes+1))
		stderrDone <- value
	}()
	go func() {
		_, _ = io.WriteString(stdin, request.Prompt+"\n")
		_ = stdin.Close()
	}()

	adapter := strings.ToLower(strings.TrimSpace(request.Engine.Adapter))
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), maxVendorFrameBytes)
	scanner.Split(splitFrameIncludingNewline)
	result := ExecuteResult{SessionID: request.SessionID}
	sequence := 0
	var consumeErr error
	for scanner.Scan() {
		sequence++
		raw := append([]byte(nil), scanner.Bytes()...)
		frame := normalizeFrame(adapter, raw, sequence)
		if frame.sessionID != "" {
			if result.SessionID != "" && result.SessionID != frame.sessionID {
				consumeErr = fmt.Errorf("vendor session identity changed within one run")
				break
			}
			result.SessionID = frame.sessionID
		}
		if frame.finalText != "" {
			result.Text = frame.finalText
		}
		if frame.vendorError != "" {
			consumeErr = fmt.Errorf("agent reported an error: %s", safeError(frame.vendorError))
		}
		if sink != nil {
			if err := sink(frame); err != nil {
				consumeErr = fmt.Errorf("persist agent frame: %w", err)
				break
			}
		}
		if consumeErr != nil {
			break
		}
	}
	if scanErr := scanner.Err(); scanErr != nil && consumeErr == nil {
		consumeErr = fmt.Errorf("agent frame exceeded the safe streaming limit: %w", scanErr)
	}
	if consumeErr != nil {
		_ = killProcessGroup(command)
	}
	waitErr := command.Wait()
	stderrBytes := <-stderrDone
	e.mu.Lock()
	wasCancelled := e.cancelled[request.RunID]
	delete(e.cancelled, request.RunID)
	delete(e.active, request.RunID)
	e.mu.Unlock()
	if wasCancelled {
		return ExecuteResult{}, ErrCancelled
	}
	if consumeErr != nil {
		return ExecuteResult{}, consumeErr
	}
	if waitErr != nil {
		return ExecuteResult{}, fmt.Errorf("agent executable failed: %w: %s", waitErr, safeError(string(stderrBytes)))
	}
	if result.SessionID == "" {
		return ExecuteResult{}, fmt.Errorf("agent output did not establish a vendor session")
	}
	if result.Text == "" {
		return ExecuteResult{}, fmt.Errorf("agent output did not contain a final response")
	}
	return result, nil
}

func (e *CLIExecutor) Cancel(runID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	command := e.active[runID]
	if command == nil || command.Process == nil {
		return false
	}
	e.cancelled[runID] = true
	_ = killProcessGroup(command)
	return true
}

func killProcessGroup(command *exec.Cmd) error {
	if command == nil || command.Process == nil {
		return nil
	}
	return syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
}

func executionArgs(request ExecuteRequest) ([]string, error) {
	switch strings.ToLower(strings.TrimSpace(request.Engine.Adapter)) {
	case "claude":
		args := []string{"-p"}
		if request.SessionID != "" {
			args = append(args, "--resume", request.SessionID)
		}
		mode := "plan"
		if request.Role == ledger.RoleImplementation || request.Role == ledger.RoleRepair {
			mode = "acceptEdits"
		}
		return append(args, "--output-format", "stream-json", "--verbose", "--permission-mode", mode), nil
	case "codex":
		sandbox := "read-only"
		if request.Role == ledger.RoleImplementation || request.Role == ledger.RoleRepair {
			sandbox = "workspace-write"
		}
		if request.SessionID != "" {
			return []string{"exec", "resume", "--json", "--config", fmt.Sprintf(`sandbox_mode=%q`, sandbox), request.SessionID, "-"}, nil
		}
		return []string{"exec", "--json", "--sandbox", sandbox, "-C", request.CWD, "-"}, nil
	default:
		return nil, fmt.Errorf("unsupported agent adapter")
	}
}

func splitFrameIncludingNewline(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if index := bytes.IndexByte(data, '\n'); index >= 0 {
		return index + 1, data[:index+1], nil
	}
	if atEOF && len(data) > 0 {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func normalizeFrame(adapter string, raw []byte, sequence int) ObservedFrame {
	frame := ObservedFrame{Sequence: sequence, Raw: raw, Channel: "stdout", ParseStatus: ledger.FrameParsed}
	trimmed := bytes.TrimSpace(raw)
	var event map[string]any
	if len(trimmed) == 0 || strictjson.Decode(trimmed, &event, false) != nil {
		frame.ParseStatus = ledger.FrameFailed
		frame.ParseError = "vendor frame is not unambiguous JSON"
		frame.Event = eventPayload(sequence, ledger.EventError, map[string]any{"message": frame.ParseError})
		return frame
	}
	typeName, _ := event["type"].(string)
	if adapter == "claude" {
		frame.sessionID, _ = event["session_id"].(string)
		switch typeName {
		case "result":
			frame.finalText, _ = event["result"].(string)
			frame.finalText = safeError(frame.finalText)
			if isError, _ := event["is_error"].(bool); isError {
				frame.vendorError = frame.finalText
				frame.Event = eventPayload(sequence, ledger.EventError, map[string]any{"message": frame.finalText})
			} else {
				frame.Event = eventPayload(sequence, ledger.EventResult, map[string]any{"text": frame.finalText})
			}
		case "assistant":
			text := safeError(claudeAssistantText(event))
			frame.Event = eventPayload(sequence, ledger.EventAgentMessage, map[string]any{"text": text})
		default:
			frame.Event = eventPayload(sequence, ledger.EventStatus, map[string]any{"type": typeName})
		}
		return frame
	}
	switch typeName {
	case "thread.started":
		frame.sessionID, _ = event["thread_id"].(string)
		frame.Event = eventPayload(sequence, ledger.EventStatus, map[string]any{"type": typeName, "sessionId": frame.sessionID})
	case "item.completed":
		item, _ := event["item"].(map[string]any)
		itemType, _ := item["type"].(string)
		switch itemType {
		case "agent_message":
			frame.finalText, _ = item["text"].(string)
			frame.finalText = safeError(frame.finalText)
			frame.Event = eventPayload(sequence, ledger.EventAgentMessage, map[string]any{"text": frame.finalText})
		case "command_execution", "mcp_tool_call":
			payload := map[string]any{"itemType": itemType}
			if status, ok := item["status"].(string); ok {
				payload["status"] = status
			}
			if exitCode, ok := item["exit_code"].(float64); ok {
				payload["exitCode"] = exitCode
			}
			frame.Event = eventPayload(sequence, ledger.EventToolResult, payload)
		default:
			frame.Event = eventPayload(sequence, ledger.EventStatus, map[string]any{"type": typeName, "itemType": itemType})
		}
	case "item.started":
		item, _ := event["item"].(map[string]any)
		itemType, _ := item["type"].(string)
		frame.Event = eventPayload(sequence, ledger.EventToolCall, map[string]any{"itemType": itemType})
	case "error":
		frame.vendorError, _ = event["message"].(string)
		frame.vendorError = safeError(frame.vendorError)
		frame.Event = eventPayload(sequence, ledger.EventError, map[string]any{"message": frame.vendorError})
	case "turn.completed":
		frame.Event = eventPayload(sequence, ledger.EventResult, map[string]any{"usage": event["usage"]})
	default:
		frame.Event = eventPayload(sequence, ledger.EventStatus, map[string]any{"type": typeName})
	}
	return frame
}

func claudeAssistantText(event map[string]any) string {
	message, _ := event["message"].(map[string]any)
	content, _ := message["content"].([]any)
	var values []string
	for _, raw := range content {
		item, _ := raw.(map[string]any)
		if text, _ := item["text"].(string); text != "" {
			values = append(values, text)
		}
	}
	return strings.Join(values, "\n")
}

func eventPayload(sequence int, kind ledger.EventKind, value any) ledger.NewRunEvent {
	payload, err := json.Marshal(value)
	if err != nil {
		payload = []byte(`{"message":"normalizer could not encode event"}`)
		kind = ledger.EventError
	}
	return ledger.NewRunEvent{Sequence: sequence, Kind: kind, PayloadJSON: string(payload)}
}

func safeError(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > maxStderrBytes {
		value = value[len(value)-maxStderrBytes:]
	}
	return secretText.ReplaceAllString(value, "[redacted]")
}
