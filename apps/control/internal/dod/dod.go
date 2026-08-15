// Package dod implements the Criterion structure and Definition of Done
// composition defined by docs/architecture/issue-types-goals-and-
// definition-of-done.md.
//
// Every Acceptance Criterion and DoD item is a structured Criterion with the
// five contract fields. The effective Definition of Done is composed, never
// copied: Resolved DoD = versioned Project default DoD + Issue-specific DoD.
// All validation fails closed and reports every determinable problem; an
// agent must not mark a human or external criterion complete merely because
// code or tests pass — that boundary lives in the callers, this package only
// models the contract.
//
// Freezing a resolved DoD into a DeliveryBaseline is M1-05 scope; this
// package deliberately exposes no freeze semantics.
package dod

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// VerificationMethod is how a criterion's evidence is produced and checked.
type VerificationMethod string

const (
	VerificationAutomatedTest       VerificationMethod = "AUTOMATED_TEST"
	VerificationPRReview            VerificationMethod = "PR_REVIEW"
	VerificationManualAcceptance    VerificationMethod = "MANUAL_ACCEPTANCE"
	VerificationRuntimeVerification VerificationMethod = "RUNTIME_VERIFICATION"
	VerificationExternalEvidence    VerificationMethod = "EXTERNAL_EVIDENCE"
)

var verificationMethods = map[VerificationMethod]bool{
	VerificationAutomatedTest:       true,
	VerificationPRReview:            true,
	VerificationManualAcceptance:    true,
	VerificationRuntimeVerification: true,
	VerificationExternalEvidence:    true,
}

// Criterion is one structured acceptance or DoD item. ID, Description,
// VerificationMethod, and RequiredEvidence are mandatory; Required marks the
// criterion as non-waivable and defaults to false when absent.
type Criterion struct {
	ID                 string             `json:"id"`
	Description        string             `json:"description"`
	VerificationMethod VerificationMethod `json:"verificationMethod"`
	RequiredEvidence   string             `json:"requiredEvidence"`
	Required           bool               `json:"required"`
}

// Source distinguishes where a resolved criterion came from.
type Source string

const (
	SourceProject Source = "PROJECT"
	SourceIssue   Source = "ISSUE"
)

// ProjectDoD is the repository-wide default DoD with an explicit version.
type ProjectDoD struct {
	Version  string      `json:"version"`
	Criteria []Criterion `json:"criteria"`
}

// ResolvedCriterion is one criterion in the composed DoD, with its origin.
type ResolvedCriterion struct {
	Criterion Criterion
	Source    Source
}

// ResolvedDoD is the composed DoD: versioned project default plus
// issue-specific criteria, each labelled with its source.
type ResolvedDoD struct {
	ProjectDoDVersion string
	Criteria          []ResolvedCriterion
}

// ParseCriterion parses one criterion from JSON and validates it. It fails
// closed: any syntax error or field problem returns an error carrying every
// determinable problem.
func ParseCriterion(raw []byte) (Criterion, error) {
	var criterion Criterion
	if err := json.Unmarshal(raw, &criterion); err != nil {
		return Criterion{}, fmt.Errorf("criterion json 无效: %w", err)
	}
	problems := criterion.Problems()
	if len(problems) == 0 {
		return criterion, nil
	}
	errs := make([]error, 0, len(problems))
	for _, problem := range problems {
		errs = append(errs, errors.New(problem))
	}
	return Criterion{}, errors.Join(errs...)
}

// Problems reports every field-level defect of the criterion in one pass:
// missing id, missing description, unsupported verification method, and
// missing required evidence.
func (c Criterion) Problems() []string {
	var problems []string
	if strings.TrimSpace(c.ID) == "" {
		problems = append(problems, "criterion id 不能为空")
	}
	if strings.TrimSpace(c.Description) == "" {
		problems = append(problems, "criterion description 不能为空")
	}
	if !verificationMethods[c.VerificationMethod] {
		problems = append(
			problems,
			fmt.Sprintf(
				"verificationMethod %q 不受支持：必须为 AUTOMATED_TEST / PR_REVIEW / MANUAL_ACCEPTANCE / RUNTIME_VERIFICATION / EXTERNAL_EVIDENCE",
				c.VerificationMethod,
			),
		)
	}
	if strings.TrimSpace(c.RequiredEvidence) == "" {
		problems = append(problems, "criterion requiredEvidence 不能为空")
	}
	return problems
}

// Problems reports every defect of the project default: missing version,
// per-criterion field problems, and duplicate criterion ids.
func (p ProjectDoD) Problems() []string {
	var problems []string
	if strings.TrimSpace(p.Version) == "" {
		problems = append(problems, "project DoD version 不能为空")
	}
	seen := make(map[string]bool)
	for _, criterion := range p.Criteria {
		problems = append(problems, criterion.Problems()...)
		if seen[criterion.ID] {
			problems = append(problems, fmt.Sprintf("project criterion id 重复：%s", criterion.ID))
		}
		seen[criterion.ID] = true
	}
	return problems
}

func issueProblems(criteria []Criterion) []string {
	var problems []string
	seen := make(map[string]bool)
	for _, criterion := range criteria {
		problems = append(problems, criterion.Problems()...)
		if seen[criterion.ID] {
			problems = append(problems, fmt.Sprintf("issue criterion id 重复：%s", criterion.ID))
		}
		seen[criterion.ID] = true
	}
	return problems
}

// Resolve composes the effective DoD from the versioned project default and
// the issue-specific criteria. Project criteria come first, issue criteria
// after, each labelled with its source, and the project version is carried
// onto the result. It fails closed: any problem in either side, or an id
// collision between them, yields an empty resolution plus every problem.
func Resolve(project ProjectDoD, issue []Criterion) (ResolvedDoD, []string) {
	problems := append(project.Problems(), issueProblems(issue)...)
	projectIDs := make(map[string]bool)
	for _, criterion := range project.Criteria {
		projectIDs[criterion.ID] = true
	}
	for _, criterion := range issue {
		if projectIDs[criterion.ID] {
			problems = append(
				problems,
				fmt.Sprintf("issue criterion id 与 project 默认 DoD 冲突：%s", criterion.ID),
			)
		}
	}
	if len(problems) > 0 {
		return ResolvedDoD{}, problems
	}

	resolved := ResolvedDoD{ProjectDoDVersion: project.Version}
	for _, criterion := range project.Criteria {
		resolved.Criteria = append(resolved.Criteria, ResolvedCriterion{Criterion: criterion, Source: SourceProject})
	}
	for _, criterion := range issue {
		resolved.Criteria = append(resolved.Criteria, ResolvedCriterion{Criterion: criterion, Source: SourceIssue})
	}
	return resolved, nil
}
