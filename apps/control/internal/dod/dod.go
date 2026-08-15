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

// Criterion is one structured acceptance or DoD item. All five contract
// fields are mandatory; Required must be an explicit true or false — the
// JSON boundary (ParseCriterion) rejects a missing or null required flag
// instead of silently defaulting it to false.
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

// criterionJSON is the wire shape of a Criterion. Required is a pointer so
// the JSON boundaries can distinguish an explicit false from a missing or
// null flag. Plain struct decoding never routes through Criterion's custom
// unmarshaler, so both boundaries below share this shape deliberately.
type criterionJSON struct {
	ID                 string             `json:"id"`
	Description        string             `json:"description"`
	VerificationMethod VerificationMethod `json:"verificationMethod"`
	RequiredEvidence   string             `json:"requiredEvidence"`
	Required           *bool              `json:"required"`
}

// UnmarshalJSON enforces required-flag presence on every JSON path — direct
// Criterion decoding, nested ProjectDoD decoding, and any future storage
// decoding — so a missing or null required can never silently degrade a
// criterion to waivable. An explicit false is valid.
func (c *Criterion) UnmarshalJSON(data []byte) error {
	var raw criterionJSON
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	if raw.Required == nil {
		return errors.New("criterion required 缺失或为 null：必须显式为 true 或 false")
	}
	*c = Criterion{
		ID:                 raw.ID,
		Description:        raw.Description,
		VerificationMethod: raw.VerificationMethod,
		RequiredEvidence:   raw.RequiredEvidence,
		Required:           *raw.Required,
	}
	return nil
}

// ParseCriterion parses one criterion from JSON and validates it. It fails
// closed on every field-level defect: a missing or null required flag,
// missing id, description, requiredEvidence, and an unsupported verification
// method are all reported together in one error. JSON syntax and type
// errors are returned separately, before field validation can run.
func ParseCriterion(raw []byte) (Criterion, error) {
	var decoded criterionJSON
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return Criterion{}, fmt.Errorf("criterion json 无效: %w", err)
	}
	criterion := Criterion{
		ID:                 decoded.ID,
		Description:        decoded.Description,
		VerificationMethod: decoded.VerificationMethod,
		RequiredEvidence:   decoded.RequiredEvidence,
	}
	problems := criterion.Problems()
	if decoded.Required == nil {
		problems = append(problems, "criterion required 缺失或为 null：必须显式为 true 或 false")
	} else {
		criterion.Required = *decoded.Required
	}
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
// onto the result. Criterion ids must be unique within each side but may be
// reused across sides: the (Source, ID) pair identifies a resolved criterion.
// It fails closed: any problem in either side yields an empty resolution
// plus every problem.
func Resolve(project ProjectDoD, issue []Criterion) (ResolvedDoD, []string) {
	problems := append(project.Problems(), issueProblems(issue)...)
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
