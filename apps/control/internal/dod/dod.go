// Package dod implements the Issue Criterion structure defined by
// docs/architecture/issue-types-goals-and-definition-of-done.md.
//
// Every Acceptance Criterion and DoD item is a structured Criterion with the
// five contract fields. There is deliberately no ProjectDoD or policy
// composition model: durable repository instructions are read from AGENTS.md
// at the Git SHA being acted on, while delivery-specific DoD belongs to the
// Issue and the adopted DeliveryBaseline. All validation fails closed and
// reports every determinable problem.
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
// Criterion decoding and any future storage decoding — so a missing or null
// required can never silently degrade a criterion to waivable. An explicit
// false is valid.
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

// ValidateIssueCriteria reports every defect in one Issue-owned criteria
// list. Criteria are not merged with repository defaults or policy records.
func ValidateIssueCriteria(criteria []Criterion) []string {
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
