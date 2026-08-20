package dod

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

const validCriterionJSON = `{
  "id": "AC-1",
  "description": "仓库提供每种模板的结构说明",
  "verificationMethod": "AUTOMATED_TEST",
  "requiredEvidence": "go test ./... 输出",
  "required": true
}`

func TestParseCriterionValidAcrossAllVerificationMethods(t *testing.T) {
	methods := []VerificationMethod{
		VerificationAutomatedTest,
		VerificationPRReview,
		VerificationManualAcceptance,
		VerificationRuntimeVerification,
		VerificationExternalEvidence,
	}
	for _, method := range methods {
		t.Run(string(method), func(t *testing.T) {
			raw := strings.Replace(validCriterionJSON, "AUTOMATED_TEST", string(method), 1)
			criterion, err := ParseCriterion([]byte(raw))
			if err != nil {
				t.Fatalf("ParseCriterion(%s) error: %v", method, err)
			}
			if criterion.ID != "AC-1" || criterion.Description == "" {
				t.Fatalf("parsed criterion incomplete: %+v", criterion)
			}
			if criterion.VerificationMethod != method {
				t.Fatalf("verificationMethod = %s, want %s", criterion.VerificationMethod, method)
			}
			if !criterion.Required {
				t.Fatal("required must round-trip as true")
			}
		})
	}
}

func TestParseCriterionRequiresExplicitRequiredFlag(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "absent required flag fails closed",
			raw:  `{"id":"D-1","description":"人工复核","verificationMethod":"MANUAL_ACCEPTANCE","requiredEvidence":"复核记录"}`,
		},
		{
			name: "null required flag fails closed",
			raw:  `{"id":"D-1","description":"人工复核","verificationMethod":"MANUAL_ACCEPTANCE","requiredEvidence":"复核记录","required":null}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseCriterion([]byte(tt.raw))
			if err == nil {
				t.Fatal("missing or null required flag must fail closed")
			}
			if !strings.Contains(err.Error(), "required") {
				t.Fatalf("error %q must mention the required flag", err.Error())
			}
		})
	}

	t.Run("explicit false stays optional", func(t *testing.T) {
		raw := `{"id":"D-1","description":"人工复核","verificationMethod":"MANUAL_ACCEPTANCE","requiredEvidence":"复核记录","required":false}`
		criterion, err := ParseCriterion([]byte(raw))
		if err != nil {
			t.Fatalf("explicit false must parse: %v", err)
		}
		if criterion.Required {
			t.Fatal("explicit false must round-trip as false")
		}
	})
}

func TestParseCriterionFailures(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{
			name: "invalid json",
			raw:  `{"id":`,
			want: []string{"json"},
		},
		{
			name: "missing id",
			raw:  `{"description":"d","verificationMethod":"PR_REVIEW","requiredEvidence":"e"}`,
			want: []string{"id"},
		},
		{
			name: "missing description",
			raw:  `{"id":"D-1","verificationMethod":"PR_REVIEW","requiredEvidence":"e"}`,
			want: []string{"description"},
		},
		{
			name: "invalid verification method",
			raw:  `{"id":"D-1","description":"d","verificationMethod":"GUESS","requiredEvidence":"e"}`,
			want: []string{"verificationMethod"},
		},
		{
			name: "missing required evidence",
			raw:  `{"id":"D-1","description":"d","verificationMethod":"PR_REVIEW"}`,
			want: []string{"requiredEvidence"},
		},
		{
			name: "all field problems are reported together",
			raw:  `{"id":"","description":"","verificationMethod":"GUESS","requiredEvidence":""}`,
			want: []string{"id", "description", "verificationMethod", "requiredEvidence", "required"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseCriterion([]byte(tt.raw))
			if err == nil {
				t.Fatal("ParseCriterion must fail closed on invalid input")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error %q must mention %q", err.Error(), want)
				}
			}
		})
	}
}

func validCriterion(id, method string) Criterion {
	return Criterion{
		ID:                 id,
		Description:        "desc " + id,
		VerificationMethod: VerificationMethod(method),
		RequiredEvidence:   "evidence " + id,
		Required:           true,
	}
}

func TestCriterionUnmarshalEnforcesRequiredPresence(t *testing.T) {
	tests := []struct {
		name string
		raw  string
	}{
		{
			name: "direct unmarshal without required fails",
			raw:  `{"id":"D-1","description":"d","verificationMethod":"PR_REVIEW","requiredEvidence":"e"}`,
		},
		{
			name: "direct unmarshal with null required fails",
			raw:  `{"id":"D-1","description":"d","verificationMethod":"PR_REVIEW","requiredEvidence":"e","required":null}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var criterion Criterion
			if err := json.Unmarshal([]byte(tt.raw), &criterion); err == nil {
				t.Fatal("direct Criterion unmarshal must enforce required presence")
			} else if !strings.Contains(err.Error(), "required") {
				t.Fatalf("error %q must mention the required flag", err.Error())
			}
		})
	}

	t.Run("direct unmarshal with explicit false decodes", func(t *testing.T) {
		var criterion Criterion
		raw := `{"id":"D-1","description":"d","verificationMethod":"PR_REVIEW","requiredEvidence":"e","required":false}`
		if err := json.Unmarshal([]byte(raw), &criterion); err != nil {
			t.Fatalf("explicit false must decode: %v", err)
		}
		if criterion.Required {
			t.Fatal("explicit false must stay false")
		}
	})
}

func TestValidateIssueCriteria(t *testing.T) {
	tests := []struct {
		name     string
		criteria []Criterion
		want     []string
	}{
		{
			name:     "duplicate issue criterion ids",
			criteria: []Criterion{validCriterion("D-1", "PR_REVIEW"), validCriterion("D-1", "AUTOMATED_TEST")},
			want:     []string{"D-1"},
		},
		{
			name:     "invalid criterion problems surface",
			criteria: []Criterion{{ID: "D-1", Description: "d", VerificationMethod: "GUESS"}},
			want:     []string{"verificationMethod"},
		},
		{
			name:     "valid issue DoD",
			criteria: []Criterion{validCriterion("D-1", "PR_REVIEW"), validCriterion("D-2", "AUTOMATED_TEST")},
			want:     nil,
		},
		{
			name: "empty issue DoD is representable",
			want: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems := ValidateIssueCriteria(tt.criteria)
			for _, want := range tt.want {
				found := slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, want) })
				if !found {
					t.Fatalf("problems %v must mention %q", problems, want)
				}
			}
			if tt.want == nil && len(problems) != 0 {
				t.Fatalf("expected no problems, got %v", problems)
			}
		})
	}
}
