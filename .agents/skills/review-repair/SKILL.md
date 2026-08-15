---
name: review-repair
description: Repair blocking findings on a Qianshou delivery PR in the original implementer worktree and update the same PR. Use when the derived stage is CHANGES_REQUESTED. Do not use Reviewer context to make code changes.
---

# Review Repair

## Workflow

1. Open a repair conversation using the DeliveryBaseline, Review handoff package, original implementer worktree, PR reference, and previously reviewed head SHA.
2. Classify every finding as accepted, rejected with evidence, or requiring human judgment.
3. Add or strengthen a failing test for each accepted behavioral finding before changing production code.
4. Make the smallest coherent repair and rerun the affected and regression gates.
5. Recheck previously passing Acceptance Criteria and DoD items plus unrelated worktree changes.
6. Commit, push, and update the same PR only when those mutations are explicitly authorized by the execution package.

## Handoff

Return a finding-by-finding resolution table, PR reference, new PR head SHA, previously reviewed head SHA, tests, remaining uncertainty, and a request for independent re-review.

Never self-approve, merge, push without authority, or bypass a disputed finding that changes business semantics.
