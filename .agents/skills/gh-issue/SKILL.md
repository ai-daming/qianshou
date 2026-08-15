---
name: gh-issue
description: Safely read and mutate GitHub Issues with the GitHub CLI. Use whenever an Agent is asked to create an Issue; edit its title, body, classification, labels, milestone, assignees, parent, Sub-issues, or dependencies; add a comment; or close or reopen an Issue. Refresh GitHub facts, preview the exact mutation, require explicit authorization, execute only the authorized change, and read GitHub again to verify the result.
---

# GitHub Issue Operator

Read the repository instructions and these Qianshou contracts before planning a mutation:

- `docs/architecture/issue-types-goals-and-definition-of-done.md`
- `docs/architecture/milestone-modes-and-control-issues.md` when Milestones, Control Issues, Parent/Sub-issues, or dependencies are involved
- `docs/architecture/issue-workspace-and-discussion-hub.md` when an adopted development brief or active DeliveryBaseline may exist

Use GitHub as the authority for Issue content, classification, relationships, Milestone membership, comments, and state. Never copy those facts into local configuration.

## Supported actions

```text
CREATE
UPDATE
COMMENT
SET_CLASSIFICATION
SET_MILESTONE
SET_RELATIONSHIP
CLOSE
REOPEN
```

Treat these as variants of one governed Issue mutation workflow, not as separate skills.

## Mutation workflow

1. Resolve the exact GitHub host, `owner/repository`, and Issue number or confirm that `CREATE` has no Issue yet. Never rely on the current directory alone when the target is ambiguous.
2. Refresh the repository and Issue from GitHub. For an existing Issue, collect at least its title, body, state, labels or native type, Milestone, assignees, `updatedAt`, URL, Parent/Sub-issues, and direct dependencies relevant to the request.
3. Classify the requested action and separate confirmed decisions from suggestions, unresolved Discussion, and inferred intent.
4. Validate the proposed content against the repository Issue contract. Do not invent missing Goal, Acceptance Criteria, DoD, classification, dependency, or closing evidence.
5. Produce an `IssueMutationPlan` and show the exact user-visible preview. Use a body diff for edits and render the complete Markdown for creation or comments.
6. Require explicit authorization for the exact external mutation. A Discussion conclusion, Agent recommendation, development brief, or prior permission for another mutation is not authorization.
7. Immediately refresh the target again. If `updatedAt`, body, classification, state, or relevant relationships changed since the preview, stop and regenerate the plan instead of overwriting concurrent work.
8. Execute only the authorized mutation. Use `gh issue` for supported Issue fields and `gh api` for native relationship operations. Pass multiline bodies through stdin or `--body-file -`; do not interpolate untrusted Markdown into a shell command.
9. Read GitHub again and compare the result with the plan. Report partial application explicitly; never claim success from a zero exit code without verifying GitHub state.

## Mutation plan

Use this logical structure; adapting presentation is allowed, but do not omit the concurrency and authorization fields:

```text
IssueMutationPlan
├── action
├── repository
├── issueNumber?
├── expectedUpdatedAt?
├── sourceConversationId?
├── rationale
├── titleChange?
├── bodyDiff?
├── commentBody?
├── classificationChange?
├── milestoneChange?
├── assigneeChange?
├── relationshipChange?
├── stateChange?
├── scopeAffecting
└── requiredAuthorization
```

The preview must identify every write. Creating an Issue and then adding a Parent, dependency, or comment is a multi-write plan; authorization and the result must cover every step.

## Body versus comment

Update the Issue body when the current contract changes, including Goal, scope, Non-goals, Acceptance Criteria, DoD, classification, or stable Control Issue information.

Add a comment for append-only history, such as a decision rationale, progress update, blocker, investigation result, execution evidence, PR or Review evidence, or next action.

A comment does not redefine the Issue contract. If a comment records a decision that changes the contract, propose a separate body update in the same mutation plan. Preserve both the durable current body and the historical explanation.

For a Control Issue, derive child progress from native Sub-issues. Keep only its small current-status block mutable and append detailed stage evidence as comments; do not duplicate every child checklist in the body.

## Development baseline guard

Treat changes to Goal, scope, Non-goals, Acceptance Criteria, constraints, or DoD as scope-affecting.

