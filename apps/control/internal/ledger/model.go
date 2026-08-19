package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/ai-daming/qianshou/apps/control/internal/strictjson"
)

var (
	ErrConflict  = errors.New("ledger object conflicts with existing data")
	ErrNotFound  = errors.New("ledger object not found")
	ErrInvariant = errors.New("ledger invariant rejected the operation")

	objectIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]*$`)
	slugPattern     = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)
)

type Project struct {
	ID           string
	RepositoryID int64
	CreationSlug string
	CreatedAt    string
	ArchivedAt   *string
}

type NewProject struct {
	ID           string
	RepositoryID int64
	CreationSlug string
}

type Runner struct {
	ID          string
	DisplayName string
	CreatedAt   string
	RetiredAt   *string
}

type NewRunner struct {
	ID          string
	DisplayName string
}

type RunnerProjectBinding struct {
	ID                    string
	RunnerID              string
	ProjectID             string
	MainCheckoutPath      string
	RepositoryIDAtBinding int64
	CreatedAt             string
	RetiredAt             *string
}

type NewRunnerProjectBinding struct {
	ID                    string
	RunnerID              string
	ProjectID             string
	MainCheckoutPath      string
	RepositoryIDAtBinding int64
}

type BriefVersion struct {
	ID            string
	ProjectID     string
	IssueNumber   int
	Content       string
	ContentSHA256 string
	CreatedAt     string
}

type NewBriefVersion struct {
	ID          string
	ProjectID   string
	IssueNumber int
	Content     string
}

type DeliveryTrack struct {
	ID                     string
	ProjectID              string
	IssueNumber            int
	RunnerProjectBindingID *string
	WorkspacePath          *string
	Branch                 *string
	BaseBranch             *string
	BaseSHAAtBinding       *string
	CreatedAt              string
	TerminalKind           *string
	TerminalAt             *string
}

type NewTrack struct {
	ID          string
	ProjectID   string
	IssueNumber int
}

type TrackBinding struct {
	RunnerProjectBindingID string
	WorkspacePath          string
	Branch                 string
	BaseBranch             string
	BaseSHA                string
}

type DeliveryBaseline struct {
	ID              string
	TrackID         string
	Sequence        int
	AdoptionKey     string
	IssueUpdatedAt  string
	IssueBody       string
	IssueBodySHA256 string
	BriefVersionID  string
	ResolvedDoDJSON string
	PayloadSHA256   string
	CreatedAt       string
}

type NewBaseline struct {
	ID              string
	AdoptionKey     string
	IssueUpdatedAt  string
	IssueBody       string
	BriefVersionID  string
	ResolvedDoDJSON string
}

type Role string

const (
	RoleDiscussion     Role = "discussion"
	RoleImplementation Role = "implementation"
	RoleReview         Role = "review"
	RoleRepair         Role = "repair"
	RoleIntegration    Role = "integration"
)

type Conversation struct {
	ID                     string
	ProjectID              string
	IssueNumber            int
	Role                   Role
	EngineID               string
	RunnerProjectBindingID string
	VendorSessionID        *string
	CreatedAt              string
	ArchivedAt             *string
}

type NewConversation struct {
	ID                     string
	ProjectID              string
	IssueNumber            int
	Role                   Role
	EngineID               string
	RunnerProjectBindingID string
}

func nowText() string {
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func validateID(field, value string) error {
	if !objectIDPattern.MatchString(value) {
		return fmt.Errorf("%s is missing or invalid: %w", field, ErrInvariant)
	}
	return nil
}

func sha256Text(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func canonicalJSON(field, value string) (string, error) {
	if err := strictjson.Validate([]byte(value)); err != nil {
		return "", fmt.Errorf("%s must be unambiguous JSON: %w", field, ErrInvariant)
	}
	var decoded any
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.UseNumber()
	if err := decoder.Decode(&decoded); err != nil {
		return "", fmt.Errorf("%s must be valid JSON: %w", field, ErrInvariant)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return "", fmt.Errorf("%s has trailing JSON: %w", field, ErrInvariant)
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return "", fmt.Errorf("canonicalize %s: %w", field, err)
	}
	return string(canonical), nil
}

func canonicalJSONArray(field, value string) (string, error) {
	canonical, err := canonicalJSON(field, value)
	if err != nil {
		return "", err
	}
	if len(canonical) == 0 || canonical[0] != '[' {
		return "", fmt.Errorf("%s must be a JSON array: %w", field, ErrInvariant)
	}
	return canonical, nil
}

func conflict(message string) error {
	return fmt.Errorf("%s: %w", message, ErrConflict)
}
