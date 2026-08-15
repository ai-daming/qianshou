# DeliveryBaseline and scope-change governance

Read this reference before changing Goal, scope, Non-goals, Acceptance Criteria, constraints, DoD, an adopted development brief, or an Issue with active delivery or PR Review evidence.

## Discussion and delivery

Discussion is an always-available control surface, not the first state in a linear pipeline. Delivery may pause for business ambiguity, scope change, technical contradiction, environment problems, Review findings, or integration conflicts without erasing the branch, worktree, PR, tests, Review rounds, reviewed head SHA, or resume point.

A Discussion conclusion may propose an Issue, relationship, Milestone, PR, or merge mutation. It does not authorize or prove that external mutation.

## Artifacts

The Qianshou governance profile distinguishes:

1. **Adopted development brief** — human-approved objective, decisions, Acceptance Criteria, Non-goals, constraints, and open questions.
2. **DeliveryBaseline** — frozen Issue body and metadata plus the adopted brief, resolved repository and Issue DoD, source versions, and adoption metadata.
3. **Execution package** — the baseline plus verified repository, integration base, workspace, permitted mutations, evidence requirements, and open Stop Conditions.

The baseline is historical evidence of the accepted target. It is not an editable replacement for the current GitHub Issue.

## Baseline status

Record one status before a potentially scope-affecting mutation:

```text
ACTIVE_VERIFIED
NONE_VERIFIED
NOT_APPLICABLE
UNAVAILABLE
CONFLICTING
```

- `ACTIVE_VERIFIED`: an active baseline was read from an authoritative Qianshou ledger or equivalent repository-declared delivery system.
- `NONE_VERIFIED`: the adopted runtime was queried and proved that no active baseline exists.
- `NOT_APPLICABLE`: the repository does not use a baseline-aware delivery runtime.
- `UNAVAILABLE`: the repository expects baseline governance, but the runtime or ledger could not be queried.
- `CONFLICTING`: baseline evidence disagrees with current Issue, PR, or runtime identity in a way that cannot be reconciled safely.

Do not equate “no local Qianshou directory was found” with `NONE_VERIFIED`. If the repository declares Qianshou or equivalent baseline governance but the runtime cannot be checked, use `UNAVAILABLE` and fail closed on scope-affecting mutations. For an unadopted repository with no baseline-aware runtime, use `NOT_APPLICABLE`; do not invent a Qianshou ledger requirement.

## Scope-change guard

Treat changes to Goal, scope, Non-goals, Acceptance Criteria, constraints, or DoD as scope-affecting.

For `ACTIVE_VERIFIED`, do not silently edit those fields and continue delivery. Return the proposed change to Discussion so the user can decide whether to:

- revise and adopt a new development brief and baseline;
- continue the active DeliveryTrack under the revised baseline;
- abandon or supersede the active track;
- split or supersede the Issue.

Existing implementation output must not be silently reinterpreted against the new target. An ordinary comment does not invalidate a baseline, but a comment containing an accepted new requirement must become an explicit body and baseline change before implementation consumes it.

For `UNAVAILABLE` or `CONFLICTING`, stop scope-affecting writes. For `NONE_VERIFIED` or `NOT_APPLICABLE`, continue only under the repository's ordinary policy and the exact mutation authorization.

## Review invalidation

Independent Review evaluates the frozen Issue body, adopted brief, resolved DoD, repository instructions, current PR diff, and checks. Bind every Review verdict to the inspected PR head SHA.

If the PR head changes after approval, the old approval no longer unlocks integration. Refresh the PR and require another independent Review. Never treat an Agent's completion statement, commit creation, or prior approval against another SHA as current completion evidence.
