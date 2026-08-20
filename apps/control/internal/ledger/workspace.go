package ledger

import (
	"context"
	"database/sql"
	"fmt"
)

// IssueWorkspace is a read model reconstructed from durable domain records.
// It deliberately contains no generic chat transcript or writable stage.
type IssueWorkspace struct {
	Conversations  []Conversation
	BriefVersions  []BriefVersion
	ActiveTrack    *DeliveryTrack
	Baselines      []DeliveryBaseline
	Runs           []AgentRun
	StopConditions []StopCondition
}

func (s *Store) GetActiveRunnerProjectBinding(ctx context.Context, runnerID, projectID string) (RunnerProjectBinding, error) {
	var value RunnerProjectBinding
	err := s.db.QueryRowContext(ctx, `SELECT binding_id, runner_id, project_id, main_checkout_path,
		repository_id_at_binding, created_at, retired_at
		FROM runner_project_bindings
		WHERE runner_id = ? AND project_id = ? AND retired_at IS NULL`, runnerID, projectID).Scan(
		&value.ID, &value.RunnerID, &value.ProjectID, &value.MainCheckoutPath,
		&value.RepositoryIDAtBinding, &value.CreatedAt, &value.RetiredAt)
	if err == sql.ErrNoRows {
		return RunnerProjectBinding{}, ErrNotFound
	}
	if err != nil {
		return RunnerProjectBinding{}, fmt.Errorf("get active runner project binding: %w", err)
	}
	return value, nil
}

