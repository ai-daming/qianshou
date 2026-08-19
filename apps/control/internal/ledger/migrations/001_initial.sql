CREATE TABLE projects (
    project_id TEXT PRIMARY KEY COLLATE NOCASE CHECK (length(project_id) > 0),
    provider TEXT NOT NULL CHECK (provider = 'github'),
    repository_id INTEGER NOT NULL UNIQUE CHECK (repository_id > 0),
    creation_slug TEXT NOT NULL CHECK (length(creation_slug) > 2),
    created_at TEXT NOT NULL CHECK (length(created_at) > 0),
    archived_at TEXT
) STRICT;

CREATE TABLE runners (
    runner_id TEXT PRIMARY KEY CHECK (length(runner_id) > 0),
    display_name TEXT NOT NULL CHECK (length(display_name) > 0),
    created_at TEXT NOT NULL CHECK (length(created_at) > 0),
    retired_at TEXT
) STRICT;

CREATE TABLE runner_project_bindings (
    binding_id TEXT PRIMARY KEY CHECK (length(binding_id) > 0),
    runner_id TEXT NOT NULL REFERENCES runners(runner_id) ON DELETE RESTRICT,
    project_id TEXT COLLATE NOCASE NOT NULL REFERENCES projects(project_id) ON DELETE RESTRICT,
    main_checkout_path TEXT NOT NULL CHECK (length(main_checkout_path) > 0),
    repository_id_at_binding INTEGER NOT NULL CHECK (repository_id_at_binding > 0),
    created_at TEXT NOT NULL CHECK (length(created_at) > 0),
    retired_at TEXT
) STRICT;

CREATE UNIQUE INDEX one_active_binding_per_runner_project
    ON runner_project_bindings(runner_id, project_id)
    WHERE retired_at IS NULL;
CREATE UNIQUE INDEX one_active_binding_per_runner_path
    ON runner_project_bindings(runner_id, main_checkout_path)
    WHERE retired_at IS NULL;
CREATE INDEX runner_project_bindings_project
    ON runner_project_bindings(project_id, retired_at);

CREATE TABLE work_items (
    project_id TEXT COLLATE NOCASE NOT NULL REFERENCES projects(project_id) ON DELETE RESTRICT,
    issue_number INTEGER NOT NULL CHECK (issue_number > 0),
    created_at TEXT NOT NULL CHECK (length(created_at) > 0),
    PRIMARY KEY (project_id, issue_number)
) STRICT;

CREATE TABLE brief_versions (
    brief_version_id TEXT PRIMARY KEY CHECK (length(brief_version_id) > 0),
    project_id TEXT COLLATE NOCASE NOT NULL,
    issue_number INTEGER NOT NULL,
    content TEXT NOT NULL CHECK (length(content) > 0),
    content_sha256 TEXT NOT NULL CHECK (length(content_sha256) = 64),
    created_at TEXT NOT NULL CHECK (length(created_at) > 0),
    FOREIGN KEY (project_id, issue_number)
        REFERENCES work_items(project_id, issue_number) ON DELETE RESTRICT
) STRICT;

CREATE INDEX brief_versions_work_item
    ON brief_versions(project_id, issue_number, created_at);

CREATE TABLE delivery_tracks (
    track_id TEXT PRIMARY KEY CHECK (length(track_id) > 0),
    project_id TEXT COLLATE NOCASE NOT NULL,
    issue_number INTEGER NOT NULL,
    runner_project_binding_id TEXT REFERENCES runner_project_bindings(binding_id) ON DELETE RESTRICT,
    workspace_path TEXT,
    branch TEXT,
    base_branch TEXT,
    base_sha_at_binding TEXT,
    created_at TEXT NOT NULL CHECK (length(created_at) > 0),
    terminal_kind TEXT CHECK (terminal_kind IN ('COMPLETED', 'ABANDONED')),
    terminal_at TEXT,
    FOREIGN KEY (project_id, issue_number)
        REFERENCES work_items(project_id, issue_number) ON DELETE RESTRICT,
    CHECK (
        (runner_project_binding_id IS NULL AND workspace_path IS NULL AND branch IS NULL AND base_branch IS NULL AND base_sha_at_binding IS NULL)
        OR
        (runner_project_binding_id IS NOT NULL AND length(workspace_path) > 0 AND length(branch) > 0 AND length(base_branch) > 0 AND length(base_sha_at_binding) > 0)
    ),
    CHECK ((terminal_kind IS NULL AND terminal_at IS NULL) OR (terminal_kind IS NOT NULL AND length(terminal_at) > 0))
) STRICT;

