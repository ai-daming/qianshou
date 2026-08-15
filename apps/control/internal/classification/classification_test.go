package classification

import (
	"slices"
	"testing"
)

// reasonCodes extracts the codes of a Result for assertion.
func reasonCodes(t *testing.T, result Result) []ReasonCode {
	t.Helper()
	codes := make([]ReasonCode, 0, len(result.Reasons))
	for _, reason := range result.Reasons {
		codes = append(codes, reason.Code)
	}
	return codes
}

// TestNormalizeAllLegalCombinations covers every legal classification in the
// contract: 7 workflow-kind combinations × 3 rigor values = 21 cases.
func TestNormalizeAllLegalCombinations(t *testing.T) {
	kindCases := []struct {
		name     string
		labels   []string
		workflow Workflow
		wantKind Kind
	}{
		{
			name:     "control-milestone-control",
			labels:   []string{"workflow:control", "type:milestone-control"},
			workflow: WorkflowControl,
			wantKind: KindMilestoneControl,
		},
		{
			name:     "delivery-feature",
			labels:   []string{"workflow:delivery", "type:feature"},
			workflow: WorkflowDelivery,
			wantKind: KindFeature,
		},
		{
			name:     "delivery-bug",
			labels:   []string{"workflow:delivery", "type:bug"},
			workflow: WorkflowDelivery,
			wantKind: KindBug,
		},
		{
			name:     "delivery-technical",
			labels:   []string{"workflow:delivery", "type:technical"},
			workflow: WorkflowDelivery,
			wantKind: KindTechnical,
		},
		{
			name:     "delivery-documentation",
			labels:   []string{"workflow:delivery", "type:documentation"},
			workflow: WorkflowDelivery,
			wantKind: KindDocumentation,
		},
		{
			name:     "operation-operation-kind",
			labels:   []string{"workflow:operation", "type:operation"},
			workflow: WorkflowOperation,
			wantKind: KindOperation,
		},
		{
			name:     "operation-no-kind",
			labels:   []string{"workflow:operation"},
			workflow: WorkflowOperation,
			wantKind: "",
		},
	}
	rigors := []struct {
		label string
		value Rigor
	}{
		{"rigor:lite", RigorLite},
		{"rigor:standard", RigorStandard},
		{"rigor:high-risk", RigorHighRisk},
	}

	for _, kindCase := range kindCases {
		for _, rigor := range rigors {
			t.Run(kindCase.name+"-"+string(rigor.value), func(t *testing.T) {
				labels := append(append([]string{}, kindCase.labels...), rigor.label)
				got := Normalize(labels)
				if !got.Valid {
					t.Fatalf("Normalize(%v) invalid, reasons: %+v", labels, reasonCodes(t, got))
				}
				want := Classification{Workflow: kindCase.workflow, Kind: kindCase.wantKind, Rigor: rigor.value}
				if got.Classification != want {
					t.Fatalf("Normalize(%v) = %+v, want %+v", labels, got.Classification, want)
				}
				if len(got.Reasons) != 0 {
					t.Fatalf("valid result must carry no reasons, got %+v", reasonCodes(t, got))
				}
			})
		}
	}
}

func TestNormalizeIgnoresWorkflowNeutralLabels(t *testing.T) {
	labels := []string{
		"workflow:delivery", "type:feature", "rigor:standard",
		"duplicate", "invalid", "wontfix", "good first issue", "help wanted",
	}
	got := Normalize(labels)
	want := Classification{Workflow: WorkflowDelivery, Kind: KindFeature, Rigor: RigorStandard}
	if !got.Valid || got.Classification != want {
		t.Fatalf("Normalize(%v) = %+v (valid=%v), want %+v", labels, got.Classification, got.Valid, want)
	}
}

func TestNormalizeNormalizesLabelCaseAndWhitespace(t *testing.T) {
	got := Normalize([]string{"Workflow:Delivery", "TYPE:Feature", " RIGOR:Standard "})
	want := Classification{Workflow: WorkflowDelivery, Kind: KindFeature, Rigor: RigorStandard}
	if !got.Valid || got.Classification != want {
		t.Fatalf("got %+v (valid=%v), want %+v", got.Classification, got.Valid, want)
	}
}