func (s *Store) GetIssueWorkspace(ctx context.Context, projectID string, issueNumber int) (IssueWorkspace, error) {
	if err := validateID("project id", projectID); err != nil || issueNumber <= 0 {
		return IssueWorkspace{}, fmt.Errorf("work item identity is invalid: %w", ErrInvariant)
	}
	result := IssueWorkspace{
		Conversations: []Conversation{}, BriefVersions: []BriefVersion{}, Baselines: []DeliveryBaseline{},
		Runs: []AgentRun{}, StopConditions: []StopCondition{},
	}

	briefRows, err := s.db.QueryContext(ctx, `SELECT brief_version_id, project_id, issue_number, content,
		content_sha256, COALESCE(issue_updated_at, ''), COALESCE(issue_body_sha256, ''), created_at FROM brief_versions
		WHERE project_id = ? AND issue_number = ? ORDER BY created_at, brief_version_id`, projectID, issueNumber)
	if err != nil {
		return IssueWorkspace{}, fmt.Errorf("list brief versions: %w", err)
	}
	for briefRows.Next() {
		var value BriefVersion
		if err := briefRows.Scan(&value.ID, &value.ProjectID, &value.IssueNumber, &value.Content, &value.ContentSHA256,
			&value.SourceIssueUpdatedAt, &value.SourceIssueBodySHA256, &value.CreatedAt); err != nil {
			briefRows.Close()
			return IssueWorkspace{}, fmt.Errorf("scan brief version: %w", err)
		}
		result.BriefVersions = append(result.BriefVersions, value)
	}
	if err := briefRows.Err(); err != nil {
		briefRows.Close()
		return IssueWorkspace{}, fmt.Errorf("iterate brief versions: %w", err)
	}
	if err := briefRows.Close(); err != nil {
		return IssueWorkspace{}, fmt.Errorf("close brief versions: %w", err)
	}

	conversationRows, err := s.db.QueryContext(ctx, `SELECT conversation_id, project_id, issue_number, role,
		engine_id, runner_project_binding_id, vendor_session_id, created_at, archived_at
		FROM conversations WHERE project_id = ? AND issue_number = ?
		ORDER BY created_at, conversation_id`, projectID, issueNumber)
	if err != nil {
		return IssueWorkspace{}, fmt.Errorf("list conversations: %w", err)
	}
	for conversationRows.Next() {
		var value Conversation
		if err := conversationRows.Scan(&value.ID, &value.ProjectID, &value.IssueNumber, &value.Role,
			&value.EngineID, &value.RunnerProjectBindingID, &value.VendorSessionID, &value.CreatedAt, &value.ArchivedAt); err != nil {
			conversationRows.Close()
			return IssueWorkspace{}, fmt.Errorf("scan conversation: %w", err)
		}
		result.Conversations = append(result.Conversations, value)
	}
	if err := conversationRows.Err(); err != nil {
		conversationRows.Close()
		return IssueWorkspace{}, fmt.Errorf("iterate conversations: %w", err)
	}
	if err := conversationRows.Close(); err != nil {
		return IssueWorkspace{}, fmt.Errorf("close conversations: %w", err)
	}

	track, err := getActiveTrackForWorkItem(ctx, s.db, projectID, issueNumber)
	if err != nil && err != ErrNotFound {
		return IssueWorkspace{}, err
	}
	if err == nil {
		result.ActiveTrack = &track
		baselineRows, queryErr := s.db.QueryContext(ctx, `SELECT baseline_id, track_id, sequence, adoption_key,
			issue_updated_at, issue_body, issue_body_sha256, brief_version_id, resolved_dod_json,
			payload_sha256, created_at FROM delivery_baselines WHERE track_id = ? ORDER BY sequence`, track.ID)
		if queryErr != nil {
			return IssueWorkspace{}, fmt.Errorf("list baselines: %w", queryErr)
		}
		for baselineRows.Next() {
			var value DeliveryBaseline
			if scanErr := baselineRows.Scan(&value.ID, &value.TrackID, &value.Sequence, &value.AdoptionKey,
				&value.IssueUpdatedAt, &value.IssueBody, &value.IssueBodySHA256, &value.BriefVersionID,
				&value.ResolvedDoDJSON, &value.PayloadSHA256, &value.CreatedAt); scanErr != nil {
				baselineRows.Close()
				return IssueWorkspace{}, fmt.Errorf("scan baseline: %w", scanErr)
			}
			result.Baselines = append(result.Baselines, value)
		}
		if rowErr := baselineRows.Err(); rowErr != nil {
			baselineRows.Close()
			return IssueWorkspace{}, fmt.Errorf("iterate baselines: %w", rowErr)
		}
		if closeErr := baselineRows.Close(); closeErr != nil {
			return IssueWorkspace{}, fmt.Errorf("close baselines: %w", closeErr)
		}

		stopRows, queryErr := s.db.QueryContext(ctx, `SELECT stop_condition_id, track_id, baseline_id, kind,
			reason, evidence_json, payload_sha256, created_at, resolution, outcome_json, resolved_at
			FROM stop_conditions WHERE track_id = ? ORDER BY created_at, stop_condition_id`, track.ID)
		if queryErr != nil {
			return IssueWorkspace{}, fmt.Errorf("list stop conditions: %w", queryErr)
		}
		for stopRows.Next() {
			var value StopCondition
			if scanErr := stopRows.Scan(&value.ID, &value.TrackID, &value.BaselineID, &value.Kind, &value.Reason,
				&value.EvidenceJSON, &value.PayloadSHA256, &value.CreatedAt, &value.Resolution, &value.OutcomeJSON, &value.ResolvedAt); scanErr != nil {
				stopRows.Close()
				return IssueWorkspace{}, fmt.Errorf("scan stop condition: %w", scanErr)
			}
			result.StopConditions = append(result.StopConditions, value)
		}
		if rowErr := stopRows.Err(); rowErr != nil {
			stopRows.Close()
			return IssueWorkspace{}, fmt.Errorf("iterate stop conditions: %w", rowErr)
		}
		if closeErr := stopRows.Close(); closeErr != nil {
			return IssueWorkspace{}, fmt.Errorf("close stop conditions: %w", closeErr)
		}
	}

	runRows, err := s.db.QueryContext(ctx, `SELECT ar.run_id, ar.conversation_id, ar.track_id, ar.baseline_id,
		ar.command_key, ar.command_hash, ar.queued_at, ar.started_at, ar.terminal_kind, ar.terminal_at,
		ar.terminal_detail_json FROM agent_runs ar
		JOIN conversations c ON c.conversation_id = ar.conversation_id
		WHERE c.project_id = ? AND c.issue_number = ? ORDER BY ar.queued_at, ar.run_id`, projectID, issueNumber)
	if err != nil {
		return IssueWorkspace{}, fmt.Errorf("list agent runs: %w", err)
	}
	for runRows.Next() {
		var value AgentRun
		if err := runRows.Scan(&value.ID, &value.ConversationID, &value.TrackID, &value.BaselineID,
			&value.CommandKey, &value.CommandHash, &value.QueuedAt, &value.StartedAt, &value.TerminalKind,
			&value.TerminalAt, &value.TerminalDetailJSON); err != nil {
			runRows.Close()
			return IssueWorkspace{}, fmt.Errorf("scan agent run: %w", err)
		}
		result.Runs = append(result.Runs, value)
	}
	if err := runRows.Err(); err != nil {
		runRows.Close()
		return IssueWorkspace{}, fmt.Errorf("iterate agent runs: %w", err)
	}
	if err := runRows.Close(); err != nil {
		return IssueWorkspace{}, fmt.Errorf("close agent runs: %w", err)
	}
	return result, nil
}

func getActiveTrackForWorkItem(ctx context.Context, query rowQuerier, projectID string, issueNumber int) (DeliveryTrack, error) {
	var value DeliveryTrack
	err := query.QueryRowContext(ctx, `SELECT track_id, project_id, issue_number, runner_project_binding_id,
		workspace_path, branch, base_branch, base_sha_at_binding, created_at, terminal_kind, terminal_at
		FROM delivery_tracks WHERE project_id = ? AND issue_number = ? AND terminal_kind IS NULL`, projectID, issueNumber).Scan(
		&value.ID, &value.ProjectID, &value.IssueNumber, &value.RunnerProjectBindingID, &value.WorkspacePath,
		&value.Branch, &value.BaseBranch, &value.BaseSHAAtBinding, &value.CreatedAt, &value.TerminalKind, &value.TerminalAt)
	if err == sql.ErrNoRows {
		return DeliveryTrack{}, ErrNotFound
	}
	if err != nil {
		return DeliveryTrack{}, fmt.Errorf("get active delivery track: %w", err)
	}
	return value, nil
}
