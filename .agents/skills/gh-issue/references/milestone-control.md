# Milestone and Control Issue governance

Read this reference for Milestone membership, initiative coordination, Parent/Sub-issues, dependencies, or Control Issue completion.

## Milestone modes

Distinguish two materially different modes:

| Mode | Control Issues | Meaning |
|---|---:|---|
| `flat` | 0 | A collection of otherwise independent work packages. |
| `initiative` | exactly 1 | One coherent outcome delivered through a Control Issue and its Sub-issues. |

Do not force a Control Issue onto every Milestone. It adds value only when one outcome spans dependent Issues, cross-Issue invariants, risks, integration, and milestone-level acceptance.

Identify a Control Issue only from repository-supported GitHub metadata, such as a native Issue Type or an explicit label. Never infer it from local configuration, title text, body prose, issue number, a checklist, or link count. More than one recognized Control Issue in one initiative is inconsistent governance and fails closed.

## GitHub-visible initiative contract

An adopted initiative requires:

- one recognized Control Issue in the same Milestone;
- every owned delivery Issue represented as its native GitHub Sub-issue;
- Parent/Sub-issue for ownership and decomposition;
- native Blocked by/Blocking relationships for execution prerequisites;
- milestone-level exit criteria accepted before the Control Issue closes;
- the Control Issue closed last.

GitHub owns these facts. A local ledger may retain history but must not manufacture missing hierarchy, dependencies, membership, or state.

## Relationship semantics

Parent/Sub-issue answers “which initiative owns this work?” Dependency answers “what must finish before this work can proceed?” Treat them as separate relationships even when they involve the same Issues.

Record only direct prerequisites. For `A → B → C`, set `B` as blocked by `A` and `C` as blocked by `B`. Do not additionally block `C` by `A` unless that direct dependency independently exists.

Never simulate native relationships with body checklists, comments, labels, or local configuration. Verify every relationship after mutation. If the native API is unavailable or the read-back is incomplete, report the edge as unverified and stop instead of substituting another representation.

## Control Issue responsibility

A Control Issue owns:

- the initiative's reason and final outcome;
- Goals and Non-goals;
- cross-Issue invariants;
- delivery map, critical path, blockers, and risks;
- milestone-level exit criteria;
- architecture, RFC, ADR, and plan links;
- final cross-Issue acceptance.

It does not own an implementation Worktree, coding Agent, implementation PR, duplicated child checklist, or the contents of linked design artifacts.

Keep stable information in the body. Keep only a small current-status block mutable: phase, critical path, active child, next child, blockers, risks, and integration branch. Derive child progress from native Sub-issues and append detailed stage evidence as comments.

## Lifecycle and closing

The governance lifecycle is:

```text
DRAFT → READY → EXECUTING → VERIFYING → COMPLETED
```

- `DRAFT`: outcome, boundaries, or decomposition remain unresolved.
- `READY`: Goals, Non-goals, children, dependencies, and exit criteria are agreed.
- `EXECUTING`: child delivery is being implemented or reviewed.
- `VERIFYING`: required children are closed but cross-Issue acceptance remains.
- `COMPLETED`: a human accepted milestone-level evidence and the Control Issue closed.

Child closure may support `VERIFYING`; it never proves `COMPLETED` automatically.

Before closing a Control Issue, refresh and verify required children, dependency state, integration evidence, exit criteria, and human acceptance. Close it last. If it was closed early or while required evidence remains missing, report inconsistent external state and require human resolution.
