package ledger

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"strings"
)

type FrameParseStatus string

const (
	FrameParsed  FrameParseStatus = "PARSED"
	FrameIgnored FrameParseStatus = "IGNORED"
	FrameFailed  FrameParseStatus = "FAILED"
)

type VendorFrame struct {
	RunID             string
	Sequence          int
	RawPayload        []byte
	PayloadSHA256     string
	Channel           string
	ReceivedAt        string
	ParseStatus       FrameParseStatus
	NormalizerVersion string
	ParseError        *string
}

type NewVendorFrame struct {
	RunID             string
	Sequence          int
	RawPayload        []byte
	Channel           string
	ParseStatus       FrameParseStatus
	NormalizerVersion string
	ParseError        string
}

type EventKind string

const (
	EventUserMessage  EventKind = "USER_MESSAGE"
	EventAgentMessage EventKind = "AGENT_MESSAGE"
	EventToolCall     EventKind = "TOOL_CALL"
	EventToolResult   EventKind = "TOOL_RESULT"
	EventStatus       EventKind = "STATUS"
	EventError        EventKind = "ERROR"
	EventResult       EventKind = "RESULT"
)

type NewRunEvent struct {
	Sequence    int
	Kind        EventKind
	PayloadJSON string
}

type RunEvent struct {
	RunID               string
	Sequence            int
	SourceFrameSequence *int
	Kind                EventKind
	PayloadJSON         string
	PayloadSHA256       string
	OccurredAt          string
}

type RunEventPage struct {
	Events     []RunEvent
	NextCursor *int
}

