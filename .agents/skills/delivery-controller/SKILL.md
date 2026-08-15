---
name: delivery-controller
description: Coordinate a Qianshou delivery project by reconciling GitHub, Git worktrees, immutable-engine conversations, DeliveryBaselines, PR Review, and local handoff state. Use when selecting the next Issue, preparing a role handoff, or deciding whether human judgment is required. Never use it to implement business code.
---

# Delivery Controller

Read `docs/architecture/control-plane.md` before coordinating a project.

## Workflow

1. Resolve the configured project, repository, integration branch, Issue, and worktree.
2. Refresh GitHub and Git facts. Treat local Qianshou state as a separate workflow assertion.
3. Refuse to infer completion from an Agent message, worktree name, or Issue label.
4. Derive the current workbench stage and select exactly one legal next action from available evidence.
5. Produce a role handoff package containing the DeliveryBaseline, current Issue and PR facts, base branch, worktree, allowed mutations, acceptance and DoD evidence, provenance, and Stop Conditions.
6. Escalate business ambiguity, production writes, release authority, destructive actions, or conflicting facts to the human.

## Output

Return the derived stage, verified facts, missing evidence, next action, assigned role, and the condition that unlocks the following role.

Do not edit code, approve Review, merge, push, deploy, or mark business verification complete.
