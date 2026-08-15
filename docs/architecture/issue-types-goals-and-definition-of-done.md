# Issue types, goals, acceptance criteria, and Definition of Done

Status: Working agreement  
Decision date: 2026-08-14

## Purpose

This document defines how Qianshou classifies GitHub Issues, which body structure each Issue template requires, and how an Issue's Goal, Acceptance Criteria, development method, Definition of Done, development brief, PR, and Review evidence relate.

The central decisions are:

1. Issue classification has two dimensions: `workflowKind` controls how Qianshou manages the work, while `templateKind` controls what the Issue body must contain.
2. `Goal`, Acceptance Criteria, TDD, and Definition of Done answer different questions and must not be collapsed into one checklist.
3. Adopting a development brief freezes the current Issue body and resolved Definition of Done into a `DeliveryBaseline` and creates the Issue's active `DeliveryTrack`.
4. Review evaluates the frozen Issue body plus the adopted development brief against the current PR. Qianshou does not expose a separate Candidate object.

This is a domain and workflow contract. It does not claim that the current Qianshou storage, GitHub metadata, UI, or Agent role skills already implement the model.

## Why one Issue type is insufficient

Issue subject labels such as `bug` and `enhancement` do not determine the complete workflow:

- a Feature, Bug, and Technical Change normally produce code through the same Worktree, PR, and independent Review loop, but require different problem statements;
- a Milestone Control Issue coordinates outcomes and acceptance and must not receive an implementation Worktree;
- a Discovery Issue may complete with an evidence-backed decision and newly created Delivery Issues rather than a code PR;
- an Operational Change may complete only after a real environment action, rollback readiness, and runtime verification;
- an Incident distinguishes service mitigation from root-cause closure and follow-up delivery.

Qianshou therefore normalizes two independent classifications.

```text
IssueDefinition
├── workflowKind   how the Issue progresses and which actions are legal
└── templateKind   which semantic fields the Issue body requires
```

## Workflow kinds

| `workflowKind` | Worktree | PR | Completion meaning |
|---|---:|---:|---|
| `CONTROL` | No | No | Child delivery and cross-Issue acceptance satisfy the initiative exit criteria. |
| `DELIVERY` | Yes | Yes | The adopted target is implemented, independently reviewed in a PR, merged to the intended branch, and closed out. |
| `DISCOVERY` | Usually no | Optional | The stated question is answered with evidence, the decision is recorded, and required follow-up work is created. |
| `OPERATIONAL` | Optional | Optional | The authorized operation is performed, verified in the target environment, and supported by rollback and audit evidence. |
| `INCIDENT` | Optional | Optional | Impact is mitigated, causal understanding is recorded to the required depth, and follow-up work is owned. |

Only `DELIVERY` uses the standard Coding -> PR -> independent Review -> Merge delivery loop. Qianshou must not manufacture a DeliveryTrack, Worktree, Coding action, or PR requirement for the other workflow kinds merely because their body mentions code.

## Initial template kinds

The initial template set is deliberately small:

| `templateKind` | `workflowKind` | Intended use |
|---|---|---|
| `MILESTONE_CONTROL` | `CONTROL` | One coherent initiative coordinated through a Control Issue. |
| `FEATURE` | `DELIVERY` | New or materially changed user or business capability. |
| `BUG` | `DELIVERY` | Observed behavior differs from expected behavior. |
| `TECHNICAL_CHANGE` | `DELIVERY` | Refactoring, state-model changes, migrations, architecture work, or technical debt. |
| `DOCUMENTATION` | `DELIVERY` | A reviewable repository documentation change whose output lands through a PR. |
| `INVESTIGATION` | `DISCOVERY` | Evidence gathering, feasibility work, problem diagnosis, or an architectural/business decision. |
| `OPERATION` | `OPERATIONAL` | Deployment, data migration, credential rotation, production cleanup, or another controlled external action. |
| `INCIDENT` | `INCIDENT` | Active or retrospective production incident handling. |

Bug, Feature, Technical Change, and Documentation differ in body structure, but share one delivery algorithm. New templates should be introduced only when their required information or completion semantics materially differ; presentation preferences alone do not justify another type.

## Shared Issue definition

Every normalized Issue definition has stable cross-template fields:

```text
IssueDefinition
├── workflowKind
├── templateKind
├── problem
├── goal
├── nonGoals[]
├── acceptanceCriteria[]
├── constraints[]
├── issueSpecificDoD[]
└── templateSpecificFields
```

`templateSpecificFields` holds genuinely type-specific information such as Bug reproduction steps, an Operation rollback plan, or an Incident timeline. Core fields such as Goal, Acceptance Criteria, constraints, and Definition of Done must not be hidden inside an arbitrary JSON document.

Issue, Parent/Sub-issue, Milestone, dependency, label, native Issue Type, and body facts remain owned by GitHub. Qianshou may cache and normalize them, but `~/.qianshou/config.json` must not classify individual Issues or duplicate their bodies.

## Goal, Acceptance Criteria, TDD, and Definition of Done

These concepts answer different questions:

| Concept | Question |
|---|---|
| Goal | Why does this work exist, and what outcome should change? |
| Acceptance Criteria | What observable behavior or result would make the Issue correct? |
| TDD | How does implementation use failing tests to drive and protect code behavior? |
| Definition of Done | Which product, quality, Review, integration, and operational gates must pass before the Issue may be considered complete? |

TDD is an implementation discipline and a source of evidence. It does not by itself prove usability, privacy, runtime behavior, deployment success, or the business Goal.

Each Acceptance Criterion and DoD item is a structured criterion:

```text
Criterion
├── id
├── description
├── verificationMethod
├── requiredEvidence
└── required
```

Initial verification methods are:

```text
AUTOMATED_TEST
PR_REVIEW
MANUAL_ACCEPTANCE
RUNTIME_VERIFICATION
EXTERNAL_EVIDENCE
```

An Agent must not mark a human or external criterion complete merely because code or tests pass.

## Project and Issue Definition of Done

The effective Definition of Done is composed rather than copied into every Issue:

```text
Resolved DoD = versioned Project default DoD + Issue-specific DoD
```

The Project default covers repository-wide requirements such as test discipline, Review independence, target coverage, required documentation, merge policy, and cleanup. The Issue-specific section adds requirements unique to the work, such as a regression test, a data migration check, a privacy inspection, or production observation.

The resolved list and its source versions are frozen into the DeliveryBaseline. A later Project-policy change must not silently reinterpret an in-flight DeliveryTrack; adopting a revised baseline is an explicit discussion decision.

## DeliveryBaseline and DeliveryTrack boundary

For a `DELIVERY` Issue, clicking **Adopt development brief** is the business boundary between discussion and formal delivery. It performs one atomic domain action:

1. adopt one version of the development brief;
2. snapshot the current GitHub Issue body and metadata needed to detect scope drift;
3. resolve and freeze the Project and Issue DoD;
4. create the new active DeliveryTrack;
5. attach an already prepared Worktree when present, or leave Worktree preparation as the next legal action.

```text
DeliveryBaseline
├── issueRef
├── issueBodySnapshot
├── issueBodyHash
├── issueUpdatedAt
├── adoptedBriefId
├── resolvedDoD[]
├── projectDoDVersion
└── adoptedAt
```

The snapshot is historical evidence of the accepted target, not an independently editable copy of GitHub truth. If the current Issue body hash later differs, Qianshou opens or proposes a scope-change Stop Condition and pauses delivery mutations until Discussion resolves the change.

One Issue may retain several historical DeliveryTracks, but at most one may be active. A Track has only this stored lifecycle:

```text
ACTIVE
COMPLETED
ABANDONED
```

Workbench stages such as waiting for a Worktree, coding, waiting for PR Review, changes requested, ready to merge, and cleanup required are derived from GitHub, Git, Agent Run, Review, and ledger facts. They are not manually editable Track states.

## PR Review model

Coding is not complete merely because an Agent reports success or creates a commit. The first Coding handoff must create or update the Issue's PR and return the PR reference, tests, known gaps, and evidence.

Review evaluates:

```text
frozen Issue body
+ adopted development brief
+ resolved DoD
+ repository instructions
+ current PR diff and checks
= Review verdict and criterion-by-criterion evidence
```

Qianshou does not model a user-visible or independently managed Candidate object. A Review round records the PR head SHA solely as immutable technical evidence of which PR revision was inspected:

```text
ReviewRound
├── pullRequestRef
├── reviewedHeadSha
├── reviewerRunId
├── criterionResults[]
├── verdict
├── findings[]
└── createdAt
```

If the PR head changes after approval, the previous approval no longer unlocks merge. Qianshou derives that the updated PR requires another independent Review. Repair continues on the same PR unless Discussion explicitly abandons the DeliveryTrack.

## Template contracts

### Milestone Control

```markdown
## 定位

## 最终结果

## Goal

## Non-Goals

## 跨 Issue 不变量

## 子 Issue 与依赖
Use native GitHub Sub-issues and dependency relationships.

## 风险

## Milestone DoD / 退出条件

## 架构与决策入口
```

The Control Issue closes last and has no implementation Worktree or PR. Its DoD is cross-Issue outcome acceptance, not a duplicate of every child task.

### Feature Delivery

```markdown
## 背景与问题

## Goal

## 用户场景

## Non-Goals

## Acceptance Criteria
- [ ] AC-1: ...

## 约束与不变量

## Issue-specific DoD
- [ ] DOD-1: ...

## 相关设计与依赖
```

### Bug Delivery

```markdown
## 影响

## 当前错误行为

## 预期行为

## 复现方式

## 已知证据

## Acceptance Criteria
- [ ] AC-1: ...

## 不允许破坏的行为

## Issue-specific DoD
- [ ] Regression test reproduces the failure before the fix and passes after it.
```

### Technical Change

```markdown
## 当前问题

## Goal / 目标状态

## Non-Goals

## 现有不变量

## 兼容性要求

## 数据迁移与回滚

## Acceptance Criteria
- [ ] AC-1: ...

## Issue-specific DoD

## ADR / 架构文档
```

### Documentation Delivery

```markdown
## 文档缺口

## Goal

## 目标读者与使用场景

## Non-Goals

## Acceptance Criteria

## 必须保持一致的事实来源

## Issue-specific DoD
```

### Investigation / Decision

```markdown
## 要回答的问题

## 背景

## 已知事实

## 假设

## 调查范围

## Non-Goals

## 需要收集的证据

## 预期输出

## DoD
- [ ] The question has an evidence-backed answer.
- [ ] The decision or remaining uncertainty is recorded.
- [ ] Required follow-up Delivery Issues are created and linked.
```

### Operational Change

```markdown
## 操作目标

## 目标环境

## 前置条件

## 风险

## 备份与回滚

## 执行步骤

## 验证方式

## 审计证据

## DoD
```

An execution claim is not completion evidence. Runtime observation and external-system facts remain distinct from a code PR or Agent transcript.

### Incident

```markdown
## 影响与严重程度

## 当前状态

## 时间线

## 已知事实与证据

## 临时缓解

## 根因

## 恢复与验证

## 后续行动

## Incident DoD
```

Mitigation and closure are separate. Restoring service may end the active emergency without proving root cause or completing preventive follow-up work.

## Classification and failure behavior

GitHub remains the owner of Issue classification. Qianshou must normalize a GitHub-native Issue Type or an explicitly supported `type:*` label into `workflowKind` and `templateKind`; the exact repository migration mechanism is a separate implementation decision.

Qianshou must not infer type from the title, body prose, Milestone membership, parent relationship, or local configuration. Unknown or contradictory classifications fail closed for delivery mutations while leaving Discussion available.

The current Mamamate repository has `type:milestone-control` plus broad labels such as `bug`, `enhancement`, `documentation`, and `question`, but no repository Issue templates. Migrating existing Issues and installing templates is follow-up work; this document defines the target semantics first.

## Consequences and follow-up

- Qianshou can present different workbenches without forcing every Issue through Coding and PR Review.
- Review becomes traceable to explicit Goal, Acceptance Criteria, DoD, and a particular PR revision.
- Project-wide quality rules remain reusable without hiding Issue-specific completion requirements.
- Scope changes become explicit baseline invalidations instead of silent prompt changes.
- Runtime storage, APIs, and UI that still refer to a Candidate domain object or manually writable delivery phase require migration to the PR Review and derived-stage model.
- GitHub Issue templates, type markers, Project default DoD storage, criterion evidence UI, and classification migration require separate implementation Issues.