func TestNormalizeFailsClosed(t *testing.T) {
	tests := []struct {
		name        string
		labels      []string
		wantReasons []ReasonCode
	}{
		{
			name:        "no labels at all",
			labels:      nil,
			wantReasons: []ReasonCode{ReasonMissingWorkflow, ReasonMissingRigor},
		},
		{
			name:        "only workflow neutral labels",
			labels:      []string{"duplicate"},
			wantReasons: []ReasonCode{ReasonMissingWorkflow, ReasonMissingRigor},
		},
		{
			name:        "two workflow labels",
			labels:      []string{"workflow:delivery", "workflow:operation", "type:feature", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonMultipleWorkflow},
		},
		{
			name:        "unknown workflow label fails closed",
			labels:      []string{"workflow:discovery", "type:feature", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonUnknownLabel, ReasonMissingWorkflow},
		},
		{
			name:        "unknown type label fails closed",
			labels:      []string{"workflow:delivery", "type:investigation", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonUnknownLabel, ReasonMissingKind},
		},
		{
			name:        "unknown rigor label fails closed",
			labels:      []string{"workflow:delivery", "type:feature", "rigor:urgent"},
			wantReasons: []ReasonCode{ReasonUnknownLabel, ReasonMissingRigor},
		},
		{
			name:        "delivery without a delivery kind",
			labels:      []string{"workflow:delivery", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonMissingKind},
		},
		{
			name:        "delivery with two kind labels",
			labels:      []string{"workflow:delivery", "type:feature", "type:bug", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonMultipleKind},
		},
		{
			name:        "delivery with operation kind",
			labels:      []string{"workflow:delivery", "type:operation", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonKindWorkflowMismatch},
		},
		{
			name:        "delivery with milestone control kind",
			labels:      []string{"workflow:delivery", "type:milestone-control", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonKindWorkflowMismatch},
		},
		{
			name:        "control with a delivery kind",
			labels:      []string{"workflow:control", "type:feature", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonKindWorkflowMismatch},
		},
		{
			name:        "control without milestone control kind",
			labels:      []string{"workflow:control", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonMissingKind},
		},
		{
			name:        "operation with a delivery kind",
			labels:      []string{"workflow:operation", "type:feature", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonKindWorkflowMismatch},
		},
		{
			name:        "missing rigor",
			labels:      []string{"workflow:delivery", "type:feature"},
			wantReasons: []ReasonCode{ReasonMissingRigor},
		},
		{
			name:        "two rigor labels",
			labels:      []string{"workflow:delivery", "type:feature", "rigor:lite", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonMultipleRigor},
		},
		{
			name:        "several independent failures are reported together",
			labels:      []string{"workflow:delivery", "type:operation"},
			wantReasons: []ReasonCode{ReasonKindWorkflowMismatch, ReasonMissingRigor},
		},
		{
			name:        "kind cardinality is reported even when workflow is contradictory",
			labels:      []string{"workflow:delivery", "workflow:operation", "type:feature", "type:bug", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonMultipleWorkflow, ReasonMultipleKind},
		},
		{
			name:        "missing kind is reported when every candidate workflow requires one",
			labels:      []string{"workflow:control", "workflow:delivery", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonMultipleWorkflow, ReasonMissingKind},
		},
		{
			name:        "missing kind stays silent when a candidate workflow allows no kind",
			labels:      []string{"workflow:delivery", "workflow:operation", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonMultipleWorkflow},
		},
		{
			name:        "kind cardinality is reported even with an unknown workflow label",
			labels:      []string{"workflow:discovery", "type:feature", "type:bug", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonUnknownLabel, ReasonMissingWorkflow, ReasonMultipleKind},
		},
		{
			name:        "missing kind stays silent when the workflow is unknown entirely",
			labels:      []string{"workflow:discovery", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonUnknownLabel, ReasonMissingWorkflow},
		},
		{
			name:        "kind incompatible with every candidate workflow is reported despite ambiguity",
			labels:      []string{"workflow:control", "workflow:operation", "type:feature", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonMultipleWorkflow, ReasonKindWorkflowMismatch},
		},
		{
			name:        "kind incompatibility is reported alongside multiple kind labels",
			labels:      []string{"workflow:delivery", "type:feature", "type:operation", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonMultipleKind, ReasonKindWorkflowMismatch},
		},
		{
			name:        "kind compatible with one candidate stays silent under workflow ambiguity",
			labels:      []string{"workflow:control", "workflow:delivery", "type:feature", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonMultipleWorkflow},
		},
		{
			name:        "every incompatible kind is named in one mismatch reason",
			labels:      []string{"workflow:control", "workflow:operation", "type:feature", "type:bug", "rigor:standard"},
			wantReasons: []ReasonCode{ReasonMultipleWorkflow, ReasonMultipleKind, ReasonKindWorkflowMismatch},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Normalize(tt.labels)
			if got.Valid {
				t.Fatalf("Normalize(%v) valid, want fail closed", tt.labels)
			}
			if got.Classification != (Classification{}) {
				t.Fatalf("invalid result must carry zero classification, got %+v", got.Classification)
			}
			codes := reasonCodes(t, got)
			for _, want := range tt.wantReasons {
				if !slices.Contains(codes, want) {
					t.Fatalf("Normalize(%v) reasons = %v, want to contain %v", tt.labels, codes, want)
				}
			}
			if len(codes) != len(tt.wantReasons) {
				t.Fatalf("Normalize(%v) reasons = %v, want exactly %v", tt.labels, codes, tt.wantReasons)
			}
		})
	}
}

func TestReasonMessagesAreReadable(t *testing.T) {
	got := Normalize([]string{"workflow:delivery", "type:operation"})
	if got.Valid {
		t.Fatal("expected invalid classification")
	}
	for _, reason := range got.Reasons {
		if reason.Message == "" {
			t.Fatalf("reason %s carries an empty message", reason.Code)
		}
	}
}
