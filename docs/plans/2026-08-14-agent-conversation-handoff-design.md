# Agent conversation and handoff design

## Decision

Every discussion, implementation, Review, repair, or integration conversation chooses Claude Code or Codex when it is created. Its engine and vendor session are immutable. A different engine requires a new conversation.

## Delivery flow

```text
discussion conversation
→ human-edited, adopted development brief and DeliveryBaseline
→ explicit Git branch/worktree creation
→ independent implementation conversation
→ GitHub-verified PR and implementation output
→ independent Review conversation
→ repair conversation or integration gate
```

Raw conversation history is retained for provenance but is not forwarded wholesale. Each downstream conversation receives a structured work package built from the DeliveryBaseline, current GitHub Issue and PR facts, repository/worktree facts, Review evidence when applicable, and the prior role's final handoff output.

## Runtime

- Claude Code runs in print mode with stream-json output and resumes by session ID.
- Codex runs with JSONL output and resumes by session/thread ID.
- Discussion and Review use read-only/plan profiles.
- Implementation and repair use workspace-write/edit profiles.
- The server invokes fixed executables with argument arrays and `shell: false`.
- Agent turns run in the background; the UI polls the local ledger for completion.

## Authority

Starting an Agent or creating a worktree always requires an explicit button click. PR and Review facts come from verified GitHub and Git state, not Agent text. Merge, push, release, deployment, Issue closure, and production writes remain separately authorized actions.
