# Qianshou control-plane contract

Qianshou is a local-first delivery control plane. It gives one human a truthful view of several AI delivery work packages, runs explicitly selected local AI coding tools, and makes every context handoff visible.

The canonical entities, `~/.qianshou` layout, configuration boundary, and Project/Scope/Landing model are defined in [Qianshou data model and home-directory contract](./data-model-and-qianshou-home.md).

Milestone topology and the distinction between flat collections and initiative-style Control Issues are defined in [Milestone modes and Control Issue contract](./milestone-modes-and-control-issues.md).

The Issue-level working model, always-available Discussion Hub, resumable Delivery Track, and Stop Condition contract are defined in [Issue Workspace, Discussion Hub, and delivery track](./issue-workspace-and-discussion-hub.md).

Issue workflow kinds, body templates, Goal/Acceptance Criteria/Definition of Done semantics, DeliveryBaseline, and PR Review are defined in [Issue types, goals, acceptance criteria, and Definition of Done](./issue-types-goals-and-definition-of-done.md).

## Facts and ledger

External facts remain authoritative:

- GitHub owns Issue, native Parent/Sub-issue and Blocked by/Blocking relationships, PR, Milestone, and CI state.
- Git owns worktree paths, branches, ancestry, dirtiness, and commit identity.
- Test commands own pass/fail evidence.

The central Qianshou SQLite ledger owns the global Project and Runner catalog plus workflow assertions and artifacts that have no external owner: DeliveryTrack lifecycle, immutable-engine Conversations, AgentRuns, adopted development briefs, DeliveryBaselines, Runner/Project and Track/worktree bindings, Review rounds, StopConditions, exact Vendor frames, and normalized Run events. Runner-local configuration owns only allowed roots and enabled adapters/executables. Scope is selected at runtime; Landing is delivery intent. Neither SQLite nor config may declare current GitHub titles, membership, relationships, states, current Git state, test results, or process state. The UI must display external and local evidence separately when they disagree.

## Conversation invariant

A conversation or Agent run selects exactly one engine at creation: Claude Code or Codex. The engine is immutable for the lifetime of that conversation. Switching engines always means creating a new conversation. Qianshou may explicitly attach an adopted development brief, DeliveryBaseline, or handoff package to the new conversation, but it never pretends that vendor-specific session state was transferred.

Discussion, implementation, and Review are independent conversations. Discussion may produce a versioned development brief. Implementation consumes the adopted brief and DeliveryBaseline and works through the Issue worktree. Review consumes the frozen Issue body, adopted brief, resolved DoD, repository instructions, current PR, and implementation evidence; it does not inherit the implementer's raw conversation history. Each Review round records the reviewed PR head SHA as technical evidence, not as a separate Candidate domain object.

## Derived delivery stages

```text
DISCUSSION HUB (always-available control surface)
↕
ADOPTED DEVELOPMENT BRIEF + FROZEN DELIVERY BASELINE
→ WAITING_FOR_WORKTREE
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

These are calculated workbench stages, not manually writable DeliveryTrack states. A Track stores only `ACTIVE`, `COMPLETED`, or `ABANDONED`. GitHub, Git, PR, Agent Run, Review, evidence, and ledger facts determine the current stage and legal actions.

A Stop Condition is an explicit side state in the Discussion Hub. It pauses legal delivery actions while retaining the active Track, PR, reviewed head SHA, and evidence. There is no compatibility read for legacy writable phases: `BLOCKED` and `NEEDS_HUMAN` are not ledger states.

## Role boundary

- Controller routes work and identifies missing evidence; it does not implement.
- Implementer changes code and creates or updates the Issue PR.
- Reviewer independently inspects the PR against the DeliveryBaseline and records the reviewed head SHA.
- Repair returns to the implementer context.
- Integration gate verifies that the approved Review still targets the current PR head before a human-approved merge.

## Execution boundary

Qianshou may run `git worktree add -b` from an explicitly adopted Landing target and start a Claude Code or Codex process only after an explicit user click. Worktree discovery and creation use Git directly; no external worktree registry or manager participates. Discussion and Review run read-only/plan profiles; implementation and repair run workspace-write/edit profiles. CLI arguments are passed without a shell and prompts are written through stdin.

One central Server exclusively opens SQLite. Multiple Runners actively connect to it and never expose an execution listener for the Server to dial. A central command selects only a logical Project, binding, Engine, and structured operation; Runner-local roots, adapters, executables, and credentials remain the final permission boundary. M1 uses the same boundary in process. Remote transport, authentication, and TLS arrive together in M2 before any non-loopback operation.

Qianshou does not merge branches, push commits, publish releases, deploy services, or perform production writes without a separate explicit authorization. An Agent completion message never proves that Coding or Review completed: Qianshou must refresh the PR, Git head, checks, tests, and recorded Review evidence before enabling the next action.