CREATE UNIQUE INDEX one_active_track_per_work_item
    ON delivery_tracks(project_id, issue_number)
    WHERE terminal_kind IS NULL;
CREATE INDEX delivery_tracks_binding
    ON delivery_tracks(runner_project_binding_id, terminal_kind);

CREATE TABLE conversations (
    conversation_id TEXT PRIMARY KEY CHECK (length(conversation_id) > 0),
    project_id TEXT COLLATE NOCASE NOT NULL,
    issue_number INTEGER NOT NULL,
    role TEXT NOT NULL CHECK (role IN ('discussion', 'implementation', 'review', 'repair', 'integration')),
    engine_id TEXT NOT NULL CHECK (length(engine_id) > 0),
    runner_project_binding_id TEXT NOT NULL REFERENCES runner_project_bindings(binding_id) ON DELETE RESTRICT,
    vendor_session_id TEXT,
    created_at TEXT NOT NULL CHECK (length(created_at) > 0),
    archived_at TEXT,
    FOREIGN KEY (project_id, issue_number)
        REFERENCES work_items(project_id, issue_number) ON DELETE RESTRICT
) STRICT;

CREATE INDEX conversations_work_item
    ON conversations(project_id, issue_number, created_at);

CREATE TABLE delivery_baselines (
    baseline_id TEXT PRIMARY KEY CHECK (length(baseline_id) > 0),
    track_id TEXT NOT NULL REFERENCES delivery_tracks(track_id) ON DELETE RESTRICT,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    adoption_key TEXT NOT NULL CHECK (length(adoption_key) > 0),
    issue_updated_at TEXT NOT NULL CHECK (length(issue_updated_at) > 0),
    issue_body TEXT NOT NULL,
    issue_body_sha256 TEXT NOT NULL CHECK (length(issue_body_sha256) = 64),
    brief_version_id TEXT NOT NULL REFERENCES brief_versions(brief_version_id) ON DELETE RESTRICT,
    resolved_dod_json TEXT NOT NULL CHECK (json_valid(resolved_dod_json) AND json_type(resolved_dod_json) = 'array'),
    payload_sha256 TEXT NOT NULL CHECK (length(payload_sha256) = 64),
    created_at TEXT NOT NULL CHECK (length(created_at) > 0),
    UNIQUE (track_id, sequence),
    UNIQUE (track_id, adoption_key)
) STRICT;

CREATE INDEX delivery_baselines_brief
    ON delivery_baselines(brief_version_id);

CREATE TABLE agent_runs (
    run_id TEXT PRIMARY KEY CHECK (length(run_id) > 0),
    conversation_id TEXT NOT NULL REFERENCES conversations(conversation_id) ON DELETE RESTRICT,
    track_id TEXT REFERENCES delivery_tracks(track_id) ON DELETE RESTRICT,
    baseline_id TEXT REFERENCES delivery_baselines(baseline_id) ON DELETE RESTRICT,
    command_key TEXT NOT NULL UNIQUE CHECK (length(command_key) > 0),
    command_hash TEXT NOT NULL CHECK (length(command_hash) = 64),
    queued_at TEXT NOT NULL CHECK (length(queued_at) > 0),
    started_at TEXT,
    terminal_kind TEXT CHECK (terminal_kind IN ('COMPLETED', 'FAILED', 'CANCELLED', 'INTERRUPTED')),
    terminal_at TEXT,
    terminal_detail_json TEXT CHECK (terminal_detail_json IS NULL OR json_valid(terminal_detail_json)),
    CHECK ((track_id IS NULL AND baseline_id IS NULL) OR (track_id IS NOT NULL AND baseline_id IS NOT NULL)),
    CHECK (
        (terminal_kind IS NULL AND terminal_at IS NULL AND terminal_detail_json IS NULL)
        OR
        (terminal_kind IS NOT NULL AND length(terminal_at) > 0 AND terminal_detail_json IS NOT NULL)
    )
) STRICT;

CREATE UNIQUE INDEX one_unfinished_run_per_conversation
    ON agent_runs(conversation_id)
    WHERE terminal_kind IS NULL;