func (s *Store) AppendSyntheticEvent(ctx context.Context, input NewRunEvent, runID string) error {
	prepared, err := prepareEvents(runID, 0, []NewRunEvent{input})
	if err != nil {
		return err
	}
	event := prepared[0]
	var existing RunEvent
	err = s.db.QueryRowContext(ctx, `SELECT run_id, sequence, source_frame_sequence, event_kind,
		payload_json, payload_sha256, occurred_at FROM run_events WHERE run_id = ? AND sequence = ?`, runID, input.Sequence).
		Scan(&existing.RunID, &existing.Sequence, &existing.SourceFrameSequence, &existing.Kind,
			&existing.PayloadJSON, &existing.PayloadSHA256, &existing.OccurredAt)
	if err == nil {
		if existing.SourceFrameSequence == nil && existing.Kind == event.Kind && existing.PayloadSHA256 == event.PayloadSHA256 {
			return nil
		}
		return conflict("synthetic event sequence was reused with different content")
	}
	if err != sql.ErrNoRows {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO run_events(run_id, sequence, source_frame_sequence, event_kind,
		payload_json, payload_sha256, occurred_at) VALUES(?, ?, NULL, ?, ?, ?, ?)`, runID, event.Sequence,
		event.Kind, event.PayloadJSON, event.PayloadSHA256, nowText())
	if err != nil {
		return conflict("synthetic event sequence is not the next contiguous event")
	}
	return s.secureFiles()
}

func (s *Store) AppendVendorFrame(ctx context.Context, input NewVendorFrame, events []NewRunEvent) error {
	input.Channel = strings.TrimSpace(input.Channel)
	input.NormalizerVersion = strings.TrimSpace(input.NormalizerVersion)
	input.ParseError = strings.TrimSpace(input.ParseError)
	if input.Sequence <= 0 || len(input.RawPayload) == 0 || input.Channel == "" || input.NormalizerVersion == "" {
		return fmt.Errorf("vendor frame is incomplete: %w", ErrInvariant)
	}
	if input.ParseStatus != FrameParsed && input.ParseStatus != FrameIgnored && input.ParseStatus != FrameFailed {
		return fmt.Errorf("vendor frame parse status is invalid: %w", ErrInvariant)
	}
	if input.ParseStatus == FrameIgnored && len(events) != 0 {
		return fmt.Errorf("ignored frame cannot emit events: %w", ErrInvariant)
	}
	if input.ParseStatus == FrameFailed {
		if strings.TrimSpace(input.ParseError) == "" || len(events) == 0 {
			return fmt.Errorf("failed frame requires an error and ERROR event: %w", ErrInvariant)
		}
		for _, event := range events {
			if event.Kind != EventError {
				return fmt.Errorf("failed frame may emit only ERROR events: %w", ErrInvariant)
			}
		}
	} else if input.ParseError != "" {
		return fmt.Errorf("non-failed frame cannot carry parse error: %w", ErrInvariant)
	}
	prepared, err := prepareEvents(input.RunID, input.Sequence, events)
	if err != nil {
		return err
	}
	frameHash := sha256Text(string(input.RawPayload))
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if existing, getErr := getVendorFrame(ctx, tx, input.RunID, input.Sequence); getErr == nil {
		if !sameFrame(existing, input, frameHash) {
			return conflict("vendor frame sequence was reused with different raw content")
		}
		storedEvents, eventErr := listEventsForFrame(ctx, tx, input.RunID, input.Sequence)
		if eventErr != nil || !samePreparedEvents(storedEvents, prepared) {
			return conflict("vendor frame retry has different normalized events")
		}
		return nil
	}
	var parseError any
	if input.ParseError != "" {
		parseError = input.ParseError
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO vendor_frames(run_id, frame_sequence, raw_payload, payload_sha256,
		channel, received_at, parse_status, normalizer_version, parse_error) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		input.RunID, input.Sequence, input.RawPayload, frameHash, input.Channel, nowText(), input.ParseStatus,
		input.NormalizerVersion, parseError)
	if err != nil {
		return conflict("vendor frame sequence is not the next contiguous frame")
	}
	for _, event := range prepared {
		_, err := tx.ExecContext(ctx, `INSERT INTO run_events(run_id, sequence, source_frame_sequence, event_kind,
			payload_json, payload_sha256, occurred_at) VALUES(?, ?, ?, ?, ?, ?, ?)`, input.RunID, event.Sequence,
			input.Sequence, event.Kind, event.PayloadJSON, event.PayloadSHA256, nowText())
		if err != nil {
			return conflict("run event sequence is not the next contiguous event")
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit vendor frame: %w", err)
	}
	return s.secureFiles()
}

func prepareEvents(runID string, source int, inputs []NewRunEvent) ([]RunEvent, error) {
	result := make([]RunEvent, 0, len(inputs))
	for _, input := range inputs {
		if input.Sequence <= 0 {
			return nil, fmt.Errorf("run event sequence must be positive: %w", ErrInvariant)
		}
		switch input.Kind {
		case EventUserMessage, EventAgentMessage, EventToolCall, EventToolResult, EventStatus, EventError, EventResult:
		default:
			return nil, fmt.Errorf("run event kind is invalid: %w", ErrInvariant)
		}
		payload, err := canonicalJSON("run event payload", input.PayloadJSON)
		if err != nil {
			return nil, err
		}
		var sourcePointer *int
		if source > 0 {
			sourceCopy := source
			sourcePointer = &sourceCopy
		}
		result = append(result, RunEvent{RunID: runID, Sequence: input.Sequence, SourceFrameSequence: sourcePointer,
			Kind: input.Kind, PayloadJSON: payload, PayloadSHA256: sha256Text(payload)})
	}
	return result, nil
}

func (s *Store) GetVendorFrame(ctx context.Context, runID string, sequence int) (VendorFrame, error) {
	return getVendorFrame(ctx, s.db, runID, sequence)
}

func getVendorFrame(ctx context.Context, query rowQuerier, runID string, sequence int) (VendorFrame, error) {
	var value VendorFrame
	err := query.QueryRowContext(ctx, `SELECT run_id, frame_sequence, raw_payload, payload_sha256, channel,
		received_at, parse_status, normalizer_version, parse_error FROM vendor_frames
		WHERE run_id = ? AND frame_sequence = ?`, runID, sequence).Scan(&value.RunID, &value.Sequence,
		&value.RawPayload, &value.PayloadSHA256, &value.Channel, &value.ReceivedAt, &value.ParseStatus,
		&value.NormalizerVersion, &value.ParseError)
	if err == sql.ErrNoRows {
		return VendorFrame{}, ErrNotFound
	}
	return value, err
}

func sameFrame(stored VendorFrame, input NewVendorFrame, hash string) bool {
	return stored.RunID == input.RunID && stored.Sequence == input.Sequence && bytes.Equal(stored.RawPayload, input.RawPayload) &&
		stored.PayloadSHA256 == hash && stored.Channel == input.Channel && stored.ParseStatus == input.ParseStatus &&
		stored.NormalizerVersion == input.NormalizerVersion && pointerText(stored.ParseError) == input.ParseError
}

func listEventsForFrame(ctx context.Context, query interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, runID string, frameSequence int) ([]RunEvent, error) {
	rows, err := query.QueryContext(ctx, `SELECT run_id, sequence, source_frame_sequence, event_kind,
		payload_json, payload_sha256, occurred_at FROM run_events
		WHERE run_id = ? AND source_frame_sequence = ? ORDER BY sequence`, runID, frameSequence)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanEvents(rows)
}

func samePreparedEvents(stored, prepared []RunEvent) bool {
	if len(stored) != len(prepared) {
		return false
	}
	for i := range stored {
		if stored[i].Sequence != prepared[i].Sequence || stored[i].Kind != prepared[i].Kind ||
			stored[i].PayloadSHA256 != prepared[i].PayloadSHA256 {
			return false
		}
	}
	return true
}

func (s *Store) ListRunEvents(ctx context.Context, runID string, afterSequence, limit int) (RunEventPage, error) {
	if afterSequence < 0 || limit <= 0 || limit > 1000 {
		return RunEventPage{}, fmt.Errorf("event cursor or explicit limit is invalid: %w", ErrInvariant)
	}
	rows, err := s.db.QueryContext(ctx, `SELECT run_id, sequence, source_frame_sequence, event_kind,
		payload_json, payload_sha256, occurred_at FROM run_events
		WHERE run_id = ? AND sequence > ? ORDER BY sequence LIMIT ?`, runID, afterSequence, limit+1)
	if err != nil {
		return RunEventPage{}, err
	}
	defer rows.Close()
	events, err := scanEvents(rows)
	if err != nil {
		return RunEventPage{}, err
	}
	page := RunEventPage{Events: events}
	if len(events) > limit {
		page.Events = events[:limit]
		cursor := page.Events[len(page.Events)-1].Sequence
		page.NextCursor = &cursor
	}
	return page, nil
}

func scanEvents(rows *sql.Rows) ([]RunEvent, error) {
	result := []RunEvent{}
	for rows.Next() {
		var event RunEvent
		if err := rows.Scan(&event.RunID, &event.Sequence, &event.SourceFrameSequence, &event.Kind,
			&event.PayloadJSON, &event.PayloadSHA256, &event.OccurredAt); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}
