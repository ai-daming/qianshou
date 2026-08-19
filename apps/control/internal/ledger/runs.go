package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type RunState string

const (
	RunQueued      RunState = "QUEUED"
	RunRunning     RunState = "RUNNING"
	RunCompleted   RunState = "COMPLETED"
	RunFailed      RunState = "FAILED"
	RunCancelled   RunState = "CANCELLED"
	RunInterrupted RunState = "INTERRUPTED"
)

type AgentRun struct {
	ID                 string
	ConversationID     string
	TrackID            *string
	BaselineID         *string
	CommandKey         string
	CommandHash        string
	QueuedAt           string
	StartedAt          *string
	TerminalKind       *RunState
	TerminalAt         *string
	TerminalDetailJSON *string
}

func (r AgentRun) State() RunState {
	if r.TerminalKind != nil {
		return *r.TerminalKind
	}
	if r.StartedAt != nil {
		return RunRunning
	}
	return RunQueued
}

type NewAgentRun struct {
	ID             string
	ConversationID string
	TrackID        string
	BaselineID     string
	CommandKey     string
	CommandJSON    string
}

func (s *Store) QueueRun(ctx context.Context, input NewAgentRun) (AgentRun, error) {
	for field, value := range map[string]string{"run id": input.ID, "conversation id": input.ConversationID, "command key": input.CommandKey} {
		if err := validateID(field, value); err != nil {
			return AgentRun{}, err
		}
	}
	command, err := canonicalJSON("command", input.CommandJSON)
	if err != nil {
		return AgentRun{}, err
	}
	hash := sha256Text(command)
	var trackID, baselineID any
	if input.TrackID != "" || input.BaselineID != "" {
		if input.TrackID == "" || input.BaselineID == "" {
			return AgentRun{}, fmt.Errorf("run track and baseline must be supplied together: %w", ErrInvariant)
		}
		trackID, baselineID = input.TrackID, input.BaselineID
	}
	queuedAt := nowText()
	_, insertErr := s.db.ExecContext(ctx, `INSERT INTO agent_runs(run_id, conversation_id, track_id, baseline_id,
		command_key, command_hash, queued_at) VALUES(?, ?, ?, ?, ?, ?, ?)`, input.ID, input.ConversationID,
		trackID, baselineID, input.CommandKey, hash, queuedAt)
	if insertErr == nil {
		result := AgentRun{ID: input.ID, ConversationID: input.ConversationID, CommandKey: input.CommandKey, CommandHash: hash, QueuedAt: queuedAt}
		if input.TrackID != "" {
			result.TrackID, result.BaselineID = &input.TrackID, &input.BaselineID
		}
		return result, nil
	}
	if !isSQLiteConstraint(insertErr) {
		return AgentRun{}, classifySQLiteWriteError(insertErr, "queue run", "run conflicts")
	}
	existing, getErr := getRunByCommandKey(ctx, s.db, input.CommandKey)
	if getErr == nil && existing.ID == input.ID && existing.ConversationID == input.ConversationID &&
		pointerText(existing.TrackID) == input.TrackID && pointerText(existing.BaselineID) == input.BaselineID && existing.CommandHash == hash {
		return existing, nil
	}
	if getErr != nil && !errors.Is(getErr, ErrNotFound) {
		return AgentRun{}, fmt.Errorf("read run after queue conflict: %w", getErr)
	}
	return AgentRun{}, classifySQLiteWriteError(insertErr, "queue run", "command key, run identity, unfinished conversation, or baseline conflicts")
}

func (s *Store) GetRun(ctx context.Context, id string) (AgentRun, error) {
	return getRun(ctx, s.db, `WHERE run_id = ?`, id)
}

func getRunByCommandKey(ctx context.Context, query rowQuerier, key string) (AgentRun, error) {
	return getRun(ctx, query, `WHERE command_key = ?`, key)
}

func getRun(ctx context.Context, query rowQuerier, where string, value any) (AgentRun, error) {
	var result AgentRun
	err := query.QueryRowContext(ctx, `SELECT run_id, conversation_id, track_id, baseline_id, command_key,
		command_hash, queued_at, started_at, terminal_kind, terminal_at, terminal_detail_json FROM agent_runs `+where, value).
		Scan(&result.ID, &result.ConversationID, &result.TrackID, &result.BaselineID, &result.CommandKey,
			&result.CommandHash, &result.QueuedAt, &result.StartedAt, &result.TerminalKind, &result.TerminalAt, &result.TerminalDetailJSON)
	if err == sql.ErrNoRows {
		return AgentRun{}, ErrNotFound
	}
	return result, err
}

func (s *Store) StartRun(ctx context.Context, id string) (AgentRun, error) {
	run, err := s.GetRun(ctx, id)
	if err != nil {
		return AgentRun{}, err
	}
	if run.TerminalKind != nil {
		return AgentRun{}, conflict("terminal run cannot start")
	}
	if run.StartedAt != nil {
		return run, nil
	}
	startedAt := nowText()
	result, err := s.db.ExecContext(ctx, `UPDATE agent_runs SET started_at = ?
		WHERE run_id = ? AND started_at IS NULL AND terminal_kind IS NULL`, startedAt, id)
	if err != nil {
		return AgentRun{}, fmt.Errorf("start run: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return AgentRun{}, conflict("run changed before start")
	}
	return s.GetRun(ctx, id)
}

func (s *Store) FinishRun(ctx context.Context, id string, terminal RunState, detailJSON string) (AgentRun, error) {
	if terminal != RunCompleted && terminal != RunFailed && terminal != RunCancelled && terminal != RunInterrupted {
		return AgentRun{}, fmt.Errorf("invalid terminal run state: %w", ErrInvariant)
	}
	detail, err := canonicalJSON("terminal detail", detailJSON)
	if err != nil {
		return AgentRun{}, err
	}
	run, err := s.GetRun(ctx, id)
	if err != nil {
		return AgentRun{}, err
	}
	if run.TerminalKind != nil {
		if *run.TerminalKind == terminal && run.TerminalDetailJSON != nil && *run.TerminalDetailJSON == detail {
			return run, nil
		}
		return AgentRun{}, conflict("run already has a different terminal outcome")
	}
	if run.StartedAt == nil {
		return AgentRun{}, conflict("queued run cannot be marked terminal before it starts")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE agent_runs SET terminal_kind = ?, terminal_at = ?, terminal_detail_json = ?
		WHERE run_id = ? AND terminal_kind IS NULL AND started_at IS NOT NULL`, terminal, nowText(), detail, id)
	if err != nil {
		return AgentRun{}, fmt.Errorf("finish run: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return AgentRun{}, conflict("run changed before terminal outcome")
	}
	return s.GetRun(ctx, id)
}

func (s *Store) interruptOrphanedRuns(ctx context.Context) error {
	runningDetail := `{"reason":"server restarted and the M1 in-process runner cannot be reattached","started":true}`
	queuedDetail := `{"reason":"server restarted before the M1 in-process runner started this command","started":false}`
	_, err := s.db.ExecContext(ctx, `UPDATE agent_runs SET terminal_kind = 'INTERRUPTED', terminal_at = ?,
		terminal_detail_json = CASE WHEN started_at IS NULL THEN ? ELSE ? END
		WHERE terminal_kind IS NULL`, nowText(), queuedDetail, runningDetail)
	if err != nil {
		return fmt.Errorf("interrupt orphaned runs: %w", err)
	}
	return nil
}

func pointerText(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}
