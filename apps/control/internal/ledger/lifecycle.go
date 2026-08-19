package ledger

import (
	"context"
	"fmt"
)

func (s *Store) ArchiveProject(ctx context.Context, projectID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE projects SET archived_at = ?
		WHERE project_id = ? AND archived_at IS NULL`, nowText(), projectID)
	if err != nil {
		return classifySQLiteWriteError(err, "archive project", "project has an active track or unfinished run")
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return conflict("project is missing or already archived")
	}
	return nil
}

func (s *Store) RetireRunner(ctx context.Context, runnerID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE runners SET retired_at = ?
		WHERE runner_id = ? AND retired_at IS NULL`, nowText(), runnerID)
	if err != nil {
		return classifySQLiteWriteError(err, "retire runner", "runner has an unfinished run")
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return conflict("runner is missing or already retired")
	}
	return nil
}

func (s *Store) RetireRunnerProjectBinding(ctx context.Context, bindingID string) error {
	result, err := s.db.ExecContext(ctx, `UPDATE runner_project_bindings SET retired_at = ?
		WHERE binding_id = ? AND retired_at IS NULL`, nowText(), bindingID)
	if err != nil {
		return classifySQLiteWriteError(err, "retire runner project binding", "binding is used by an active track")
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return conflict("binding is missing or already retired")
	}
	return nil
}

func (s *Store) TerminateTrack(ctx context.Context, trackID, kind string) error {
	if kind != "COMPLETED" && kind != "ABANDONED" {
		return fmt.Errorf("track terminal kind is invalid: %w", ErrInvariant)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE delivery_tracks SET terminal_kind = ?, terminal_at = ?
		WHERE track_id = ? AND terminal_kind IS NULL`, kind, nowText(), trackID)
	if err != nil {
		return fmt.Errorf("terminate track: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return conflict("track is missing or already terminal")
	}
	return nil
}
