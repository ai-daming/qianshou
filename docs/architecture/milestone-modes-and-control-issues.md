# Milestone modes and Control Issue contract

Status: Accepted  
Decision date: 2026-08-14

## Context

Qianshou manages two materially different kinds of GitHub Milestone:

1. A collection Milestone groups otherwise independent Issues, such as a month of unrelated fixes.
2. An initiative Milestone delivers one coherent outcome through several dependent Issues, a long-lived integration branch, independent Issue worktrees and PRs, independent Review rounds, and milestone-level acceptance.

Treating both as the same flat Issue board loses the initiative's intent, critical path, cross-Issue constraints, risks, and final acceptance. Conversely, forcing every collection Milestone to maintain a control artifact creates ceremony without value.

## Decision

Qianshou supports two Milestone modes:

| Mode | Configured control Issues | Meaning |
|---|---:|---|
| `flat` | 0 | The Milestone is a collection of independent work packages. |
| `initiative` | exactly 1 | The Milestone delivers one coherent outcome through a Control Issue and its Sub-issues. |

Qianshou discovers Milestone membership on every GitHub refresh. A Control Issue is identified only by the native GitHub label `type:milestone-control`, or by an equivalent organization Issue Type if one becomes available. It is never identified from local configuration, an Issue title, body text, number, checklist, or number of links.

```json
{
  "repository": {
    "slug": "ai-daming/mamamate",
    "path": "/Users/<user>/work/mamamate/mamamate"
  },
  "milestone": { "number": 7 }
}
```

Zero GitHub-labeled Control Issues selects `flat`. One selects `initiative`. More than one is inconsistent GitHub governance and must fail closed.

## GitHub-visible contract

An initiative Milestone must satisfy all of the following:

- The Control Issue belongs to the same GitHub Milestone.
- It carries the label `type:milestone-control`, or an equivalent organization Issue Type if one becomes available.
- Every delivery Issue owned by the initiative is a GitHub Sub-issue of the Control Issue.
- Parent/Sub-issue expresses work decomposition and aggregate progress.
- Issue dependencies express execution order and blocking; they are not inferred from the parent relationship.
- The Control Issue is closed last.

GitHub owns Issue, Milestone, Parent/Sub-issue, dependency, PR, and closed-state facts. Qianshou may validate and present these facts but must not silently manufacture or override them.

## Native dependency contract

Initiative delivery order is represented with GitHub's native Issue relationship, not with body checklists, labels, title conventions, or the Parent/Sub-issue hierarchy.

For two Issues `A` and `B`, when `B` cannot begin until `A` has delivered its required result:

- set `B` to **Blocked by `A`**;
- GitHub presents the same relationship on `A` as **Blocking `B`**;
- do not create a second reverse relationship: `Blocked by` and `Blocking` are two views of one edge;
- record only the direct prerequisite. If `A → B → C`, set `B` as blocked by `A` and `C` as blocked by `B`; do not also set `C` as blocked by `A` unless `C` has an independent direct dependency on `A`.

Parent/Sub-issue answers “which initiative owns this work?” Dependency answers “what must finish before this work may proceed?” An Issue may be a Sub-issue of the Control Issue and still have no dependency on its sibling Issues.

The native GitHub relationship is the only dependency fact. Qianshou takes the Issue membership returned by the selected GitHub Milestone, then collects those Issues' native `blockedBy` relationships through GitHub GraphQL and uses the returned Issue states to calculate local action gates. `config/projects.json` must not contain Issue lists, roles, titles, or dependency edges; those fields are invalid configuration because they would create a second, drifting source of truth. If dependency collection is incomplete or fails, Qianshou fails the external refresh instead of treating the Issue as unblocked.

## Control Issue responsibility

The Control Issue is the Milestone's control plane, not a large implementation Issue.

It owns:

- the reason the Milestone exists;
- the final outcome;
- goals and non-goals;
- cross-Issue invariants;
- the delivery map and critical path;
- Milestone-level exit criteria;
- current phase, active child, next child, blockers, and risks;
- links to plans, architecture documents, RFCs, and ADRs;
- final cross-Issue acceptance.

It does not own:

- a development worktree;
- a coding or repair Agent;
- an implementation PR;
- duplicated child-Issue task lists;
- the contents of RFCs or ADRs.

A Control Issue may have planning and milestone-review conversations. Those conversations may refine scope, create or revise child Issues, and prepare milestone-level acceptance, but they do not mutate product code.

## Stable body and dynamic evidence

The Control Issue body contains stable information:

1. positioning and final outcome;
2. goals and non-goals;
3. cross-Issue invariants;
4. delivery map and dependency summary;
5. Milestone exit criteria;
6. architecture and decision links;
7. progress-update and closing rules.

Only a small current-status block is mutable: phase, critical path, active child, next child, blockers, and integration branch.