CREATE INDEX agent_runs_track_baseline
    ON agent_runs(track_id, baseline_id);

CREATE TABLE vendor_frames (
    run_id TEXT NOT NULL REFERENCES agent_runs(run_id) ON DELETE RESTRICT,
    frame_sequence INTEGER NOT NULL CHECK (frame_sequence > 0),
    raw_payload BLOB NOT NULL,
    payload_sha256 TEXT NOT NULL CHECK (length(payload_sha256) = 64),
    channel TEXT NOT NULL CHECK (length(channel) > 0),
    received_at TEXT NOT NULL CHECK (length(received_at) > 0),
    parse_status TEXT NOT NULL CHECK (parse_status IN ('PARSED', 'IGNORED', 'FAILED')),
    normalizer_version TEXT NOT NULL CHECK (length(normalizer_version) > 0),
    parse_error TEXT,
    CHECK ((parse_status = 'FAILED' AND length(parse_error) > 0) OR (parse_status != 'FAILED' AND parse_error IS NULL)),
    PRIMARY KEY (run_id, frame_sequence)
) STRICT;

CREATE TABLE run_events (
    run_id TEXT NOT NULL REFERENCES agent_runs(run_id) ON DELETE RESTRICT,
    sequence INTEGER NOT NULL CHECK (sequence > 0),
    source_frame_sequence INTEGER,
    event_kind TEXT NOT NULL CHECK (event_kind IN ('USER_MESSAGE', 'AGENT_MESSAGE', 'TOOL_CALL', 'TOOL_RESULT', 'STATUS', 'ERROR', 'RESULT')),
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
    payload_sha256 TEXT NOT NULL CHECK (length(payload_sha256) = 64),
    occurred_at TEXT NOT NULL CHECK (length(occurred_at) > 0),
    PRIMARY KEY (run_id, sequence),
    FOREIGN KEY (run_id, source_frame_sequence)
        REFERENCES vendor_frames(run_id, frame_sequence) ON DELETE RESTRICT
) STRICT;

CREATE INDEX run_events_source_frame
    ON run_events(run_id, source_frame_sequence);

CREATE TABLE review_rounds (
    review_round_id TEXT PRIMARY KEY CHECK (length(review_round_id) > 0),
    track_id TEXT NOT NULL REFERENCES delivery_tracks(track_id) ON DELETE RESTRICT,
    baseline_id TEXT NOT NULL REFERENCES delivery_baselines(baseline_id) ON DELETE RESTRICT,
    pull_request_number INTEGER NOT NULL CHECK (pull_request_number > 0),
    reviewed_head_sha TEXT NOT NULL CHECK (length(reviewed_head_sha) > 0),
    criterion_results_json TEXT NOT NULL CHECK (json_valid(criterion_results_json) AND json_type(criterion_results_json) = 'array'),
    verdict TEXT NOT NULL CHECK (verdict IN ('APPROVED', 'CHANGES_REQUESTED')),
    findings_json TEXT NOT NULL CHECK (json_valid(findings_json) AND json_type(findings_json) = 'array'),
    payload_sha256 TEXT NOT NULL CHECK (length(payload_sha256) = 64),
    created_at TEXT NOT NULL CHECK (length(created_at) > 0)
) STRICT;

CREATE INDEX review_rounds_track_created
    ON review_rounds(track_id, created_at);

CREATE TABLE stop_conditions (
    stop_condition_id TEXT PRIMARY KEY CHECK (length(stop_condition_id) > 0),
    track_id TEXT NOT NULL REFERENCES delivery_tracks(track_id) ON DELETE RESTRICT,
    baseline_id TEXT REFERENCES delivery_baselines(baseline_id) ON DELETE RESTRICT,
    kind TEXT NOT NULL CHECK (length(kind) > 0),
    reason TEXT NOT NULL CHECK (length(reason) > 0),
    evidence_json TEXT NOT NULL CHECK (json_valid(evidence_json)),
    payload_sha256 TEXT NOT NULL CHECK (length(payload_sha256) = 64),
    created_at TEXT NOT NULL CHECK (length(created_at) > 0),
    resolution TEXT,
    outcome_json TEXT CHECK (outcome_json IS NULL OR json_valid(outcome_json)),
    resolved_at TEXT,
    CHECK (
        (resolution IS NULL AND outcome_json IS NULL AND resolved_at IS NULL)
        OR
        (length(resolution) > 0 AND outcome_json IS NOT NULL AND length(resolved_at) > 0)
    )
) STRICT;

