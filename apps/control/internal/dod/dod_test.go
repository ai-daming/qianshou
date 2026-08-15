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

func TestProjectDoDUnmarshalEnforcesRequiredPresence(t *testing.T) {
	t.Run("nested criterion without required fails the whole decode", func(t *testing.T) {
		raw := `{"version":"v1","criteria":[{"id":"P-1","description":"d","verificationMethod":"PR_REVIEW","requiredEvidence":"e"}]}`
		var project ProjectDoD
		if err := json.Unmarshal([]byte(raw), &project); err == nil {
			t.Fatal("nested ProjectDoD decode must enforce required presence")
		} else if !strings.Contains(err.Error(), "required") {
			t.Fatalf("error %q must mention the required flag", err.Error())
		}
	})

	t.Run("nested criterion with explicit false decodes cleanly", func(t *testing.T) {
		raw := `{"version":"v1","criteria":[{"id":"P-1","description":"d","verificationMethod":"PR_REVIEW","requiredEvidence":"e","required":false}]}`
		var project ProjectDoD
		if err := json.Unmarshal([]byte(raw), &project); err != nil {
			t.Fatalf("explicit false must decode: %v", err)
		}
		if len(project.Problems()) != 0 {
			t.Fatalf("decoded project must be problem-free, got %v", project.Problems())
		}
		if project.Criteria[0].Required {
			t.Fatal("explicit false must stay false through nested decode")
		}
	})
}

func TestProjectDoDProblems(t *testing.T) {
	tests := []struct {
		name    string
		project ProjectDoD
		want    []string
	}{
		{
			name:    "missing version",
			project: ProjectDoD{Criteria: []Criterion{validCriterion("P-1", "PR_REVIEW")}},
			want:    []string{"version"},
		},
		{
			name: "duplicate project criterion ids",
			project: ProjectDoD{
				Version:  "v1",
				Criteria: []Criterion{validCriterion("P-1", "PR_REVIEW"), validCriterion("P-1", "AUTOMATED_TEST")},
			},
			want: []string{"P-1"},
		},
		{
			name: "invalid criterion problems surface",
			project: ProjectDoD{
				Version:  "v1",
				Criteria: []Criterion{{ID: "P-1", Description: "d", VerificationMethod: "GUESS"}},
			},
			want: []string{"verificationMethod"},
		},
		{
			name: "valid project default",
			project: ProjectDoD{
				Version:  "v1",
				Criteria: []Criterion{validCriterion("P-1", "PR_REVIEW"), validCriterion("P-2", "AUTOMATED_TEST")},
			},
			want: nil,
		},
		{
			name:    "empty criteria with version is representable",
			project: ProjectDoD{Version: "v0"},
			want:    nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			problems := tt.project.Problems()
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

func TestResolveComposesVersionedDefaultWithIssueSpecific(t *testing.T) {
	project := ProjectDoD{
		Version:  "2026-08-15.1",
		Criteria: []Criterion{validCriterion("P-1", "PR_REVIEW"), validCriterion("P-2", "AUTOMATED_TEST")},
	}
	issue := []Criterion{validCriterion("I-1", "RUNTIME_VERIFICATION"), validCriterion("I-2", "MANUAL_ACCEPTANCE")}

	resolved, problems := Resolve(project, issue)
	if len(problems) != 0 {
		t.Fatalf("Resolve problems: %v", problems)
	}
	if resolved.ProjectDoDVersion != "2026-08-15.1" {
		t.Fatalf("resolved must record the project DoD version, got %q", resolved.ProjectDoDVersion)
	}
	if len(resolved.Criteria) != 4 {
		t.Fatalf("resolved criteria = %d, want 4", len(resolved.Criteria))
	}
	wantSources := []Source{SourceProject, SourceProject, SourceIssue, SourceIssue}
	for i, want := range wantSources {
		if resolved.Criteria[i].Source != want {
			t.Fatalf("criteria[%d].Source = %s, want %s", i, resolved.Criteria[i].Source, want)
		}
	}
	if resolved.Criteria[0].Criterion.ID != "P-1" || resolved.Criteria[2].Criterion.ID != "I-1" {
		t.Fatalf("project criteria must precede issue criteria, got %+v", resolved.Criteria)
	}
}

func TestResolveWithoutIssueCriteria(t *testing.T) {
	project := ProjectDoD{Version: "v1", Criteria: []Criterion{validCriterion("P-1", "PR_REVIEW")}}
	resolved, problems := Resolve(project, nil)
	if len(problems) != 0 || len(resolved.Criteria) != 1 || resolved.Criteria[0].Source != SourceProject {
		t.Fatalf("issue-free resolution must pass through the project default: %+v %v", resolved, problems)
	}
}

func TestResolveAllowsSharedIdAcrossSources(t *testing.T) {
	project := ProjectDoD{Version: "v1", Criteria: []Criterion{validCriterion("SHARED", "PR_REVIEW")}}
	issue := []Criterion{validCriterion("SHARED", "RUNTIME_VERIFICATION")}

	resolved, problems := Resolve(project, issue)
	if len(problems) != 0 {
		t.Fatalf("cross-source id reuse is legal, problems: %v", problems)
	}
	if len(resolved.Criteria) != 2 {
		t.Fatalf("resolved criteria = %d, want both", len(resolved.Criteria))
	}
	if resolved.Criteria[0].Criterion.ID != "SHARED" || resolved.Criteria[0].Source != SourceProject {
		t.Fatalf("project entry wrong: %+v", resolved.Criteria[0])
	}
	if resolved.Criteria[1].Criterion.ID != "SHARED" || resolved.Criteria[1].Source != SourceIssue {
		t.Fatalf("issue entry wrong: %+v", resolved.Criteria[1])
	}
}

func TestResolveFailsClosed(t *testing.T) {
	tests := []struct {
		name    string
		project ProjectDoD
		issue   []Criterion
		want    []string
	}{
		{
			name:    "invalid project default",
			project: ProjectDoD{Criteria: []Criterion{validCriterion("P-1", "PR_REVIEW")}},
			issue:   nil,
			want:    []string{"version"},
		},
		{
			name:    "invalid issue criterion",
			project: ProjectDoD{Version: "v1"},
			issue:   []Criterion{{ID: "I-1", Description: "d", VerificationMethod: "GUESS"}},
			want:    []string{"verificationMethod"},
		},
		{
			name:    "duplicate issue criterion ids",
			project: ProjectDoD{Version: "v1"},
			issue:   []Criterion{validCriterion("I-1", "PR_REVIEW"), validCriterion("I-1", "AUTOMATED_TEST")},
			want:    []string{"I-1"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved, problems := Resolve(tt.project, tt.issue)
			if len(resolved.Criteria) != 0 || resolved.ProjectDoDVersion != "" {
				t.Fatalf("invalid input must produce an empty resolution, got %+v", resolved)
			}
			for _, want := range tt.want {
				if !slices.ContainsFunc(problems, func(p string) bool { return strings.Contains(p, want) }) {
					t.Fatalf("problems %v must mention %q", problems, want)
				}
			}
		})
	}
}
