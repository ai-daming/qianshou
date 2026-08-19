package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type ReviewVerdict string

const (
	ReviewApproved         ReviewVerdict = "APPROVED"
	ReviewChangesRequested ReviewVerdict = "CHANGES_REQUESTED"
)

type ReviewRound struct {
	ID                   string
	TrackID              string
	BaselineID           string
	PullRequestNumber    int
	ReviewedHeadSHA      string
	CriterionResultsJSON string
	Verdict              ReviewVerdict
	FindingsJSON         string
	PayloadSHA256        string
	CreatedAt            string
}

type NewReviewRound struct {
	ID                   string
	TrackID              string
	BaselineID           string
	PullRequestNumber    int
	ReviewedHeadSHA      string
	CriterionResultsJSON string
	Verdict              ReviewVerdict
	FindingsJSON         string
}

func (s *Store) RecordReviewRound(ctx context.Context, input NewReviewRound) (ReviewRound, error) {
	criteria, err := canonicalJSONArray("criterion results", input.CriterionResultsJSON)
	if err != nil {
		return ReviewRound{}, err
	}
	findings, err := canonicalJSONArray("review findings", input.FindingsJSON)
	if err != nil {
		return ReviewRound{}, err
	}
	if input.PullRequestNumber <= 0 || strings.TrimSpace(input.ReviewedHeadSHA) == "" ||
		(input.Verdict != ReviewApproved && input.Verdict != ReviewChangesRequested) {
		return ReviewRound{}, fmt.Errorf("review evidence is incomplete: %w", ErrInvariant)
	}
	payload := strings.Join([]string{input.TrackID, input.BaselineID, fmt.Sprint(input.PullRequestNumber),
		input.ReviewedHeadSHA, criteria, string(input.Verdict), findings}, "\x00")
	value := ReviewRound{ID: input.ID, TrackID: input.TrackID, BaselineID: input.BaselineID,
		PullRequestNumber: input.PullRequestNumber, ReviewedHeadSHA: input.ReviewedHeadSHA,
		CriterionResultsJSON: criteria, Verdict: input.Verdict, FindingsJSON: findings,
		PayloadSHA256: sha256Text(payload), CreatedAt: nowText()}
	_, insertErr := s.db.ExecContext(ctx, `INSERT INTO review_rounds(review_round_id, track_id, baseline_id,
		pull_request_number, reviewed_head_sha, criterion_results_json, verdict, findings_json, payload_sha256, created_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, value.ID, value.TrackID, value.BaselineID, value.PullRequestNumber,
		value.ReviewedHeadSHA, value.CriterionResultsJSON, value.Verdict, value.FindingsJSON, value.PayloadSHA256, value.CreatedAt)
	if insertErr == nil {
		return value, nil
	}
	if !isSQLiteConstraint(insertErr) {
		return ReviewRound{}, classifySQLiteWriteError(insertErr, "record review round", "review evidence conflicts")
	}
	existing, getErr := getReview(ctx, s.db, input.ID)
	if getErr == nil && existing.PayloadSHA256 == value.PayloadSHA256 {
		return existing, nil
	}
	if getErr != nil && !errors.Is(getErr, ErrNotFound) {
		return ReviewRound{}, fmt.Errorf("read review round after create conflict: %w", getErr)
	}
	return ReviewRound{}, classifySQLiteWriteError(insertErr, "record review round", "review id or frozen baseline is conflicting or stale")
}

func getReview(ctx context.Context, query rowQuerier, id string) (ReviewRound, error) {
	var value ReviewRound
	err := query.QueryRowContext(ctx, `SELECT review_round_id, track_id, baseline_id, pull_request_number,
		reviewed_head_sha, criterion_results_json, verdict, findings_json, payload_sha256, created_at
		FROM review_rounds WHERE review_round_id = ?`, id).Scan(&value.ID, &value.TrackID, &value.BaselineID,
		&value.PullRequestNumber, &value.ReviewedHeadSHA, &value.CriterionResultsJSON, &value.Verdict,
		&value.FindingsJSON, &value.PayloadSHA256, &value.CreatedAt)
	if err == sql.ErrNoRows {
		return ReviewRound{}, ErrNotFound
	}
	return value, err
}