CREATE INDEX stop_conditions_open_track
    ON stop_conditions(track_id)
    WHERE resolved_at IS NULL;

CREATE TRIGGER projects_no_delete BEFORE DELETE ON projects BEGIN
    SELECT RAISE(ABORT, 'projects cannot be deleted');
END;
CREATE TRIGGER projects_lifecycle_update BEFORE UPDATE ON projects WHEN
    NEW.project_id != OLD.project_id OR NEW.provider != OLD.provider OR
    NEW.repository_id != OLD.repository_id OR NEW.creation_slug != OLD.creation_slug OR
    NEW.created_at != OLD.created_at OR OLD.archived_at IS NOT NULL OR NEW.archived_at IS NULL
BEGIN SELECT RAISE(ABORT, 'project fields are immutable'); END;
CREATE TRIGGER projects_archive_guard BEFORE UPDATE OF archived_at ON projects WHEN
    EXISTS (SELECT 1 FROM delivery_tracks t WHERE t.project_id = OLD.project_id AND t.terminal_kind IS NULL)
    OR EXISTS (
        SELECT 1 FROM agent_runs ar
        JOIN conversations c ON c.conversation_id = ar.conversation_id
        WHERE c.project_id = OLD.project_id AND ar.terminal_kind IS NULL
    )
BEGIN SELECT RAISE(ABORT, 'project has active delivery work'); END;

CREATE TRIGGER runners_no_delete BEFORE DELETE ON runners BEGIN
    SELECT RAISE(ABORT, 'runners cannot be deleted');
END;
CREATE TRIGGER runners_lifecycle_update BEFORE UPDATE ON runners WHEN
    NEW.runner_id != OLD.runner_id OR NEW.created_at != OLD.created_at OR
    OLD.retired_at IS NOT NULL
BEGIN SELECT RAISE(ABORT, 'runner identity and retirement are immutable'); END;
CREATE TRIGGER runners_retire_guard BEFORE UPDATE OF retired_at ON runners WHEN EXISTS (
    SELECT 1 FROM agent_runs ar
    JOIN conversations c ON c.conversation_id = ar.conversation_id
    JOIN runner_project_bindings b ON b.binding_id = c.runner_project_binding_id
    WHERE b.runner_id = OLD.runner_id AND ar.terminal_kind IS NULL
) BEGIN SELECT RAISE(ABORT, 'runner has unfinished run'); END;

CREATE TRIGGER bindings_no_delete BEFORE DELETE ON runner_project_bindings BEGIN
    SELECT RAISE(ABORT, 'runner project bindings cannot be deleted');
END;
CREATE TRIGGER bindings_lifecycle_update BEFORE UPDATE ON runner_project_bindings WHEN
    NEW.binding_id != OLD.binding_id OR NEW.runner_id != OLD.runner_id OR
    NEW.project_id != OLD.project_id OR NEW.main_checkout_path != OLD.main_checkout_path OR
    NEW.repository_id_at_binding != OLD.repository_id_at_binding OR NEW.created_at != OLD.created_at OR
    OLD.retired_at IS NOT NULL OR NEW.retired_at IS NULL
BEGIN SELECT RAISE(ABORT, 'runner project binding fields are immutable'); END;
CREATE TRIGGER bindings_repository_identity BEFORE INSERT ON runner_project_bindings WHEN NOT EXISTS (
    SELECT 1 FROM projects p
    JOIN runners r ON r.runner_id = NEW.runner_id
    WHERE p.project_id = NEW.project_id AND p.repository_id = NEW.repository_id_at_binding
      AND p.archived_at IS NULL AND r.retired_at IS NULL
) BEGIN SELECT RAISE(ABORT, 'binding repository identity or active runner does not match'); END;
CREATE TRIGGER bindings_retire_guard BEFORE UPDATE OF retired_at ON runner_project_bindings WHEN EXISTS (
    SELECT 1 FROM delivery_tracks t
    WHERE t.runner_project_binding_id = OLD.binding_id AND t.terminal_kind IS NULL
) BEGIN SELECT RAISE(ABORT, 'active track still uses binding'); END;