If an active DeliveryBaseline exists, do not silently update those fields and continue delivery. Mark the plan as scope-affecting and return it to Discussion so the user can decide whether to revise the development brief, adopt a new baseline, and continue, abandon, or supersede the active DeliveryTrack.

Ordinary comments do not invalidate a baseline. A comment containing a new requirement must not be used as hidden implementation input; convert the accepted requirement into the body and baseline through the explicit scope-change flow.

## Classification rules

Use the three top-level workflows:

```text
CONTROL
DELIVERY
OPERATION
```

For `DELIVERY`, require exactly one supported `deliveryKind`: `FEATURE`, `BUG`, `TECHNICAL`, or `DOCUMENTATION`. For `OPERATION`, use a supported `operationKind` when applicable. Require or apply repository policy for one rigor: `LITE`, `STANDARD`, or `HIGH_RISK`.

Do not create `DISCOVERY` or `INCIDENT` as top-level Qianshou workflows. Discussion is available to every Issue; RFC and ADR are artifacts. Incident response is an Operation scenario, with repair and repository documentation represented by linked Delivery Issues when needed.

Normalize only from GitHub-owned native Issue Types or explicitly supported labels. Never infer classification from title text, free-form body prose, Milestone membership, parentage, or local Qianshou configuration. Fail closed on missing, contradictory, or illegal combinations.

## Relationship rules

- Use native Parent/Sub-issue relationships for ownership and decomposition.
- Use native Blocked by/Blocking relationships for execution prerequisites.
- Record only direct dependencies. For `A -> B -> C`, do not also block `C` by `A` unless that direct dependency independently exists.
- Never simulate relationships with body checklists, comments, labels, or local configuration.
- Treat Parent and dependency mutations as separate semantics even when they involve the same Issues.
- Verify the relationship after mutation from GitHub. If the native API is unavailable or incomplete, stop rather than substituting another representation.

## Action-specific guards

### Create

- Confirm repository, template/workflow, sub-kind where applicable, rigor, Goal, Acceptance Criteria, and Issue-specific DoD.
- Preview the complete title and rendered Markdown body.
- Create relationships only after the new Issue exists, then verify every edge separately.
- Report the created Issue URL and number from GitHub.

### Update

- Start from the refreshed body and preserve unrelated edits.
- Show a semantic Markdown diff rather than only the replacement body.
- Do not erase comments, history, or externally added fields to make the Issue match a stale local copy.

### Comment

- Preview the complete rendered Markdown.
- Treat comments as public repository content unless repository visibility is verified otherwise. Remove secrets, credentials, private host details, personal data, and raw private Agent transcripts.
- Prefer a new comment over rewriting historical comments. Edit a previous comment only when the exact comment and correction are explicitly authorized.

### Close or reopen

- Require an explicit state-change authorization and reason.
- For Delivery, verify the PR, Review, merge target, Acceptance Criteria, and resolved DoD evidence; do not equate Agent completion with completion.
- Close a Milestone Control Issue last, only after required child work and Milestone exit criteria are accepted.
- Do not close or reopen a Milestone merely because an Issue state changed.

## Result

Return:

```text
IssueMutationResult
├── action
├── repository
├── issueNumber
├── issueUrl
├── beforeUpdatedAt?
├── afterUpdatedAt
├── commentUrlOrId?
├── verifiedChanges[]
├── unverifiedOrFailedChanges[]
├── baselineImpact
└── nextRequiredAction?
```

Distinguish `PLANNED`, `AUTHORIZED`, `APPLIED`, `VERIFIED`, and `PARTIALLY_APPLIED`. Never report a plan or an API response as a verified mutation.

## Hard boundaries

- Do not write to GitHub without explicit authorization for the exact mutation.
- Do not create, close, reopen, reclassify, or relate Issues merely because an Agent recommends it.
- Do not overwrite concurrent GitHub edits.
- Do not use local configuration or the Qianshou ledger as a competing Issue truth.
- Do not expose tokens, raw environment values, secrets, or sensitive private evidence in commands, previews, comments, or bodies.
- Do not implement code, approve a PR, merge, deploy, or perform production writes under this skill.
