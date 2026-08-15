# Issue Workspace, Discussion Hub, and delivery track

Status: Working agreement  
Decision date: 2026-08-14

## Purpose

This document defines the working model for one GitHub Issue inside Qianshou. It separates the Issue's long-lived discussion and governance context from its resumable software-delivery process.

The central decision is:

> Discussion is not the first state in a linear delivery state machine. Discussion is an always-available control surface for the Issue. Delivery has its own lifecycle and may pause for discussion without losing its workspace, PR, Review rounds, resume point, or evidence.

## Why the linear model is insufficient

A simple flow such as `DISCUSS → IMPLEMENT → REVIEW → MERGE` hides important behavior:

- implementation may expose a missing business decision;
- Review may discover that the Issue scope is wrong rather than that the code is wrong;
- an environment conflict may require human judgment without invalidating completed work;
- discussion may split the Issue, create a larger control Issue, or propose a new Milestone;
- returning to discussion must not erase the branch, worktree, tests, PR, Review rounds, reviewed head SHA, or the point from which work may resume.

Qianshou therefore models the Issue as a workspace with independent but connected areas.

```text
Issue Workspace
├── Discussion Hub
│   ├── Conversations
│   ├── Stop Conditions
│   ├── Decisions and resolutions
│   ├── Open questions
│   └── Current effective understanding
├── Delivery Track
│   ├── Adopted development brief and DeliveryBaseline
│   ├── Issue worktree and branch
│   ├── Implementation and repair conversations
│   ├── PR, test evidence, and independent Review rounds
│   └── Integration, merge, and close-out
└── Activity and Evidence
    ├── GitHub facts
    ├── Git facts
    ├── test evidence
    └── local ledger events
```

## Two independent dimensions

### Discussion Hub

The Discussion Hub exists for the entire lifetime of the Issue. It may contain several conversations. Each Conversation still selects exactly one immutable Engine, but the Issue-level discussion survives vendor sessions, process restarts, Engine changes, and newly created Conversations.

Changing Claude Code to Codex does not mutate a Conversation. It creates another Discussion Conversation within the same Issue Workspace. Qianshou rebuilds useful context from explicit Issue artifacts and evidence rather than pretending to transfer an opaque vendor session.

The durable semantic context is not merely the raw transcript. It consists of:

- current effective understanding;
- confirmed decisions;
- rejected or superseded decisions;
- unresolved questions;
- development-brief versions and the currently adopted version;
- scope-change proposals;
- Stop Conditions and their resolutions;
- references to the delivery evidence that caused a discussion.

### Delivery Track

One Issue may retain several historical DeliveryTracks, but at most one Track may be active. A Track stores only its lifecycle:

```text
ACTIVE
COMPLETED
ABANDONED
```

The workbench derives the current delivery stage from GitHub, Git, PR, Agent Run, Review, evidence, and ledger facts:

```text
WAITING_FOR_WORKTREE
→ WORKTREE_READY
→ IMPLEMENTING
→ READY_FOR_PR_REVIEW
→ REVIEWING
→ CHANGES_REQUESTED → IMPLEMENTING
→ APPROVED
→ MERGED_TO_TARGET
→ CLEANUP_REQUIRED
→ CLOSEOUT_COMPLETE (then Track becomes COMPLETED)
```

A Stop Condition pauses legal delivery actions but does not replace the Track lifecycle or erase the derived stage. For example, an active Track may derive as `REVIEWING` while one Stop Condition remains open. Resolving that Stop Condition can resume Review, request repair, invalidate the baseline, or restructure the work.

The legacy writable phase values, including `BLOCKED` and `NEEDS_HUMAN`, may be read during migration, but new workflow behavior must derive workbench stages and represent attention separately from the Track lifecycle.

## Stop Condition

A Stop Condition is a structured escalation into the Discussion Hub. It answers why delivery stopped and preserves the exact recovery point.

Minimum fields:

```text
id
Issue number
category
summary and detail
origin derived Delivery stage
origin Conversation, when known
PR reference and head SHA at the time, when present
status: OPEN or RESOLVED
resolution
outcome / resume action
created and resolved timestamps
```

