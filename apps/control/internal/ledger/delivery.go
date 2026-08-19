package ledger

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
)

func (s *Store) StartTrack(ctx context.Context, trackInput NewTrack, baselineInput NewBaseline) (DeliveryTrack, DeliveryBaseline, error) {
	if err := validateID("track id", trackInput.ID); err != nil {
		return DeliveryTrack{}, DeliveryBaseline{}, err
	}
	baseline, err := prepareBaseline(trackInput.ID, 1, baselineInput)
	if err != nil {
		return DeliveryTrack{}, DeliveryBaseline{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeliveryTrack{}, DeliveryBaseline{}, err
	}
	defer tx.Rollback()

	if existing, getErr := getTrack(ctx, tx, trackInput.ID); getErr == nil {
		existingBaseline, baselineErr := getBaselineBySequence(ctx, tx, trackInput.ID, 1)
		if baselineErr == nil && existing.ProjectID == trackInput.ProjectID && existing.IssueNumber == trackInput.IssueNumber && sameBaselinePayload(existingBaseline, baseline) {
			return existing, existingBaseline, nil
		}
		return DeliveryTrack{}, DeliveryBaseline{}, conflict("track id is already owned by another track")
	}
	createdAt := nowText()
	if _, err := tx.ExecContext(ctx, `INSERT INTO delivery_tracks(track_id, project_id, issue_number, created_at)
		VALUES(?, ?, ?, ?)`, trackInput.ID, trackInput.ProjectID, trackInput.IssueNumber, createdAt); err != nil {
		return DeliveryTrack{}, DeliveryBaseline{}, conflict("work item already has an active track or track identity is invalid")
	}
	baseline.CreatedAt = createdAt
	if err := insertBaseline(ctx, tx, baseline); err != nil {
		return DeliveryTrack{}, DeliveryBaseline{}, err
	}
	if err := tx.Commit(); err != nil {
		return DeliveryTrack{}, DeliveryBaseline{}, fmt.Errorf("commit track start: %w", err)
	}
	return DeliveryTrack{ID: trackInput.ID, ProjectID: trackInput.ProjectID, IssueNumber: trackInput.IssueNumber, CreatedAt: createdAt}, baseline, nil
}

func (s *Store) AppendBaseline(ctx context.Context, trackID string, input NewBaseline) (DeliveryBaseline, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return DeliveryBaseline{}, err
	}
	defer tx.Rollback()
	if existing, getErr := getBaselineByAdoptionKey(ctx, tx, trackID, input.AdoptionKey); getErr == nil {
		candidate, prepErr := prepareBaseline(trackID, existing.Sequence, input)
		if prepErr != nil {
			return DeliveryBaseline{}, prepErr
		}
		if sameBaselinePayload(existing, candidate) {
			return existing, nil
		}
		return DeliveryBaseline{}, conflict("baseline adoption key was reused with different evidence")
	}
	var next int
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(max(sequence), 0) + 1 FROM delivery_baselines WHERE track_id = ?`, trackID).Scan(&next); err != nil {
		return DeliveryBaseline{}, err
	}
	baseline, err := prepareBaseline(trackID, next, input)
	if err != nil {
		return DeliveryBaseline{}, err
	}
	baseline.CreatedAt = nowText()
	if err := insertBaseline(ctx, tx, baseline); err != nil {
		return DeliveryBaseline{}, err
	}
	if err := tx.Commit(); err != nil {
		return DeliveryBaseline{}, fmt.Errorf("commit baseline: %w", err)
	}
	return baseline, nil
}

func prepareBaseline(trackID string, sequence int, input NewBaseline) (DeliveryBaseline, error) {
	for field, value := range map[string]string{
		"track id": trackID, "baseline id": input.ID, "brief version id": input.BriefVersionID,
		"adoption key": input.AdoptionKey,
	} {
		if err := validateID(field, value); err != nil {
			return DeliveryBaseline{}, err
		}
	}
	if strings.TrimSpace(input.IssueUpdatedAt) == "" {
		return DeliveryBaseline{}, fmt.Errorf("issue updatedAt is required: %w", ErrInvariant)
	}
	dod, err := canonicalJSONArray("resolved DoD", input.ResolvedDoDJSON)
	if err != nil {
		return DeliveryBaseline{}, err
	}
	issueHash := sha256Text(input.IssueBody)
	payload := strings.Join([]string{trackID, input.AdoptionKey, input.IssueUpdatedAt, issueHash, input.BriefVersionID, dod}, "\x00")
	return DeliveryBaseline{ID: input.ID, TrackID: trackID, Sequence: sequence, AdoptionKey: input.AdoptionKey,
		IssueUpdatedAt: input.IssueUpdatedAt, IssueBody: input.IssueBody, IssueBodySHA256: issueHash,
		BriefVersionID: input.BriefVersionID, ResolvedDoDJSON: dod, PayloadSHA256: sha256Text(payload)}, nil
}

func insertBaseline(ctx context.Context, tx *sql.Tx, baseline DeliveryBaseline) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO delivery_baselines(
		baseline_id, track_id, sequence, adoption_key, issue_updated_at, issue_body, issue_body_sha256,
		brief_version_id, resolved_dod_json, payload_sha256, created_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, baseline.ID, baseline.TrackID, baseline.Sequence,
		baseline.AdoptionKey, baseline.IssueUpdatedAt, baseline.IssueBody, baseline.IssueBodySHA256,
		baseline.BriefVersionID, baseline.ResolvedDoDJSON, baseline.PayloadSHA256, baseline.CreatedAt)
	if err != nil {
		return conflict("baseline is not the next immutable snapshot for this active track")
	}
	return nil
}

func getTrack(ctx context.Context, query rowQuerier, id string) (DeliveryTrack, error) {
	var value DeliveryTrack
	err := query.QueryRowContext(ctx, `SELECT track_id, project_id, issue_number, runner_project_binding_id,
		workspace_path, branch, base_branch, base_sha_at_binding, created_at, terminal_kind, terminal_at
		FROM delivery_tracks WHERE track_id = ?`, id).Scan(&value.ID, &value.ProjectID, &value.IssueNumber,
		&value.RunnerProjectBindingID, &value.WorkspacePath, &value.Branch, &value.BaseBranch,
		&value.BaseSHAAtBinding, &value.CreatedAt, &value.TerminalKind, &value.TerminalAt)
	if err == sql.ErrNoRows {
		return DeliveryTrack{}, ErrNotFound
	}
	return value, err
}

func getBaselineBySequence(ctx context.Context, query rowQuerier, trackID string, sequence int) (DeliveryBaseline, error) {
	return scanBaseline(query.QueryRowContext(ctx, `SELECT baseline_id, track_id, sequence, adoption_key,
		issue_updated_at, issue_body, issue_body_sha256, brief_version_id, resolved_dod_json,
		payload_sha256, created_at FROM delivery_baselines WHERE track_id = ? AND sequence = ?`, trackID, sequence))
}

func getBaselineByAdoptionKey(ctx context.Context, query rowQuerier, trackID, key string) (DeliveryBaseline, error) {
	return scanBaseline(query.QueryRowContext(ctx, `SELECT baseline_id, track_id, sequence, adoption_key,
		issue_updated_at, issue_body, issue_body_sha256, brief_version_id, resolved_dod_json,
		payload_sha256, created_at FROM delivery_baselines WHERE track_id = ? AND adoption_key = ?`, trackID, key))
}

func scanBaseline(row *sql.Row) (DeliveryBaseline, error) {
	var value DeliveryBaseline
	err := row.Scan(&value.ID, &value.TrackID, &value.Sequence, &value.AdoptionKey, &value.IssueUpdatedAt,
		&value.IssueBody, &value.IssueBodySHA256, &value.BriefVersionID, &value.ResolvedDoDJSON,
		&value.PayloadSHA256, &value.CreatedAt)
	if err == sql.ErrNoRows {
		return DeliveryBaseline{}, ErrNotFound
	}
	return value, err
}

func sameBaselinePayload(left, right DeliveryBaseline) bool {
	return left.ID == right.ID && left.TrackID == right.TrackID && left.Sequence == right.Sequence &&
		left.AdoptionKey == right.AdoptionKey && left.PayloadSHA256 == right.PayloadSHA256
}

func (s *Store) BindTrack(ctx context.Context, trackID string, binding TrackBinding) error {
	for field, value := range map[string]string{
		"track id": trackID, "runner project binding id": binding.RunnerProjectBindingID,
		"workspace path": binding.WorkspacePath, "branch": binding.Branch,
		"base branch": binding.BaseBranch, "base SHA": binding.BaseSHA,
	} {
		if strings.TrimSpace(value) == "" {
			return fmt.Errorf("%s is required: %w", field, ErrInvariant)
		}
	}
	track, err := getTrack(ctx, s.db, trackID)
	if err != nil {
		return err
	}
	if track.RunnerProjectBindingID != nil {
		if *track.RunnerProjectBindingID == binding.RunnerProjectBindingID && *track.WorkspacePath == binding.WorkspacePath &&
			*track.Branch == binding.Branch && *track.BaseBranch == binding.BaseBranch && *track.BaseSHAAtBinding == binding.BaseSHA {
			return nil
		}
		return conflict("track worktree binding is already frozen")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE delivery_tracks SET runner_project_binding_id = ?, workspace_path = ?,
		branch = ?, base_branch = ?, base_sha_at_binding = ? WHERE track_id = ? AND runner_project_binding_id IS NULL`,
		binding.RunnerProjectBindingID, binding.WorkspacePath, binding.Branch, binding.BaseBranch, binding.BaseSHA, trackID)
	if err != nil {
		return conflict("track binding does not match the project or is no longer active")
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return conflict("track worktree binding changed concurrently")
	}
	return nil
}

