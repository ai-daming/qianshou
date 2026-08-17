package scope

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ai-daming/qianshou/apps/control/internal/classification"
	"github.com/ai-daming/qianshou/apps/control/internal/ghfacts"
)

func issue(number int, labels ...string) ghfacts.Issue {
	return ghfacts.Issue{Number: number, Title: fmt.Sprintf("issue %d", number), State: "OPEN", Labels: labels}
}

func rel(number int, parent *int, blocked ...ghfacts.BlockedIssue) ghfacts.Relationships {
	return ghfacts.Relationships{Number: number, Parent: parent, BlockedBy: blocked}
}

func ptr(n int) *int { return &n }

func TestBuildFlatScopeWithoutControlIssue(t *testing.T) {
	issues := []ghfacts.Issue{
		issue(10, "workflow:delivery", "type:technical", "rigor:standard"),
		issue(11),
	}
	rels := map[int]ghfacts.Relationships{10: rel(10, nil), 11: rel(11, nil)}
	snap, err := Build("s", issues, rels)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.Mode != ModeFlat {
		t.Fatalf("mode = %q, want flat", snap.Mode)
	}
	if snap.ControlIssue != 0 {
		t.Fatalf("flat scope must not name a control issue, got %d", snap.ControlIssue)
	}
	if len(snap.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(snap.Items))
	}
}

func TestBuildInitiativeDetectsUniqueControlIssueByLabelOnly(t *testing.T) {
	// The control label alone decides initiative mode, even when the rest of
	// the classification is incomplete (rigor missing here).
	issues := []ghfacts.Issue{
		issue(1, "workflow:control", "type:milestone-control"),
		issue(2, "workflow:delivery", "type:feature", "rigor:standard"),
	}
	rels := map[int]ghfacts.Relationships{1: rel(1, nil), 2: rel(2, ptr(1))}
	snap, err := Build("m1", issues, rels)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if snap.Mode != ModeInitiative || snap.ControlIssue != 1 {
		t.Fatalf("mode = %q control = %d, want initiative/1", snap.Mode, snap.ControlIssue)
	}
	if snap.Items[1].Parent == nil || *snap.Items[1].Parent != 1 {
		t.Fatalf("parent not carried: %+v", snap.Items[1])
	}
}

func TestBuildFailsClosedWithMultipleControlIssues(t *testing.T) {
	issues := []ghfacts.Issue{
		issue(1, "workflow:control", "type:milestone-control", "rigor:standard"),
		issue(2, "type:milestone-control"),
	}
	rels := map[int]ghfacts.Relationships{1: rel(1, nil), 2: rel(2, nil)}
	_, err := Build("m1", issues, rels)
	if err == nil {
		t.Fatalf("multiple control issues accepted")
	}
	if !strings.Contains(err.Error(), "#1") || !strings.Contains(err.Error(), "#2") {
		t.Fatalf("error must list every control issue: %v", err)
	}
}

func TestBuildFailsClosedOnIncompleteRelationshipFacts(t *testing.T) {
	issues := []ghfacts.Issue{issue(1, "workflow:delivery", "type:technical", "rigor:standard"), issue(2)}
	rels := map[int]ghfacts.Relationships{1: rel(1, nil)} // #2 missing
	_, err := Build("m1", issues, rels)
	if err == nil {
		t.Fatalf("missing relationship facts silently treated as no dependencies")
	}
	if !strings.Contains(err.Error(), "#2") {
		t.Fatalf("error must name the issue lacking facts: %v", err)
	}
}

func TestBuildAttachesClassificationIncludingInvalid(t *testing.T) {
	issues := []ghfacts.Issue{
		issue(1, "workflow:delivery", "type:technical", "rigor:standard"),
		issue(2, "workflow:delivery", "workflow:operation"),
	}
	rels := map[int]ghfacts.Relationships{1: rel(1, nil), 2: rel(2, nil)}
	snap, err := Build("m1", issues, rels)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !snap.Items[0].Classification.Valid {
		t.Fatalf("valid labels classified invalid: %+v", snap.Items[0].Classification)
	}
	got := snap.Items[0].Classification.Classification
	if got.Workflow != classification.WorkflowDelivery || got.Kind != classification.KindTechnical || got.Rigor != classification.RigorStandard {
		t.Fatalf("classification = %+v", got)
	}
	if snap.Items[1].Classification.Valid {
		t.Fatalf("contradictory labels must stay invalid, not be dropped")
	}
	if len(snap.Items[1].Classification.Reasons) == 0 {
		t.Fatalf("invalid classification carries no reasons")
	}
}

func TestUnsatisfiedDependenciesUsesBlockerState(t *testing.T) {
	item := Item{
		Number: 30,
		BlockedBy: []BlockedRef{
			{Number: 29, State: "CLOSED"},
			{Number: 12, State: "OPEN"},
			{Number: 13, State: "open"},
		},
	}
	unsatisfied := item.UnsatisfiedDependencies()
	if len(unsatisfied) != 2 || unsatisfied[0] != 12 || unsatisfied[1] != 13 {
		t.Fatalf("unsatisfied = %v, want [12 13]", unsatisfied)
	}
}

