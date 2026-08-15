// Package classification normalizes GitHub issue labels into the Qianshou
// 3-workflow classification contract defined by
// docs/architecture/issue-types-goals-and-definition-of-done.md.
//
// Normalize only accepts the explicitly supported repository labels
// (workflow:*, type:*, rigor:*). It never infers classification from titles,
// body prose, milestone membership, parent relationships, or local
// configuration. Missing, unknown, or contradictory labels fail closed: the
// result is invalid and carries every determinable reason, so delivery
// mutations stay locked while Discussion remains available. Workflow-neutral
// labels such as duplicate or wontfix are ignored.
package classification

import (
	"fmt"
	"slices"
	"strings"
)

// Workflow is how an issue progresses and which actions are legal.
type Workflow string

const (
	WorkflowControl   Workflow = "CONTROL"
	WorkflowDelivery  Workflow = "DELIVERY"
	WorkflowOperation Workflow = "OPERATION"
)

// Kind refines a workflow: the deliveryKind for DELIVERY, the control kind
// for CONTROL, and the operationKind for OPERATION. It is empty for an
// OPERATION issue that carries no kind label.
type Kind string

const (
	KindMilestoneControl Kind = "MILESTONE_CONTROL"
	KindFeature          Kind = "FEATURE"
	KindBug              Kind = "BUG"
	KindTechnical        Kind = "TECHNICAL"
	KindDocumentation    Kind = "DOCUMENTATION"
	KindOperation        Kind = "OPERATION"
)

// Rigor is the process strictness of an issue; exactly one is required.
type Rigor string

const (
	RigorLite     Rigor = "LITE"
	RigorStandard Rigor = "STANDARD"
	RigorHighRisk Rigor = "HIGH_RISK"
)

// Classification is the normalized three-dimension classification.
type Classification struct {
	Workflow Workflow
	Kind     Kind
	Rigor    Rigor
}

// ReasonCode identifies why a classification failed closed.
type ReasonCode string

const (
	ReasonMissingWorkflow      ReasonCode = "MISSING_WORKFLOW"
	ReasonMultipleWorkflow     ReasonCode = "MULTIPLE_WORKFLOW"
	ReasonUnknownLabel         ReasonCode = "UNKNOWN_LABEL"
	ReasonMissingKind          ReasonCode = "MISSING_KIND"
	ReasonMultipleKind         ReasonCode = "MULTIPLE_KIND"
	ReasonKindWorkflowMismatch ReasonCode = "KIND_WORKFLOW_MISMATCH"
	ReasonMissingRigor         ReasonCode = "MISSING_RIGOR"
	ReasonMultipleRigor        ReasonCode = "MULTIPLE_RIGOR"
)

// Reason is one structured cause of an invalid classification.
type Reason struct {
	Code    ReasonCode
	Message string
}

// Result is the outcome of normalization. When Valid is false,
// Classification is the zero value and Reasons lists every determinable cause.
type Result struct {
	Classification Classification
	Valid          bool
	Reasons        []Reason
}

var (
	workflowLabels = map[string]Workflow{
		"workflow:control":   WorkflowControl,
		"workflow:delivery":  WorkflowDelivery,
		"workflow:operation": WorkflowOperation,
	}
	kindLabels = map[string]Kind{
		"type:milestone-control": KindMilestoneControl,
		"type:feature":           KindFeature,
		"type:bug":               KindBug,
		"type:technical":         KindTechnical,
		"type:documentation":     KindDocumentation,
		"type:operation":         KindOperation,
	}
	rigorLabels = map[string]Rigor{
		"rigor:lite":      RigorLite,
		"rigor:standard":  RigorStandard,
		"rigor:high-risk": RigorHighRisk,
	}
	kindAllowed = map[Workflow]map[Kind]bool{
		WorkflowControl:   {KindMilestoneControl: true},
		WorkflowDelivery:  {KindFeature: true, KindBug: true, KindTechnical: true, KindDocumentation: true},
		WorkflowOperation: {KindOperation: true},
	}
	kindRequired = map[Workflow]bool{
		WorkflowControl:   true,
		WorkflowDelivery:  true,
		WorkflowOperation: false,
	}
)

