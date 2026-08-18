package scope

// Fact-construction fixtures live in internal/ghfacts (external test
// binary): scope's own tests here only exercise logic that needs no fact
// mint — pure derived-value behavior and the refusal of the only
// cross-package-constructible value (the zero value).

import (
	"testing"

	"github.com/ai-daming/qianshou/apps/control/internal/ghfacts"
)

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