type stubFacts struct {
	issues []ghfacts.Issue
	rels   map[int]ghfacts.Relationships
	errOn  int // issue number whose relationship fetch fails
}

func (s stubFacts) ListMilestoneIssues(context.Context, string, int) ([]ghfacts.Issue, error) {
	return s.issues, nil
}

func (s stubFacts) Relationships(_ context.Context, _ string, number int) (ghfacts.Relationships, error) {
	if s.errOn == number {
		return ghfacts.Relationships{}, fmt.Errorf("injected failure for #%d", number)
	}
	return s.rels[number], nil
}

func TestFromMilestoneFailsClosedWhenAnyRelationshipFetchFails(t *testing.T) {
	stub := stubFacts{
		issues: []ghfacts.Issue{issue(1, "workflow:delivery", "type:technical", "rigor:standard"), issue(2)},
		rels:   map[int]ghfacts.Relationships{1: rel(1, nil), 2: rel(2, nil)},
		errOn:  2,
	}
	_, err := FromMilestone(context.Background(), stub, "o/r", 1, "m1")
	if err == nil {
		t.Fatalf("relationship fetch failure must fail the whole snapshot")
	}
	if !strings.Contains(err.Error(), "#2") || !strings.Contains(err.Error(), "injected failure") {
		t.Fatalf("error loses cause: %v", err)
	}
}

func TestFromMilestoneBuildsSnapshotFromAllFacts(t *testing.T) {
	stub := stubFacts{
		issues: []ghfacts.Issue{
			issue(1, "workflow:control", "type:milestone-control", "rigor:standard"),
			issue(5, "workflow:delivery", "type:feature", "rigor:standard"),
		},
		rels: map[int]ghfacts.Relationships{1: rel(1, nil), 5: rel(5, ptr(1))},
	}
	snap, err := FromMilestone(context.Background(), stub, "o/r", 1, "m1")
	if err != nil {
		t.Fatalf("FromMilestone: %v", err)
	}
	if snap.Mode != ModeInitiative || snap.ControlIssue != 1 || len(snap.Items) != 2 {
		t.Fatalf("snapshot = %+v", snap)
	}
	if snap.ScopeID != "m1" {
		t.Fatalf("scope id = %q", snap.ScopeID)
	}
}

func TestFromMilestoneFailsClosedOnRelationshipNumberMismatch(t *testing.T) {
	stub := stubFacts{
		issues: []ghfacts.Issue{issue(5, "workflow:delivery", "type:technical", "rigor:standard")},
		rels:   map[int]ghfacts.Relationships{5: rel(6, nil)}, // claims facts about #6
	}
	if _, err := FromMilestone(context.Background(), stub, "o/r", 1, "m1"); err == nil {
		t.Fatalf("relationships for another issue accepted")
	}
}

func TestBuildFailsClosedOnDuplicateIssueNumbers(t *testing.T) {
	dup := issue(5, "workflow:delivery", "type:technical", "rigor:standard")
	issues := []ghfacts.Issue{dup, dup}
	rels := map[int]ghfacts.Relationships{5: rel(5, nil)}
	if _, err := Build("m1", issues, rels); err == nil {
		t.Fatalf("duplicate issue numbers accepted as two work items")
	}
}

// --- Round 4: Build must reject invalid facts from any Facts source ---

func TestBuildFailsClosedOnInvalidFactShapes(t *testing.T) {
	valid := issue(5, "workflow:delivery", "type:technical", "rigor:standard")
	cases := []struct {
		name string
		rels map[int]ghfacts.Relationships
	}{
		{
			name: "reviewer repro zero-number closed blocker",
			rels: map[int]ghfacts.Relationships{5: {Number: 5, BlockedBy: []ghfacts.BlockedIssue{{Number: 0, State: "CLOSED"}}}},
		},
		{
			name: "parent zero",
			rels: map[int]ghfacts.Relationships{5: {Number: 5, Parent: ptr(0)}},
		},
		{
			name: "blocker state lowercase from graphql source",
			rels: map[int]ghfacts.Relationships{5: {Number: 5, BlockedBy: []ghfacts.BlockedIssue{{Number: 9, State: "open"}}}},
		},
		{
			name: "duplicate blockers",
			rels: map[int]ghfacts.Relationships{5: {Number: 5, BlockedBy: []ghfacts.BlockedIssue{{Number: 9, State: "OPEN"}, {Number: 9, State: "OPEN"}}}},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Build("m1", []ghfacts.Issue{valid}, tc.rels); err == nil {
				t.Fatalf("invalid relationship facts accepted: %s", tc.name)
			}
		})
	}
}

func TestBuildFailsClosedOnInvalidIssueFacts(t *testing.T) {
	bad := ghfacts.Issue{Number: 5, Title: "", State: "open", Labels: []string{"workflow:delivery", "type:technical", "rigor:standard"}}
	rels := map[int]ghfacts.Relationships{5: rel(5, nil)}
	if _, err := Build("m1", []ghfacts.Issue{bad}, rels); err == nil {
		t.Fatalf("invalid issue fact accepted from a replaceable Facts source")
	}
}
