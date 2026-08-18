# Qianshou Issue governance profile

Use this bundled profile to govern Issue content and classification without depending on files in the target repository.

## Repository-policy status

Classify the repository before proposing a mutation:

```text
ADOPTED
COMPATIBLE
UNADOPTED
CONFLICTING
UNKNOWN
```

- `ADOPTED`: repository-owned instructions, templates, labels, or native Issue Types explicitly implement this profile.
- `COMPATIBLE`: the repository defines equivalent semantics with different names or metadata.
- `UNADOPTED`: the repository has its own Issue process and does not claim this profile.
- `CONFLICTING`: repository rules contradict a required mutation or contain mutually inconsistent metadata.
- `UNKNOWN`: policy or metadata could not be read completely.

For `ADOPTED`, enforce the profile strictly. For `COMPATIBLE`, map only from explicit repository-owned evidence and show the mapping in the mutation plan. For `UNADOPTED`, use the universal safety workflow and treat this profile as a proposal, not an installed repository contract. For `CONFLICTING` or `UNKNOWN`, fail closed on classification, relationship, scope-affecting, and closing mutations.

Never create labels, Issue Types, templates, milestones, or relationships merely to make a repository appear adopted. Bootstrap is a separate multi-write change that requires its own preview and authorization.

## Authority

GitHub owns Issue bodies, labels or native Issue Types, Parent/Sub-issues, dependencies, Milestones, comments, and state. Repository instructions and templates define policy. Local tools may cache and normalize these facts, but must not redefine them.

## Classification model

Separate workflow, subject kind, and rigor:

```text
IssueDefinition
├── workflowKind   CONTROL | DELIVERY | OPERATION
├── deliveryKind   FEATURE | BUG | TECHNICAL | DOCUMENTATION
├── operationKind  repository-defined refinement of OPERATION
└── rigor          LITE | STANDARD | HIGH_RISK
```

- `CONTROL` coordinates child delivery and cross-Issue acceptance. It has no implementation Worktree or PR of its own.
- `DELIVERY` produces a reviewable repository change through implementation, PR, independent Review, integration, and closeout.
- `OPERATION` performs an authorized external action and completes only with target-environment verification, rollback readiness, and audit evidence. A Worktree or PR is optional.

Require exactly one `deliveryKind` for `DELIVERY` and no `deliveryKind` for other workflows. Use a supported `operationKind` when repository policy defines one. Require exactly one rigor for repositories that adopt the profile.

Rigor is independent of kind. A typo Bug and a destructive-migration Bug share a subject kind but must not share evidence and authorization requirements.

Do not create `DISCOVERY` or `INCIDENT` as top-level workflows under this profile. Discussion is available to every Issue; RFC and ADR remain artifacts. Incident response is an `OPERATION` scenario, while repair and repository documentation become linked `DELIVERY` Issues.

Normalize classification only from repository-owned native Issue Types or explicitly supported labels. Never infer it from title text, free-form body prose, Milestone membership, parentage, or local configuration. Missing, contradictory, or illegal classification fails closed.

## Issue definition

A normalized Issue definition contains:

```text
IssueDefinition
├── workflowKind
├── deliveryKind or operationKind
├── rigor
├── problem
├── goal
├── nonGoals[]
├── acceptanceCriteria[]
├── constraints[]
├── issueSpecificDoD[]
└── templateSpecificFields
```

Keep type-specific information such as Bug reproduction steps or an Operation rollback plan in `templateSpecificFields`. Do not hide Goal, Acceptance Criteria, constraints, or DoD inside an opaque attachment or local record.

## Goal, Acceptance Criteria, TDD, and DoD

These concepts answer different questions:

| Concept | Question |
|---|---|
| Goal | Why does this work exist, and what outcome should change? |
| Acceptance Criteria | What observable behavior or result would make the Issue correct? |
| TDD | How will failing tests drive and protect implementation behavior? |
| Definition of Done | Which product, quality, Review, integration, and operational gates must pass before completion? |

TDD is implementation evidence. It does not prove usability, privacy, runtime behavior, deployment success, or the business Goal.

Each Acceptance Criterion or DoD criterion should identify a description, verification method, required evidence, and whether it is required. Supported verification methods include automated tests, PR Review, manual acceptance, runtime verification, and external evidence. An Agent must not mark a human or external criterion complete merely because code or tests pass.

Compose effective DoD from versioned repository defaults plus Issue-specific DoD when repository policy supports it. Do not silently reinterpret active work after a default-policy change.

## Body versus comment

Update the body when the current contract changes: Goal, scope, Non-goals, Acceptance Criteria, constraints, DoD, classification explanation, or stable Control Issue information.

Add a comment for append-only history: decision rationale, progress, blockers, investigation results, execution evidence, PR or Review evidence, risks, or next action.

A comment does not redefine the contract. If a comment records an accepted requirement change, propose a separate body update and apply the baseline rules. Preserve both the durable current body and the historical explanation.

## Action guards

### Create

- Confirm repository, applicable policy or template, classification, rigor, Goal, Acceptance Criteria, and Issue-specific DoD.
- Preview the complete title and rendered Markdown body.
- Create relationships only after the Issue exists, then verify every edge separately.
- Report the Issue URL and number read back from GitHub.

### Update

- Start from the refreshed body and preserve unrelated edits.
- Show a semantic Markdown diff rather than only a replacement body.
- Do not erase comments, history, or externally added fields to match a stale local copy.

### Comment

- Preview the complete rendered Markdown.
- Treat comments as public unless repository visibility is verified otherwise.
- Remove secrets, credentials, private host details, personal data, and raw private Agent transcripts.
- Prefer a new comment over rewriting history. Edit a prior comment only when the exact comment and correction are authorized.

### Close or reopen

- Require explicit authorization and a reason.
- For `DELIVERY`, verify the PR, target branch, current head, independent Review, Acceptance Criteria, and DoD evidence. Agent completion is not completion evidence.
- Apply the Milestone Control closing rules when an initiative is involved.
- Do not close or reopen a Milestone merely because an Issue state changed.