Child completion is read from native GitHub Sub-issues. Detailed implementation tasks remain in each child Issue. Stage changes are appended as comments with PR links, reviewed PR head SHAs, test evidence, Review verdicts, remaining risks, and the next action. The body must not duplicate every child checklist.

## Control Issue lifecycle

```text
DRAFT
→ READY
→ EXECUTING
→ VERIFYING
→ COMPLETED
```

- `DRAFT`: outcome, boundaries, or decomposition are still being discussed.
- `READY`: goals, non-goals, child Issues, dependencies, and exit criteria are agreed.
- `EXECUTING`: one or more delivery Issues are being implemented or reviewed.
- `VERIFYING`: required child Issues are closed; cross-Issue and integration acceptance remains.
- `COMPLETED`: milestone-level evidence is accepted by a human, the Control Issue is closed, and the Milestone may be closed.

Child closure may move the board into `VERIFYING`; it must never automatically claim `COMPLETED`.

## Board behavior

### Flat Milestone

The left column is a flat Issue navigator. The center displays the selected Issue work package. There is no Milestone Control view.

```text
Issues → selected Issue workbench ← active conversation
```

### Initiative Milestone

The Control Issue is pinned above delivery Issues. Selecting it changes the center into a Milestone Control view containing:

- outcome, goals, and non-goals;
- Sub-issue progress;
- dependency graph and critical path;
- active and next delivery Issues;
- blockers and risks;
- cross-Issue exit criteria;
- architecture, RFC, and ADR links;
- integration-branch evidence;
- the legal next milestone-level action.

Selecting a delivery Issue changes the center back to the Issue workbench: DeliveryBaseline, Git worktree, implementation, PR, tests, Review rounds, and dependencies.

The Control view must not offer `Create worktree`, `Start coding`, `Open PR`, or implementation Review actions.

## Validation and failure modes

Qianshou must surface configuration drift instead of guessing:

- More than one Milestone Issue carrying `type:milestone-control`: invalid initiative.
- A previously observed Control Issue missing from the current GitHub Milestone is no longer current scope; retain its ledger history but do not display it as an active work package.
- Missing `type:milestone-control` label: visible metadata drift.
- Delivery Issue not parented by the Control Issue: hierarchy drift.
- Parent relationship used as a dependency: modeling error.
- All children closed while exit criteria remain open: `VERIFYING`, not completed.
- Control Issue closed before required children or milestone acceptance: inconsistent external state requiring human review.

## Control Issue template

```markdown
# M<NUMBER> 总控：<outcome>

## 定位
本 Issue 是本 Milestone 唯一的 Milestone Control Issue。

## 最终结果
<One measurable product or engineering outcome.>

## 当前状态
- 阶段：DRAFT | READY | EXECUTING | VERIFYING | COMPLETED
- 当前关键路径：
- 当前实施：
- 下一项：
- 当前阻塞：
- 集成分支：

## 目标
- ...

## 非目标
- ...

## 跨 Issue 不变量
- ...

## 交付地图
Use native GitHub Sub-issues. Summarize dependencies separately.

## Milestone 退出条件
- [ ] Cross-Issue outcome, not a copy of child tasks.

## 架构与决策入口
- Plan:
- Architecture:
- RFC:
- ADR:

## 进度更新规则
- Child progress comes from GitHub Sub-issues.
- Stage changes append evidence comments.
- The Control Issue closes last.
```

## Mamamate M7 example

Milestone #7 is an `initiative`:

- Control Issue: `ai-daming/mamamate#151`
- Label: `type:milestone-control`
- Sub-issues: `#224`, `#19`, `#149`, `#152`, `#148`
- Integration branch: `codex/milestone-7-poster-engine-baseline`
- Native dependency graph:

  ```text
  #224 → #19 → #149 → #152
                    └→ #148
  ```

- Direct GitHub relationships:
  - `#19` is **Blocked by `#224`**.
  - `#149` is **Blocked by `#19`**.
  - `#152` is **Blocked by `#149`**.
  - `#148` is **Blocked by `#149`**.
- Current critical path to the shared preview/share layer: `#224 → #19 → #149`

The GitHub hierarchy, native dependency relationships, and rewritten Control Issue were applied on 2026-08-14. Qianshou discovers all six M7 Issues from GitHub Milestone #7, identifies #151 from its `type:milestone-control` label, and collects the dependency graph exclusively from GitHub. The specialized Initiative center view and Control-specific action gates remain implementation work; this document defines their required behavior and must not be read as evidence that the UI is already complete.

## Consequences

- Collection Milestones stay lightweight.
- Initiative Milestones gain one stable semantic and acceptance anchor.
- GitHub provides native hierarchy and progress instead of duplicated checklists.
- RFCs and ADRs remain independently versioned design and decision records.
- Qianshou can render materially different boards without guessing from prose.
- Initiative validation introduces deliberate setup work and must fail visibly when GitHub and local configuration drift.
