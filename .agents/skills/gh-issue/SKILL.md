---
name: gh-issue
description: Safely read and mutate GitHub Issues with the GitHub CLI using a self-contained, opinionated governance profile. Use whenever an Agent is asked to create an Issue; edit its title, body, classification, labels, milestone, assignees, parent, Sub-issues, or dependencies; add a comment; or close or reopen an Issue. Refresh GitHub facts, load the target repository's policy, preview the exact mutation, require explicit authorization, execute only the authorized change, and read GitHub again to verify the result.
---

# GitHub Issue Operator

Operate GitHub Issues through one governed mutation workflow. Keep GitHub as the authority for Issue content, classification, relationships, Milestone membership, comments, and state. Never copy those facts into local configuration.

This Skill is self-contained. Do not require the target repository to contain Qianshou architecture documents.

## Load the governance profile

1. Read the target repository's instructions, Issue templates, supported labels or native Issue Types, and other declared Issue policy.
2. Read [references/issue-governance.md](references/issue-governance.md) before planning any mutation. It defines the bundled Qianshou governance profile and compatibility behavior for repositories that have not adopted it.
3. Read [references/milestone-control.md](references/milestone-control.md) when Milestones, Control Issues, Parent/Sub-issues, dependencies, or initiative completion are involved.
4. Read [references/baseline-and-scope-change.md](references/baseline-and-scope-change.md) when Goal, scope, Non-goals, Acceptance Criteria, constraints, DoD, a development brief, an active DeliveryBaseline, PR Review, or a scope-changing mutation may be involved.

Repository policy wins over the bundled profile when it explicitly defines different compatible semantics. Do not silently bootstrap Qianshou labels, Issue Types, templates, or runtime state in an unadopted repository. Apply the universal safety guards regardless of repository policy.

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
3. Determine the repository-policy status and the baseline status defined by the bundled references. Separate confirmed decisions from suggestions, unresolved Discussion, and inferred intent.
4. Validate the proposed content against the repository Issue contract and the applicable parts of the bundled governance profile. Do not invent missing Goal, Acceptance Criteria, DoD, classification, dependency, closing evidence, or repository adoption.
5. Produce an `IssueMutationPlan` and show the exact user-visible preview. Use a body diff for edits and render the complete Markdown for creation or comments.
6. Require explicit authorization for the exact external mutation. A Discussion conclusion, Agent recommendation, development brief, or prior permission for another mutation is not authorization.
7. Immediately refresh the target again. If `updatedAt`, body, classification, state, or relevant relationships changed since the preview, stop and regenerate the plan instead of overwriting concurrent work.
8. Execute only the authorized mutation. Use `gh issue` for supported Issue fields and `gh api` for native relationship operations. Pass multiline bodies through stdin or `--body-file -`; do not interpolate untrusted Markdown into a shell command.
9. Read GitHub again and compare the result with the plan. Report partial application explicitly; never claim success from a zero exit code without verifying GitHub state.

## Mutation plan

Use this logical structure; adapting presentation is allowed, but do not omit policy, concurrency, baseline, or authorization fields:

```text
IssueMutationPlan
├── action
├── repository
├── issueNumber?
├── expectedUpdatedAt?
├── sourceConversationId?
├── repositoryPolicyStatus
├── baselineStatus
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

The preview must identify every write. Creating an Issue and then adding a Parent, dependency, label, or comment is a multi-write plan; authorization and the result must cover every step.

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
├── repositoryPolicyStatus
├── baselineStatus
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
- Do not treat local configuration, an Agent transcript, or a Qianshou ledger as a competing source of GitHub truth.
- Do not claim that a repository adopted the bundled governance profile without repository-owned evidence.
- Do not expose tokens, raw environment values, secrets, or sensitive private evidence in commands, previews, comments, or bodies.
- Do not implement code, approve a PR, merge, deploy, or perform production writes under this Skill.