CREATE TRIGGER work_items_no_update BEFORE UPDATE ON work_items BEGIN
    SELECT RAISE(ABORT, 'work items are immutable');
END;
CREATE TRIGGER work_items_no_delete BEFORE DELETE ON work_items BEGIN
    SELECT RAISE(ABORT, 'work items cannot be deleted');
END;

CREATE TRIGGER brief_versions_no_update BEFORE UPDATE ON brief_versions BEGIN
    SELECT RAISE(ABORT, 'brief versions are immutable');
END;
CREATE TRIGGER brief_versions_no_delete BEFORE DELETE ON brief_versions BEGIN
    SELECT RAISE(ABORT, 'brief versions cannot be deleted');
END;

CREATE TRIGGER tracks_no_delete BEFORE DELETE ON delivery_tracks BEGIN
    SELECT RAISE(ABORT, 'delivery tracks cannot be deleted');
END;
CREATE TRIGGER tracks_binding_identity BEFORE INSERT ON delivery_tracks WHEN
    NEW.runner_project_binding_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM runner_project_bindings b
        JOIN runners r ON r.runner_id = b.runner_id
        JOIN projects p ON p.project_id = b.project_id
        WHERE b.binding_id = NEW.runner_project_binding_id AND b.project_id = NEW.project_id
          AND b.retired_at IS NULL AND r.retired_at IS NULL AND p.archived_at IS NULL
    )
BEGIN SELECT RAISE(ABORT, 'track binding does not match active project binding'); END;
CREATE TRIGGER tracks_update_guard BEFORE UPDATE ON delivery_tracks WHEN
    NEW.track_id != OLD.track_id OR NEW.project_id != OLD.project_id OR NEW.issue_number != OLD.issue_number OR
    NEW.created_at != OLD.created_at OR
    (OLD.runner_project_binding_id IS NOT NULL AND (
        NEW.runner_project_binding_id IS NOT OLD.runner_project_binding_id OR
        NEW.workspace_path IS NOT OLD.workspace_path OR NEW.branch IS NOT OLD.branch OR
        NEW.base_branch IS NOT OLD.base_branch OR NEW.base_sha_at_binding IS NOT OLD.base_sha_at_binding
    )) OR
    OLD.terminal_kind IS NOT NULL
BEGIN SELECT RAISE(ABORT, 'delivery track lifecycle is one-way'); END;
CREATE TRIGGER tracks_binding_identity_update BEFORE UPDATE OF runner_project_binding_id ON delivery_tracks WHEN
    NEW.runner_project_binding_id IS NOT NULL AND NOT EXISTS (
        SELECT 1 FROM runner_project_bindings b
        JOIN runners r ON r.runner_id = b.runner_id
        JOIN projects p ON p.project_id = b.project_id
        WHERE b.binding_id = NEW.runner_project_binding_id AND b.project_id = NEW.project_id
          AND b.retired_at IS NULL AND r.retired_at IS NULL AND p.archived_at IS NULL
    )
BEGIN SELECT RAISE(ABORT, 'track binding does not match active project binding'); END;
CREATE TRIGGER tracks_terminal_guard BEFORE UPDATE OF terminal_kind ON delivery_tracks WHEN
    NEW.terminal_kind IS NOT NULL AND EXISTS (
        SELECT 1 FROM agent_runs ar WHERE ar.track_id = OLD.track_id AND ar.terminal_kind IS NULL
    )
BEGIN SELECT RAISE(ABORT, 'track has an unfinished run'); END;

CREATE TRIGGER conversations_no_delete BEFORE DELETE ON conversations BEGIN
    SELECT RAISE(ABORT, 'conversations cannot be deleted');
