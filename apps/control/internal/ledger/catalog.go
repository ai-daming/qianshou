package ledger

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

func (s *Store) CreateProject(ctx context.Context, input NewProject) (Project, error) {
	if err := validateID("project id", input.ID); err != nil {
		return Project{}, err
	}
	if input.RepositoryID <= 0 || !slugPattern.MatchString(input.CreationSlug) {
		return Project{}, fmt.Errorf("project repository identity is invalid: %w", ErrInvariant)
	}
	createdAt := nowText()
	_, err := s.db.ExecContext(ctx, `INSERT INTO projects(project_id, provider, repository_id, creation_slug, created_at)
		VALUES(?, 'github', ?, ?, ?)`, input.ID, input.RepositoryID, input.CreationSlug, createdAt)
	if err == nil {
		return Project{ID: input.ID, RepositoryID: input.RepositoryID, CreationSlug: input.CreationSlug, CreatedAt: createdAt}, nil
	}
	if !isSQLiteConstraint(err) {
		return Project{}, classifySQLiteWriteError(err, "create project", "project identity conflicts")
	}
	existing, getErr := s.GetProject(ctx, input.ID)
	if getErr == nil && existing.ArchivedAt == nil && existing.RepositoryID == input.RepositoryID && existing.CreationSlug == input.CreationSlug {
		return existing, nil
	}
	if getErr != nil && !errors.Is(getErr, ErrNotFound) {
		return Project{}, fmt.Errorf("read project after create conflict: %w", getErr)
	}
	return Project{}, classifySQLiteWriteError(err, "create project", "project id or GitHub repository id is already owned")
}

func (s *Store) GetProject(ctx context.Context, id string) (Project, error) {
	var value Project
	err := s.db.QueryRowContext(ctx, `SELECT project_id, repository_id, creation_slug, created_at, archived_at
		FROM projects WHERE project_id = ?`, id).Scan(
		&value.ID, &value.RepositoryID, &value.CreationSlug, &value.CreatedAt, &value.ArchivedAt)
	if err == sql.ErrNoRows {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, fmt.Errorf("get project: %w", err)
	}
	return value, nil
}

func (s *Store) ListProjects(ctx context.Context) ([]Project, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT project_id, repository_id, creation_slug, created_at, archived_at
		FROM projects WHERE archived_at IS NULL ORDER BY project_id`)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()
	result := []Project{}
	for rows.Next() {
		var value Project
		if err := rows.Scan(&value.ID, &value.RepositoryID, &value.CreationSlug, &value.CreatedAt, &value.ArchivedAt); err != nil {
			return nil, fmt.Errorf("list projects: %w", err)
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (s *Store) CreateRunner(ctx context.Context, input NewRunner) (Runner, error) {
	if err := validateID("runner id", input.ID); err != nil {
		return Runner{}, err
	}
	input.DisplayName = strings.TrimSpace(input.DisplayName)
	if input.DisplayName == "" {
		return Runner{}, fmt.Errorf("runner display name is required: %w", ErrInvariant)
	}
	createdAt := nowText()
	_, err := s.db.ExecContext(ctx, `INSERT INTO runners(runner_id, display_name, created_at) VALUES(?, ?, ?)`,
		input.ID, input.DisplayName, createdAt)
	if err == nil {
		return Runner{ID: input.ID, DisplayName: input.DisplayName, CreatedAt: createdAt}, nil
	}
	if !isSQLiteConstraint(err) {
		return Runner{}, classifySQLiteWriteError(err, "create runner", "runner identity conflicts")
	}
	var existing Runner
	getErr := s.db.QueryRowContext(ctx, `SELECT runner_id, display_name, created_at, retired_at FROM runners WHERE runner_id = ?`, input.ID).
		Scan(&existing.ID, &existing.DisplayName, &existing.CreatedAt, &existing.RetiredAt)
	if getErr == nil && existing.RetiredAt == nil && existing.DisplayName == input.DisplayName {
		return existing, nil
	}
	if getErr != nil && !errors.Is(getErr, sql.ErrNoRows) {
		return Runner{}, fmt.Errorf("read runner after create conflict: %w", getErr)
	}
	return Runner{}, classifySQLiteWriteError(err, "create runner", "runner id is already owned")
}

func (s *Store) UpdateRunnerDisplayName(ctx context.Context, runnerID, displayName string) error {
	displayName = strings.TrimSpace(displayName)
	if displayName == "" {
		return fmt.Errorf("runner display name is required: %w", ErrInvariant)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE runners SET display_name = ?
		WHERE runner_id = ? AND retired_at IS NULL`, displayName, runnerID)
	if err != nil {
		return fmt.Errorf("update runner display name: %w", err)
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) CreateRunnerProjectBinding(ctx context.Context, input NewRunnerProjectBinding) (RunnerProjectBinding, error) {
	for field, value := range map[string]string{"binding id": input.ID, "runner id": input.RunnerID, "project id": input.ProjectID} {
		if err := validateID(field, value); err != nil {
			return RunnerProjectBinding{}, err
		}
	}
	input.MainCheckoutPath = filepath.Clean(strings.TrimSpace(input.MainCheckoutPath))
	if !filepath.IsAbs(input.MainCheckoutPath) || input.RepositoryIDAtBinding <= 0 {
		return RunnerProjectBinding{}, fmt.Errorf("binding path or repository identity is invalid: %w", ErrInvariant)
	}
	createdAt := nowText()
	_, err := s.db.ExecContext(ctx, `INSERT INTO runner_project_bindings(
		binding_id, runner_id, project_id, main_checkout_path, repository_id_at_binding, created_at
	) VALUES(?, ?, ?, ?, ?, ?)`, input.ID, input.RunnerID, input.ProjectID, input.MainCheckoutPath, input.RepositoryIDAtBinding, createdAt)
	if err == nil {
		return RunnerProjectBinding{ID: input.ID, RunnerID: input.RunnerID, ProjectID: input.ProjectID,
			MainCheckoutPath: input.MainCheckoutPath, RepositoryIDAtBinding: input.RepositoryIDAtBinding, CreatedAt: createdAt}, nil
	}
	if !isSQLiteConstraint(err) {
		return RunnerProjectBinding{}, classifySQLiteWriteError(err, "create runner project binding", "runner project binding conflicts")
	}
	existing, getErr := getBinding(ctx, s.db, input.ID)
	if getErr == nil && existing.RetiredAt == nil && existing.RunnerID == input.RunnerID && existing.ProjectID == input.ProjectID &&
		existing.MainCheckoutPath == input.MainCheckoutPath && existing.RepositoryIDAtBinding == input.RepositoryIDAtBinding {
		return existing, nil
	}
	if getErr != nil && !errors.Is(getErr, ErrNotFound) {
		return RunnerProjectBinding{}, fmt.Errorf("read runner project binding after create conflict: %w", getErr)
	}
	return RunnerProjectBinding{}, classifySQLiteWriteError(err, "create runner project binding", "runner project binding conflicts with an existing active binding")
}