func (s *Store) CreateConversation(ctx context.Context, input NewConversation) (Conversation, error) {
	if err := validateID("conversation id", input.ID); err != nil {
		return Conversation{}, err
	}
	if strings.TrimSpace(input.EngineID) == "" {
		return Conversation{}, fmt.Errorf("engine id is required: %w", ErrInvariant)
	}
	createdAt := nowText()
	_, err := s.db.ExecContext(ctx, `INSERT INTO conversations(conversation_id, project_id, issue_number, role,
		engine_id, runner_project_binding_id, created_at) VALUES(?, ?, ?, ?, ?, ?, ?)`, input.ID,
		input.ProjectID, input.IssueNumber, input.Role, input.EngineID, input.RunnerProjectBindingID, createdAt)
	if err == nil {
		return Conversation{ID: input.ID, ProjectID: input.ProjectID, IssueNumber: input.IssueNumber, Role: input.Role,
			EngineID: input.EngineID, RunnerProjectBindingID: input.RunnerProjectBindingID, CreatedAt: createdAt}, nil
	}
	existing, getErr := getConversation(ctx, s.db, input.ID)
	if getErr == nil && existing.ProjectID == input.ProjectID && existing.IssueNumber == input.IssueNumber &&
		existing.Role == input.Role && existing.EngineID == input.EngineID && existing.RunnerProjectBindingID == input.RunnerProjectBindingID {
		return existing, nil
	}
	return Conversation{}, conflict("conversation identity or affinity conflicts")
}

