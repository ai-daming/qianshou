---
name: integration-gate
description: Verify whether an approved Qianshou delivery PR may be merged into its intended target branch. Use when Review is approved and a human is considering integration. Check the current PR head against the reviewed head SHA, branch ancestry, worktree cleanliness, tests, CI, and approval evidence before any merge.
---

# Integration Gate

## Preconditions

Require the DeliveryBaseline, PR reference, approved Review verdict and reviewed head SHA, intended target branch, base ancestry, test evidence, and explicit human merge authority.

## Workflow

1. Refresh remote refs and verify current repository/worktree identity.
2. Confirm the current PR head SHA equals the head SHA recorded by the approved Review.
3. Verify the PR head descends from the intended integration baseline or report the required reconciliation.
4. Confirm the Issue and integration worktrees are clean and no unrelated paths are included.
5. Re-run required merge-sensitive gates and inspect CI/PR state separately.
6. Present the exact merge plan and rollback point to the human.
7. Merge only after explicit authorization, then record the resulting integration SHA and verification evidence.

Never equate merge with release, deployment, runtime health, or business success. Never push, release, deploy, or close an Issue unless separately authorized.