type rowQuerier interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func getBinding(ctx context.Context, query rowQuerier, id string) (RunnerProjectBinding, error) {
	var value RunnerProjectBinding
	err := query.QueryRowContext(ctx, `SELECT binding_id, runner_id, project_id, main_checkout_path,
		repository_id_at_binding, created_at, retired_at FROM runner_project_bindings WHERE binding_id = ?`, id).
		Scan(&value.ID, &value.RunnerID, &value.ProjectID, &value.MainCheckoutPath,
			&value.RepositoryIDAtBinding, &value.CreatedAt, &value.RetiredAt)
	if err == sql.ErrNoRows {
		return RunnerProjectBinding{}, ErrNotFound
	}
	return value, err
}

func (s *Store) EnsureWorkItem(ctx context.Context, projectID string, issueNumber int) error {
	if err := validateID("project id", projectID); err != nil || issueNumber <= 0 {
		return fmt.Errorf("work item identity is invalid: %w", ErrInvariant)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO work_items(project_id, issue_number, created_at)
		VALUES(?, ?, ?) ON CONFLICT(project_id, issue_number) DO NOTHING`, projectID, issueNumber, nowText()); err != nil {
		return classifySQLiteWriteError(err, "ensure work item", "work item identity conflicts")
	}
	return nil
}

func (s *Store) CreateBriefVersion(ctx context.Context, input NewBriefVersion) (BriefVersion, error) {
	if err := validateID("brief version id", input.ID); err != nil {
		return BriefVersion{}, err
	}
	if strings.TrimSpace(input.Content) == "" || input.IssueNumber <= 0 || strings.TrimSpace(input.SourceIssueUpdatedAt) == "" ||
		len(input.SourceIssueBodySHA256) != 64 {
		return BriefVersion{}, fmt.Errorf("brief content or source Issue evidence is invalid: %w", ErrInvariant)
	}
	hash := sha256Text(input.Content)
	createdAt := nowText()
	_, err := s.db.ExecContext(ctx, `INSERT INTO brief_versions(
		brief_version_id, project_id, issue_number, content, content_sha256, issue_updated_at, issue_body_sha256, created_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`, input.ID, input.ProjectID, input.IssueNumber, input.Content, hash,
		input.SourceIssueUpdatedAt, strings.ToLower(input.SourceIssueBodySHA256), createdAt)
	if err == nil {
		return BriefVersion{ID: input.ID, ProjectID: input.ProjectID, IssueNumber: input.IssueNumber,
			Content: input.Content, ContentSHA256: hash, SourceIssueUpdatedAt: input.SourceIssueUpdatedAt,
			SourceIssueBodySHA256: strings.ToLower(input.SourceIssueBodySHA256), CreatedAt: createdAt}, nil
	}
	if !isSQLiteConstraint(err) {
		return BriefVersion{}, classifySQLiteWriteError(err, "create brief version", "brief version conflicts")
	}
	var existing BriefVersion
	getErr := s.db.QueryRowContext(ctx, `SELECT brief_version_id, project_id, issue_number, content, content_sha256,
		COALESCE(issue_updated_at, ''), COALESCE(issue_body_sha256, ''), created_at
		FROM brief_versions WHERE brief_version_id = ?`, input.ID).Scan(&existing.ID, &existing.ProjectID,
		&existing.IssueNumber, &existing.Content, &existing.ContentSHA256, &existing.SourceIssueUpdatedAt,
		&existing.SourceIssueBodySHA256, &existing.CreatedAt)
	if getErr == nil && existing.ProjectID == input.ProjectID && existing.IssueNumber == input.IssueNumber &&
		existing.ContentSHA256 == hash && existing.SourceIssueUpdatedAt == input.SourceIssueUpdatedAt &&
		existing.SourceIssueBodySHA256 == strings.ToLower(input.SourceIssueBodySHA256) {
		return existing, nil
	}
	if getErr != nil && !errors.Is(getErr, sql.ErrNoRows) {
		return BriefVersion{}, fmt.Errorf("read brief version after create conflict: %w", getErr)
	}
	return BriefVersion{}, classifySQLiteWriteError(err, "create brief version", "brief version id is already owned by different content")
}