func getConversation(ctx context.Context, query rowQuerier, id string) (Conversation, error) {
	var value Conversation
	err := query.QueryRowContext(ctx, `SELECT conversation_id, project_id, issue_number, role, engine_id,
		runner_project_binding_id, vendor_session_id, created_at, archived_at FROM conversations WHERE conversation_id = ?`, id).
		Scan(&value.ID, &value.ProjectID, &value.IssueNumber, &value.Role, &value.EngineID,
			&value.RunnerProjectBindingID, &value.VendorSessionID, &value.CreatedAt, &value.ArchivedAt)
	if err == sql.ErrNoRows {
		return Conversation{}, ErrNotFound
	}
	return value, err
}

func (s *Store) SetVendorSession(ctx context.Context, conversationID, vendorSessionID string) error {
	vendorSessionID = strings.TrimSpace(vendorSessionID)
	if vendorSessionID == "" {
		return fmt.Errorf("vendor session id is required: %w", ErrInvariant)
	}
	conversation, err := getConversation(ctx, s.db, conversationID)
	if err != nil {
		return err
	}
	if conversation.VendorSessionID != nil {
		if *conversation.VendorSessionID == vendorSessionID {
			return nil
		}
		return conflict("vendor session id is already frozen")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE conversations SET vendor_session_id = ?
		WHERE conversation_id = ? AND vendor_session_id IS NULL`, vendorSessionID, conversationID)
	if err != nil {
		return fmt.Errorf("set vendor session: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return conflict("vendor session changed concurrently")
	}
	return nil
}

func (s *Store) ArchiveConversation(ctx context.Context, conversationID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE conversations SET archived_at = ?
		WHERE conversation_id = ? AND archived_at IS NULL`, nowText(), conversationID)
	if err != nil {
		return fmt.Errorf("archive conversation: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return conflict("conversation is missing or already archived")
	}
	return nil
}
