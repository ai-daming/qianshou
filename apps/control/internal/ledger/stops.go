package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type StopCondition struct {
	ID            string
	TrackID       string
	BaselineID    *string
	Kind          string
	Reason        string
	EvidenceJSON  string
	PayloadSHA256 string
	CreatedAt     string
	Resolution    *string
	OutcomeJSON   *string
	ResolvedAt    *string
}

func (s StopCondition) Open() bool { return s.ResolvedAt == nil }

type NewStopCondition struct {
	ID           string
	TrackID      string
	BaselineID   string
	Kind         string
	Reason       string
	EvidenceJSON string
}

const (
	StopContinue         = "CONTINUE"
	StopAdoptNewBaseline = "ADOPT_NEW_BASELINE"
	StopRepair           = "REPAIR"
	StopReReview         = "REREVIEW"
	StopSplit            = "SPLIT"
	StopSupersede        = "SUPERSEDE"
	StopAbandon          = "ABANDON"
)

var stopResolutions = map[string]bool{
	StopContinue: true, StopAdoptNewBaseline: true, StopRepair: true, StopReReview: true,
	StopSplit: true, StopSupersede: true, StopAbandon: true,
}

func (s *Store) OpenStopCondition(ctx context.Context, input NewStopCondition) (StopCondition, error) {
	evidence, err := canonicalJSON("stop evidence", input.EvidenceJSON)
	if err != nil {
		return StopCondition{}, err
	}
	if strings.TrimSpace(input.Kind) == "" || strings.TrimSpace(input.Reason) == "" {
		return StopCondition{}, fmt.Errorf("stop kind and reason are required: %w", ErrInvariant)
	}
	payload := strings.Join([]string{input.TrackID, input.BaselineID, input.Kind, input.Reason, evidence}, "\x00")
	value := StopCondition{ID: input.ID, TrackID: input.TrackID, Kind: input.Kind, Reason: input.Reason,
		EvidenceJSON: evidence, PayloadSHA256: sha256Text(payload), CreatedAt: nowText()}
	var baseline any
	if input.BaselineID != "" {
		value.BaselineID = &input.BaselineID
		baseline = input.BaselineID
	}
	_, insertErr := s.db.ExecContext(ctx, `INSERT INTO stop_conditions(stop_condition_id, track_id, baseline_id,
		kind, reason, evidence_json, payload_sha256, created_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		value.ID, value.TrackID, baseline, value.Kind, value.Reason, value.EvidenceJSON, value.PayloadSHA256, value.CreatedAt)
	if insertErr == nil {
		return value, nil
	}
	if !isSQLiteConstraint(insertErr) {
		return StopCondition{}, classifySQLiteWriteError(insertErr, "open stop condition", "stop condition evidence conflicts")
	}
	existing, getErr := getStop(ctx, s.db, input.ID)
	if getErr == nil && existing.PayloadSHA256 == value.PayloadSHA256 {
		return existing, nil
	}
	if getErr != nil && !errors.Is(getErr, ErrNotFound) {
		return StopCondition{}, fmt.Errorf("read stop condition after create conflict: %w", getErr)
	}
	return StopCondition{}, classifySQLiteWriteError(insertErr, "open stop condition", "stop condition id is already owned by different evidence")
}

func (s *Store) ResolveStopCondition(ctx context.Context, id, resolution, outcomeJSON string) (StopCondition, error) {
	resolution = strings.TrimSpace(resolution)
	if !stopResolutions[resolution] {
		return StopCondition{}, fmt.Errorf("stop resolution is unsupported: %w", ErrInvariant)
	}
	outcome, err := canonicalJSON("stop outcome", outcomeJSON)
	if err != nil {
		return StopCondition{}, err
	}
	existing, err := getStop(ctx, s.db, id)
	if err != nil {
		return StopCondition{}, err
	}
	if existing.ResolvedAt != nil {
		if pointerText(existing.Resolution) == resolution && pointerText(existing.OutcomeJSON) == outcome {
			return existing, nil
		}
		return StopCondition{}, conflict("stop condition already has a different resolution")
	}
	result, err := s.db.ExecContext(ctx, `UPDATE stop_conditions SET resolution = ?, outcome_json = ?, resolved_at = ?
		WHERE stop_condition_id = ? AND resolved_at IS NULL`, resolution, outcome, nowText(), id)
	if err != nil {
		return StopCondition{}, fmt.Errorf("resolve stop condition: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		current, getErr := getStop(ctx, s.db, id)
		if getErr == nil && pointerText(current.Resolution) == resolution && pointerText(current.OutcomeJSON) == outcome {
			return current, nil
		}
		return StopCondition{}, conflict("stop condition changed concurrently")
	}
	return getStop(ctx, s.db, id)
}

func getStop(ctx context.Context, query rowQuerier, id string) (StopCondition, error) {
	var value StopCondition
	err := query.QueryRowContext(ctx, `SELECT stop_condition_id, track_id, baseline_id, kind, reason,
		evidence_json, payload_sha256, created_at, resolution, outcome_json, resolved_at
		FROM stop_conditions WHERE stop_condition_id = ?`, id).Scan(&value.ID, &value.TrackID, &value.BaselineID,
		&value.Kind, &value.Reason, &value.EvidenceJSON, &value.PayloadSHA256, &value.CreatedAt,
		&value.Resolution, &value.OutcomeJSON, &value.ResolvedAt)
	if err == sql.ErrNoRows {
		return StopCondition{}, ErrNotFound
	}
	return value, err
}