// Normalize maps issue labels onto the classification contract. It is a pure
// function with no IO so any caller (server, runner, tools) can gate on it.
func Normalize(labels []string) Result {
	var reasons []Reason
	var workflows []Workflow
	var kinds []Kind
	var rigors []Rigor

	seen := make(map[string]bool)
	for _, raw := range labels {
		label := strings.ToLower(strings.TrimSpace(raw))
		if label == "" || seen[label] {
			continue
		}
		seen[label] = true
		var unknown bool
		switch {
		case strings.HasPrefix(label, "workflow:"):
			if value, ok := workflowLabels[label]; ok {
				workflows = append(workflows, value)
			} else {
				unknown = true
			}
		case strings.HasPrefix(label, "type:"):
			if value, ok := kindLabels[label]; ok {
				kinds = append(kinds, value)
			} else {
				unknown = true
			}
		case strings.HasPrefix(label, "rigor:"):
			if value, ok := rigorLabels[label]; ok {
				rigors = append(rigors, value)
			} else {
				unknown = true
			}
		}
		if unknown {
			reasons = append(reasons, Reason{
				Code:    ReasonUnknownLabel,
				Message: fmt.Sprintf("不支持的分类标签：%s（支持清单见标签契约）", label),
			})
		}
	}

	var workflow Workflow
	switch len(workflows) {
	case 1:
		workflow = workflows[0]
	case 0:
		reasons = append(reasons, Reason{
			Code:    ReasonMissingWorkflow,
			Message: "缺少 workflow 标签：需要恰好一个 workflow:control / workflow:delivery / workflow:operation",
		})
	default:
		reasons = append(reasons, Reason{
			Code:    ReasonMultipleWorkflow,
			Message: "存在多个 workflow 标签：工作流互斥，恰好一个才合法",
		})
	}

	var kind Kind
	if len(workflows) == 1 && len(kinds) == 1 && kindAllowed[workflow][kinds[0]] {
		kind = kinds[0]
	}
	// Kind validation runs as three independent checks so no determinable
	// reason is swallowed by another: cardinality, determinable absence, and
	// determinable incompatibility.
	if len(kinds) > 1 {
		reasons = append(reasons, Reason{
			Code:    ReasonMultipleKind,
			Message: "存在多个 type: 标签：kind 互斥，恰好一个才合法",
		})
	}
	if len(kinds) == 0 && everyCandidateRequiresKind(workflows) {
		reasons = append(reasons, Reason{
			Code:    ReasonMissingKind,
			Message: missingKindMessageFor(workflows),
		})
	}
	if mismatched := incompatibleKinds(workflows, kinds); len(mismatched) > 0 {
		reasons = append(reasons, Reason{
			Code:    ReasonKindWorkflowMismatch,
			Message: mismatchMessage(workflows, mismatched),
		})
	}

	var rigor Rigor
	switch len(rigors) {
	case 1:
		rigor = rigors[0]
	case 0:
		reasons = append(reasons, Reason{
			Code:    ReasonMissingRigor,
			Message: "缺少 rigor 标签：需要恰好一个 rigor:lite / rigor:standard / rigor:high-risk",
		})
	default:
		reasons = append(reasons, Reason{
			Code:    ReasonMultipleRigor,
			Message: "存在多个 rigor 标签：严格度互斥，恰好一个才合法",
		})
	}

	if len(reasons) > 0 {
		return Result{Valid: false, Reasons: reasons}
	}
	return Result{Valid: true, Classification: Classification{Workflow: workflow, Kind: kind, Rigor: rigor}}
}

// incompatibleKinds returns the kinds that no candidate workflow allows. With
// no workflow evidence at all, every workflow remains a candidate, so any
// supported kind fits one of them and nothing is determinable.
func incompatibleKinds(workflows []Workflow, kinds []Kind) []Kind {
	if len(workflows) == 0 {
		return nil
	}
	var mismatched []Kind
	for _, kind := range kinds {
		allowed := false
		for _, workflow := range workflows {
			if kindAllowed[workflow][kind] {
				allowed = true
				break
			}
		}
		if !allowed {
			mismatched = append(mismatched, kind)
		}
	}
	return mismatched
}

func mismatchMessage(workflows []Workflow, mismatched []Kind) string {
	names := make([]string, 0, len(mismatched))
	for _, kind := range mismatched {
		names = append(names, "type:"+kindLabelName(kind))
	}
	if len(workflows) == 1 {
		return fmt.Sprintf("%s 与工作流 %s 不匹配", strings.Join(names, "、"), workflows[0])
	}
	candidates := make([]string, 0, len(workflows))
	for _, workflow := range workflows {
		candidates = append(candidates, string(workflow))
	}
	slices.Sort(candidates)
	return fmt.Sprintf("%s 与所有候选工作流（%s）都不匹配", strings.Join(names, "、"), strings.Join(candidates, "、"))
}

// missingKindMessageFor renders the absence of a required kind for one
// unambiguous workflow or for a candidate set in which every workflow
// requires one.
func missingKindMessageFor(workflows []Workflow) string {
	if len(workflows) == 1 {
		return missingKindMessage(workflows[0])
	}
	return "所有候选工作流都要求恰好一个 kind 标签，但未找到任何 type: 标签"
}

// everyCandidateRequiresKind reports whether every workflow candidate demands
// exactly one kind label. With no workflow evidence at all, OPERATION stays a
// candidate that legitimately carries no kind, so the answer is false.
func everyCandidateRequiresKind(workflows []Workflow) bool {
	if len(workflows) == 0 {
		return false
	}
	for _, workflow := range workflows {
		if !kindRequired[workflow] {
			return false
		}
	}
	return true
}

func missingKindMessage(workflow Workflow) string {
	switch workflow {
	case WorkflowDelivery:
		return "DELIVERY 需要恰好一个 deliveryKind：type:feature / type:bug / type:technical / type:documentation"
	case WorkflowControl:
		return "CONTROL 需要 type:milestone-control"
	default:
		return "OPERATION 的 type:operation 标签可省略"
	}
}

func kindLabelName(kind Kind) string {
	for name, value := range kindLabels {
		if value == kind {
			return strings.TrimPrefix(name, "type:")
		}
	}
	return string(kind)
}
