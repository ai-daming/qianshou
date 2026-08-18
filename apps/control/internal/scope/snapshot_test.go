package scope

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ai-daming/qianshou/apps/control/internal/classification"
	"github.com/ai-daming/qianshou/apps/control/internal/ghfacts"
)

// Facts cannot be constructed outside ghfacts any more, so these tests stub
// the SERVER, not the fact layer: every fixture flows through the real
// decode exits and carries the full binding/strictness chain with it.

type stubIssue struct {
	number int
	labels []string
	state  string
}

type stubRel struct {
	parent    int
	hasParent bool
	blockedBy []ghfacts.BlockedIssue
}

// scopeStubClient serves a milestone listing and per-issue relationships and
// returns a real client pointed at the stubs.
func scopeStubClient(t *testing.T, issues []stubIssue, rels map[int]stubRel, failRelAt map[int]bool) *ghfacts.Client {
	t.Helper()
	item := func(s stubIssue) string {
		state := s.state
		if state == "" {
			state = "open"
		}
		labels := make([]string, 0, len(s.labels))
		for _, l := range s.labels {
			labels = append(labels, fmt.Sprintf(`{"name":%q}`, l))
		}
		return fmt.Sprintf(`{"number":%d,"title":"issue %d","state":%q,"labels":[%s],"repository_url":"https://api.github.com/repos/ai-daming/qianshou","milestone":{"number":1}}`,
			s.number, s.number, state, strings.Join(labels, ","))
	}
	relBody := func(n int, r stubRel) string {
		parent := "null"
		if r.hasParent {
			parent = fmt.Sprintf(`{"number":%d}`, r.parent)
		}
		nodes := make([]string, 0, len(r.blockedBy))
		for _, b := range r.blockedBy {
			nodes = append(nodes, fmt.Sprintf(`{"number":%d,"state":%q}`, b.Number, b.State))
		}
		return fmt.Sprintf(`{"data":{"repository":{"nameWithOwner":"ai-daming/qianshou","issue":{"number":%d,"parent":%s,"blockedBy":{"pageInfo":{"hasNextPage":false},"nodes":[%s]}}}}}`,
			n, parent, strings.Join(nodes, ","))
	}
	rest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		items := make([]string, 0, len(issues))
		for _, s := range issues {
			items = append(items, item(s))
		}
		fmt.Fprintf(w, "[%s]", strings.Join(items, ","))
	}))
	t.Cleanup(rest.Close)
	gql := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var req struct {
			Variables struct {
				Number int `json:"number"`
			} `json:"variables"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode graphql request: %v", err)
			return
		}
		if failRelAt[req.Variables.Number] {
			w.WriteHeader(http.StatusInternalServerError)
			fmt.Fprint(w, `{"message":"injected failure"}`)
			return
		}
		rel, ok := rels[req.Variables.Number]
		if !ok {
			rel = stubRel{}
		}
		fmt.Fprint(w, relBody(req.Variables.Number, rel))
	}))
	t.Cleanup(gql.Close)
	client, err := ghfacts.NewWithBase("test-token", rest.URL, gql.URL, rest.Client())
	if err != nil {
		t.Fatalf("NewWithBase: %v", err)
	}
	return client
}

func TestFromMilestoneDerivesFlatScope(t *testing.T) {
	client := scopeStubClient(t,
		[]stubIssue{
			{number: 10, labels: []string{"workflow:delivery", "type:technical", "rigor:standard"}},
			{number: 11},
		},
		map[int]stubRel{10: {}, 11: {}}, nil)
	snap, err := FromMilestone(context.Background(), client, "ai-daming/qianshou", 1, "s")
	if err != nil {
		t.Fatalf("FromMilestone: %v", err)
	}
	if snap.Mode != ModeFlat || snap.ControlIssue != 0 || len(snap.Items) != 2 {
		t.Fatalf("snapshot = %s/%d items=%d, want flat/0/2", snap.Mode, snap.ControlIssue, len(snap.Items))
	}
}

func TestFromMilestoneDetectsInitiativeByLabelOnly(t *testing.T) {
	// The control label alone decides initiative mode, even when the rest of
	// the classification is incomplete (rigor missing here).
	client := scopeStubClient(t,
		[]stubIssue{
			{number: 1, labels: []string{"workflow:control", "type:milestone-control"}},
			{number: 2, labels: []string{"workflow:delivery", "type:feature", "rigor:standard"}},
		},
		map[int]stubRel{1: {}, 2: {parent: 1, hasParent: true}}, nil)
	snap, err := FromMilestone(context.Background(), client, "ai-daming/qianshou", 1, "m1")
	if err != nil {
		t.Fatalf("FromMilestone: %v", err)
	}
	if snap.Mode != ModeInitiative || snap.ControlIssue != 1 {
		t.Fatalf("mode = %s control = %d, want initiative/1", snap.Mode, snap.ControlIssue)
	}
	for _, item := range snap.Items {
		if item.Number == 2 {
			if item.Parent == nil || *item.Parent != 1 {
				t.Fatalf("parent not carried: %+v", item.Parent)
			}
		}
	}
}

func TestFromMilestoneFailsClosedWithMultipleControlIssues(t *testing.T) {
	client := scopeStubClient(t,
		[]stubIssue{
			{number: 1, labels: []string{"workflow:control", "type:milestone-control", "rigor:standard"}},
			{number: 2, labels: []string{"type:milestone-control"}},
		},
		map[int]stubRel{1: {}, 2: {}}, nil)
	_, err := FromMilestone(context.Background(), client, "ai-daming/qianshou", 1, "m1")
	if err == nil {
		t.Fatalf("multiple control issues accepted")
	}
	if !strings.Contains(err.Error(), "#1") || !strings.Contains(err.Error(), "#2") {
		t.Fatalf("error must list every control issue: %v", err)
	}
}

func TestFromMilestoneFailsClosedWhenRelationshipFetchFails(t *testing.T) {
	client := scopeStubClient(t,
		[]stubIssue{
			{number: 1, labels: []string{"workflow:delivery", "type:technical", "rigor:standard"}},
			{number: 2},
		},
		map[int]stubRel{1: {}}, map[int]bool{2: true})
	_, err := FromMilestone(context.Background(), client, "ai-daming/qianshou", 1, "m1")
	if err == nil {
		t.Fatalf("relationship fetch failure must fail the whole snapshot")
	}
	if !strings.Contains(err.Error(), "#2") {
		t.Fatalf("error loses cause: %v", err)
	}
}

func TestFromMilestoneAttachesClassificationIncludingInvalid(t *testing.T) {
	client := scopeStubClient(t,
		[]stubIssue{
			{number: 1, labels: []string{"workflow:delivery", "type:technical", "rigor:standard"}},
			{number: 2, labels: []string{"workflow:delivery", "workflow:operation"}},
		},
		map[int]stubRel{1: {}, 2: {}}, nil)
	snap, err := FromMilestone(context.Background(), client, "ai-daming/qianshou", 1, "m1")
	if err != nil {
		t.Fatalf("FromMilestone: %v", err)
	}
	byNumber := map[int]Item{}
	for _, item := range snap.Items {
		byNumber[item.Number] = item
	}
	got := byNumber[1].Classification
	if !got.Valid || got.Classification.Workflow != classification.WorkflowDelivery ||
		got.Classification.Kind != classification.KindTechnical || got.Classification.Rigor != classification.RigorStandard {
		t.Fatalf("#1 classification = %+v", got)
	}
	if byNumber[2].Classification.Valid || len(byNumber[2].Classification.Reasons) == 0 {
		t.Fatalf("contradictory labels must stay invalid with reasons, not be dropped")
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

func TestFromMilestoneFailsClosedOnCrossFactStateContradictions(t *testing.T) {
	t.Run("member state contradicts blocker state", func(t *testing.T) {
		client := scopeStubClient(t,
			[]stubIssue{
				{number: 1, labels: []string{"workflow:control", "type:milestone-control", "rigor:standard"}},
				{number: 2, labels: []string{"workflow:delivery", "type:technical", "rigor:standard"}},
			},
			map[int]stubRel{
				1: {},
				2: {blockedBy: []ghfacts.BlockedIssue{{Number: 1, State: "CLOSED"}}},
			}, nil)
		if _, err := FromMilestone(context.Background(), client, "ai-daming/qianshou", 1, "m1"); err == nil {
			t.Fatalf("member #1 open vs relationship fact #1 CLOSED merged into one snapshot")
		}
	})
	t.Run("two referrers disagree on blocker state", func(t *testing.T) {
		client := scopeStubClient(t,
			[]stubIssue{
				{number: 1, labels: []string{"workflow:control", "type:milestone-control", "rigor:standard"}},
				{number: 2, labels: []string{"workflow:delivery", "type:technical", "rigor:standard"}},
				{number: 3, labels: []string{"workflow:delivery", "type:technical", "rigor:standard"}},
			},
			map[int]stubRel{
				1: {},
				2: {blockedBy: []ghfacts.BlockedIssue{{Number: 9, State: "OPEN"}}},
				3: {blockedBy: []ghfacts.BlockedIssue{{Number: 9, State: "CLOSED"}}},
			}, nil)
		if _, err := FromMilestone(context.Background(), client, "ai-daming/qianshou", 1, "m1"); err == nil {
			t.Fatalf("blocker #9 reported OPEN and CLOSED by different facts")
		}
	})
}

// Round 9: fact types are opaque — the only constructible value outside
// ghfacts is the zero value, and it must be refused.
func TestZeroValueFactsAreRejected(t *testing.T) {
	if _, err := Build("m1", []ghfacts.Issue{{}}, map[int]ghfacts.Relationships{1: {}}); err == nil {
		t.Fatalf("zero-value facts fabricated a snapshot")
	}
	if err := (ghfacts.Issue{}).Validate(); err == nil {
		t.Fatalf("zero-value Issue validated")
	}
	if err := (ghfacts.Relationships{}).Validate(); err == nil {
		t.Fatalf("zero-value Relationships validated")
	}
}