END;
CREATE TRIGGER conversations_binding_identity BEFORE INSERT ON conversations WHEN NOT EXISTS (
    SELECT 1 FROM runner_project_bindings b
    JOIN runners r ON r.runner_id = b.runner_id
    JOIN projects p ON p.project_id = b.project_id
    WHERE b.binding_id = NEW.runner_project_binding_id AND b.project_id = NEW.project_id
      AND b.retired_at IS NULL AND r.retired_at IS NULL AND p.archived_at IS NULL
) BEGIN SELECT RAISE(ABORT, 'conversation binding does not match active project binding'); END;
CREATE TRIGGER conversations_update_guard BEFORE UPDATE ON conversations WHEN
    NEW.conversation_id != OLD.conversation_id OR NEW.project_id != OLD.project_id OR
    NEW.issue_number != OLD.issue_number OR NEW.role != OLD.role OR NEW.engine_id != OLD.engine_id OR
    NEW.runner_project_binding_id != OLD.runner_project_binding_id OR NEW.created_at != OLD.created_at OR
    (OLD.vendor_session_id IS NOT NULL AND NEW.vendor_session_id IS NOT OLD.vendor_session_id) OR
    OLD.archived_at IS NOT NULL OR
    (OLD.vendor_session_id IS NULL AND NEW.vendor_session_id IS NULL AND OLD.archived_at IS NEW.archived_at)
BEGIN SELECT RAISE(ABORT, 'conversation identity and lifecycle are immutable'); END;

CREATE TRIGGER baselines_no_update BEFORE UPDATE ON delivery_baselines BEGIN
    SELECT RAISE(ABORT, 'delivery baselines are immutable');
END;
CREATE TRIGGER baselines_no_delete BEFORE DELETE ON delivery_baselines BEGIN
    SELECT RAISE(ABORT, 'delivery baselines cannot be deleted');
END;
CREATE TRIGGER baselines_identity BEFORE INSERT ON delivery_baselines WHEN
    NOT EXISTS (
        SELECT 1 FROM delivery_tracks t
        JOIN brief_versions b ON b.brief_version_id = NEW.brief_version_id
        WHERE t.track_id = NEW.track_id
          AND t.project_id = b.project_id AND t.issue_number = b.issue_number
          AND t.terminal_kind IS NULL
    ) OR NEW.sequence != COALESCE((SELECT max(sequence) + 1 FROM delivery_baselines WHERE track_id = NEW.track_id), 1)
BEGIN SELECT RAISE(ABORT, 'baseline must be next for the active track and matching work item'); END;

CREATE TRIGGER agent_runs_no_delete BEFORE DELETE ON agent_runs BEGIN
    SELECT RAISE(ABORT, 'agent runs cannot be deleted');
END;
CREATE TRIGGER agent_runs_identity BEFORE INSERT ON agent_runs WHEN
    NOT EXISTS (
        SELECT 1 FROM conversations c
        WHERE c.conversation_id = NEW.conversation_id
          AND EXISTS (
              SELECT 1 FROM runner_project_bindings rb
              JOIN runners rr ON rr.runner_id = rb.runner_id
              JOIN projects rp ON rp.project_id = rb.project_id
              WHERE rb.binding_id = c.runner_project_binding_id
                AND rb.retired_at IS NULL AND rr.retired_at IS NULL AND rp.archived_at IS NULL
          )
          AND ((c.role = 'discussion' AND NEW.track_id IS NULL AND NEW.baseline_id IS NULL)
            OR (c.role != 'discussion' AND NEW.track_id IS NOT NULL AND EXISTS (
                SELECT 1 FROM delivery_baselines b
                JOIN delivery_tracks t ON t.track_id = b.track_id
                WHERE b.baseline_id = NEW.baseline_id AND b.track_id = NEW.track_id
                  AND t.project_id = c.project_id AND t.issue_number = c.issue_number
                  AND t.runner_project_binding_id = c.runner_project_binding_id
                  AND t.terminal_kind IS NULL
                  AND b.sequence = (SELECT max(sequence) FROM delivery_baselines WHERE track_id = NEW.track_id)
            )))
    )
BEGIN SELECT RAISE(ABORT, 'run baseline is missing, mismatched, or stale'); END;
CREATE TRIGGER agent_runs_update_guard BEFORE UPDATE ON agent_runs WHEN
    NEW.run_id != OLD.run_id OR NEW.conversation_id != OLD.conversation_id OR
    NEW.track_id IS NOT OLD.track_id OR NEW.baseline_id IS NOT OLD.baseline_id OR
    NEW.command_key != OLD.command_key OR NEW.command_hash != OLD.command_hash OR NEW.queued_at != OLD.queued_at OR
    (OLD.started_at IS NOT NULL AND NEW.started_at IS NOT OLD.started_at) OR
    (OLD.terminal_kind IS NOT NULL AND (
        NEW.terminal_kind IS NOT OLD.terminal_kind OR NEW.terminal_at IS NOT OLD.terminal_at OR
        NEW.terminal_detail_json IS NOT OLD.terminal_detail_json
    ))
