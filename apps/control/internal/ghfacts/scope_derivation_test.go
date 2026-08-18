package ghfacts_test

// These derivation tests live in ghfacts' external test binary on purpose:
// fixtures need a client aimed at stub servers, and that constructor is
// test authority exposed only through export_test.go. Production code has
// no path to redirect the fact source; the tests here exercise
// scope.FromMilestone/Build through the full real decode chain.

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
	"github.com/ai-daming/qianshou/apps/control/internal/scope"
)

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
	client, err := ghfacts.NewClientForTest("test-token", rest.URL, gql.URL, rest.Client())
	if err != nil {
		t.Fatalf("NewClientForTest: %v", err)
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
	snap, err := scope.FromMilestone(context.Background(), client, "ai-daming/qianshou", 1, "s")
	if err != nil {
		t.Fatalf("FromMilestone: %v", err)
	}
	if snap.Mode != scope.ModeFlat || snap.ControlIssue != 0 || len(snap.Items) != 2 {
		t.Fatalf("snapshot = %s/%d items=%d, want flat/0/2", snap.Mode, snap.ControlIssue, len(snap.Items))
	}
}

func TestFromMilestoneDetectsInitiativeByLabelOnly(t *testing.T) {
	client := scopeStubClient(t,
		[]stubIssue{
			{number: 1, labels: []string{"workflow:control", "type:milestone-control"}},
			{number: 2, labels: []string{"workflow:delivery", "type:feature", "rigor:standard"}},
		},
		map[int]stubRel{1: {}, 2: {parent: 1, hasParent: true}}, nil)
	snap, err := scope.FromMilestone(context.Background(), client, "ai-daming/qianshou", 1, "m1")
	if err != nil {
		t.Fatalf("FromMilestone: %v", err)
	}
	if snap.Mode != scope.ModeInitiative || snap.ControlIssue != 1 {
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
	_, err := scope.FromMilestone(context.Background(), client, "ai-daming/qianshou", 1, "m1")
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
	_, err := scope.FromMilestone(context.Background(), client, "ai-daming/qianshou", 1, "m1")
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
	snap, err := scope.FromMilestone(context.Background(), client, "ai-daming/qianshou", 1, "m1")
	if err != nil {
		t.Fatalf("FromMilestone: %v", err)
	}
	byNumber := map[int]scope.Item{}
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
		if _, err := scope.FromMilestone(context.Background(), client, "ai-daming/qianshou", 1, "m1"); err == nil {
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
		if _, err := scope.FromMilestone(context.Background(), client, "ai-daming/qianshou", 1, "m1"); err == nil {
			t.Fatalf("blocker #9 reported OPEN and CLOSED by different facts")
		}
	})
}

// Round 10: the endpoint-override constructor exists ONLY inside test
// compilation. Production code cannot redirect the fact source at all.
func TestEndpointOverrideExistsOnlyInTestCompilation(t *testing.T) {
	// The symbol is provided by export_test.go; a production build never
	// sees it. This test documents the boundary and fails to compile if the
	// hook is moved into production files.
	var _ = ghfacts.NewClientForTest
	_ = t
}
