---
name: issue-implementer
description: Implement one Qianshou-governed Delivery Issue in its assigned worktree and create or update its PR. Use when the derived stage is WORKTREE_READY or CHANGES_REQUESTED and the Agent receives a DeliveryBaseline, handoff package, integration base, scope, Acceptance Criteria, and Issue-specific DoD.
---

# Issue Implementer

## Preconditions

Require the exact repository, worktree, integration branch and base, frozen Issue body, adopted development brief, Acceptance Criteria, Issue-specific DoD, and mutation boundary. Read repository instructions from the assigned worktree's `AGENTS.md`; never accept a copied Project Policy as a substitute. Stop if any required input is missing or contradictory.

## Workflow

1. Read repository instructions and required architecture/business documents.
2. Inspect current code and write failing tests before production changes.
3. Implement only the adopted scope. Preserve unrelated user work.
4. Run the repository-required focused and regression gates using real boundaries; do not weaken requirements or replace them with mocks.
5. Review the diff for scope drift, secrets, generated artifacts, and accidental files.
6. Commit, push, and create or update the Issue PR only when those mutations are explicitly authorized by the execution package.

## Handoff

Return changed files, tests and exit results, scoped coverage where required, PR reference, current PR head SHA, base branch, known gaps, and exact Reviewer instructions.

Never merge into the integration branch, push without authority, deploy, or declare Review approval.