BEGIN SELECT RAISE(ABORT, 'agent run lifecycle is one-way'); END;

CREATE TRIGGER vendor_frames_no_update BEFORE UPDATE ON vendor_frames BEGIN
    SELECT RAISE(ABORT, 'vendor frames are immutable');
END;
CREATE TRIGGER vendor_frames_no_delete BEFORE DELETE ON vendor_frames BEGIN
    SELECT RAISE(ABORT, 'vendor frames cannot be deleted');
END;
CREATE TRIGGER vendor_frames_sequence BEFORE INSERT ON vendor_frames WHEN
    NEW.frame_sequence != COALESCE((SELECT max(frame_sequence) + 1 FROM vendor_frames WHERE run_id = NEW.run_id), 1)
BEGIN SELECT RAISE(ABORT, 'vendor frame sequence must be contiguous'); END;

CREATE TRIGGER run_events_no_update BEFORE UPDATE ON run_events BEGIN
    SELECT RAISE(ABORT, 'run events are immutable');
END;
CREATE TRIGGER run_events_no_delete BEFORE DELETE ON run_events BEGIN
    SELECT RAISE(ABORT, 'run events cannot be deleted');
END;
CREATE TRIGGER run_events_sequence BEFORE INSERT ON run_events WHEN
    NEW.sequence != COALESCE((SELECT max(sequence) + 1 FROM run_events WHERE run_id = NEW.run_id), 1)
BEGIN SELECT RAISE(ABORT, 'run event sequence must be contiguous'); END;

CREATE TRIGGER review_rounds_no_update BEFORE UPDATE ON review_rounds BEGIN
    SELECT RAISE(ABORT, 'review rounds are immutable');
END;
CREATE TRIGGER review_rounds_no_delete BEFORE DELETE ON review_rounds BEGIN
    SELECT RAISE(ABORT, 'review rounds cannot be deleted');
END;
CREATE TRIGGER review_rounds_identity BEFORE INSERT ON review_rounds WHEN NOT EXISTS (
    SELECT 1 FROM delivery_baselines b
    JOIN delivery_tracks t ON t.track_id = b.track_id
    WHERE b.baseline_id = NEW.baseline_id AND b.track_id = NEW.track_id AND t.terminal_kind IS NULL
      AND b.sequence = (SELECT max(sequence) FROM delivery_baselines WHERE track_id = NEW.track_id)
) BEGIN SELECT RAISE(ABORT, 'review baseline is mismatched or stale'); END;

CREATE TRIGGER stop_conditions_no_delete BEFORE DELETE ON stop_conditions BEGIN
    SELECT RAISE(ABORT, 'stop conditions cannot be deleted');
END;
CREATE TRIGGER stop_conditions_identity BEFORE INSERT ON stop_conditions WHEN NOT EXISTS (
    SELECT 1 FROM delivery_tracks t
    WHERE t.track_id = NEW.track_id AND t.terminal_kind IS NULL
      AND (NEW.baseline_id IS NULL OR EXISTS (
          SELECT 1 FROM delivery_baselines b WHERE b.baseline_id = NEW.baseline_id AND b.track_id = NEW.track_id
      ))
) BEGIN SELECT RAISE(ABORT, 'stop condition baseline does not belong to the active track'); END;
CREATE TRIGGER stop_conditions_update_guard BEFORE UPDATE ON stop_conditions WHEN
    NEW.stop_condition_id != OLD.stop_condition_id OR NEW.track_id != OLD.track_id OR
    NEW.baseline_id IS NOT OLD.baseline_id OR NEW.kind != OLD.kind OR NEW.reason != OLD.reason OR
    NEW.evidence_json != OLD.evidence_json OR NEW.payload_sha256 != OLD.payload_sha256 OR NEW.created_at != OLD.created_at OR
    (OLD.resolved_at IS NOT NULL AND (
        NEW.resolution IS NOT OLD.resolution OR NEW.outcome_json IS NOT OLD.outcome_json OR NEW.resolved_at IS NOT OLD.resolved_at
    ))
BEGIN SELECT RAISE(ABORT, 'stop condition lifecycle is one-way'); END;