Initial categories:

- business ambiguity;
- scope change;
- technical contradiction;
- environment problem;
- Review finding requiring discussion;
- delivery or integration conflict;
- other.

Opening a Stop Condition must not mutate the Track lifecycle or the PR. While at least one Stop Condition is open, Qianshou marks delivery as needing discussion and disables delivery mutations that would race ahead of the unresolved decision.

Resolving a Stop Condition requires a recorded decision and one explicit outcome:

- continue from the preserved resume point;
- revise and adopt a new development brief;
- repair the current PR;
- re-run independent Review;
- split the Issue;
- supersede the Issue with larger work;
- abandon the active DeliveryTrack.

Creating or modifying GitHub Issues, relationships, Milestones, PRs, or merges remains an explicit external mutation with its own preview and authorization. A Discussion result may propose such an action; a chat statement does not prove that it occurred.

## Artifacts and invalidation

Discussion and delivery preparation produce three explicit artifacts:

1. **Adopted Development Brief** — the human-approved implementation input: objective, decisions, acceptance criteria, non-goals, constraints, and open questions.
2. **DeliveryBaseline** — the frozen Issue body plus the adopted brief, resolved Project and Issue DoD, source versions, and adoption metadata.
3. **Execution Package** — the DeliveryBaseline plus verified repository, integration base, workspace, permitted mutations, evidence requirements, and Stop Conditions.

If discussion changes requirements materially, Qianshou adopts a new brief version and freezes a new DeliveryBaseline. Existing implementation output is not silently reinterpreted against the new baseline. The existing Track must be explicitly continued, abandoned, or superseded through Discussion.

Review targets the Issue PR. Each Review round records the inspected PR head SHA. A new push changes the PR head and invalidates approval tied to the previously reviewed SHA without creating a separate Candidate domain object.

## UI contract

The Issue is the middle area's primary workspace.

- The left rail selects GitHub Issues and shows derived readiness or attention.
- The middle area is the Issue Workspace. Discussion is always available beside the current Delivery workbench; it is not hidden as a final-stage-only tool.
- The right area is optional. It shows activity, evidence, and selected-record detail. Hiding it must not remove required controls.

The middle area separates four concepts visually:

| Concept | Examples |
|---|---|
| Stage | implementing, reviewing, approved |
| Action | create worktree, start implementation, open Stop Condition |
| Artifact | adopted brief, DeliveryBaseline, PR Review verdict |
| Fact | GitHub dependency, Git branch, CI result |

The delivery navigator is derived from facts and ledger state; clicking a decorative stage must not manufacture truth. The primary action is the next legal action, not merely the most convenient button.

## Recovery and reconciliation

Opening an Issue Workspace always reconciles current GitHub and Git facts with the local ledger. It must handle more than the happy path:

- an existing branch or worktree created outside Qianshou;
- an existing or externally updated PR;
- a PR head that no longer equals the head SHA of the approved Review;
- a Review invalidated by a new push;
- CI failure, merge conflict, or branch-protection changes;
- an Issue closed, moved, split, or superseded outside Qianshou;
- a process restart during an Agent turn.

Qianshou's value is not merely launching an Agent. Its control-plane value is preserving provenance, exposing disagreement, and recovering the correct legal next action after interruption or external change.

## Initial implementation slice

The first slice implements:

- persistent Stop Conditions in the local ledger;
- derived-stage, PR, and reviewed-head snapshots when a Stop Condition opens;
- resolution outcomes without erasing the Track lifecycle or resume point;
- API boundaries for opening and resolving Stop Conditions;
- an Issue Workbench with an always-visible Discussion Hub;
- a delivery pause gate while Stop Conditions remain open;
- activity/evidence display separate from conversation controls.

The first slice does not claim:

- automatic extraction of durable decisions from chat;
- automatic GitHub Issue, relationship, or Milestone creation;
- automatic PR creation, approval, merge, or cleanup;
- complete `~/.qianshou` or SQLite migration.
