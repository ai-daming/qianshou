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

func TestNormalizeLegalCombinations(t *testing.T) {
	tests := []struct {
		name   string
		labels []string
		want   Classification
	}{
		{
			name:   "delivery technical standard",
			labels: []string{"workflow:delivery", "type:technical", "rigor:standard"},
			want:   Classification{Workflow: WorkflowDelivery, Kind: KindTechnical, Rigor: RigorStandard},
		},
		{
			name:   "delivery bug lite",
			labels: []string{"workflow:delivery", "type:bug", "rigor:lite"},
			want:   Classification{Workflow: WorkflowDelivery, Kind: KindBug, Rigor: RigorLite},
		},
		{
			name:   "delivery feature high risk",
			labels: []string{"workflow:delivery", "type:feature", "rigor:high-risk"},
			want:   Classification{Workflow: WorkflowDelivery, Kind: KindFeature, Rigor: RigorHighRisk},
		},
		{
			name:   "delivery documentation standard",
			labels: []string{"workflow:delivery", "type:documentation", "rigor:standard"},
			want:   Classification{Workflow: WorkflowDelivery, Kind: KindDocumentation, Rigor: RigorStandard},
		},
		{
			name:   "control with milestone control kind",
			labels: []string{"workflow:control", "type:milestone-control", "rigor:standard"},
			want:   Classification{Workflow: WorkflowControl, Kind: KindMilestoneControl, Rigor: RigorStandard},
		},
		{
			name:   "operation with operation kind",
			labels: []string{"workflow:operation", "type:operation", "rigor:standard"},
			want:   Classification{Workflow: WorkflowOperation, Kind: KindOperation, Rigor: RigorStandard},
		},
		{
			name:   "operation without kind label",
			labels: []string{"workflow:operation", "rigor:standard"},
			want:   Classification{Workflow: WorkflowOperation, Kind: "", Rigor: RigorStandard},
		},
		{
			name: "workflow neutral labels are ignored",
			labels: []string{
				"workflow:delivery", "type:feature", "rigor:standard",
				"duplicate", "invalid", "wontfix", "good first issue", "help wanted",
			},
			want: Classification{Workflow: WorkflowDelivery, Kind: KindFeature, Rigor: RigorStandard},
		},
		{
			name:   "label case and surrounding whitespace are normalized",
			labels: []string{"Workflow:Delivery", "TYPE:Feature", " RIGOR:Standard "},
			want:   Classification{Workflow: WorkflowDelivery, Kind: KindFeature, Rigor: RigorStandard},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Normalize(tt.labels)
			if !got.Valid {
				t.Fatalf("Normalize(%v) invalid, reasons: %+v", tt.labels, reasonCodes(t, got))
			}
			if got.Classification != tt.want {
				t.Fatalf("Normalize(%v) = %+v, want %+v", tt.labels, got.Classification, tt.want)
			}
			if len(got.Reasons) != 0 {
				t.Fatalf("valid result must carry no reasons, got %+v", reasonCodes(t, got))
			}
		})
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
