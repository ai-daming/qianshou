# Qianshou data model and home-directory contract

Status: Working agreement  
Decision date: 2026-08-14

## Purpose

This document defines Qianshou's core data entities, their ownership boundaries, and the intended user-level storage layout. It exists to keep the program model honest before workflow algorithms and UI behavior are expanded.

The central rule is:

> Configuration locates projects and delivery scopes. GitHub, Git, test commands, and the Qianshou ledger remain the owners of their respective facts.

This is a target architecture and data contract. It must not be read as evidence that the current V0 storage and runtime have already been migrated.

## Qianshou Home

Qianshou uses one user-level home directory per machine:

```text
~/.qianshou/
├── config.json
├── qianshou.db
├── cache/
├── runs/
└── logs/
```

The default may be overridden with `QIANSHOU_HOME` for tests, isolated profiles, or controlled deployments. The home directory is owned by Qianshou, not by any managed repository.

| Path | Responsibility | Authoritative? |
|---|---|---|
| `config.json` | Engines, Projects, Scopes, Landing strategies, and local bindings | Yes, for local configuration only |
| `qianshou.db` | Conversations, development briefs, DeliveryBaselines, DeliveryTrack lifecycles, Review rounds, workspace bindings, and events | Yes, for Qianshou's local ledger only |
| `cache/` | Rebuildable GitHub and Git snapshots | No |
| `runs/` | Raw and normalized Codex / Claude Code execution records | Evidence artifact, not business truth |
| `logs/` | Qianshou process diagnostics | No |

Recommended permissions:

```text
~/.qianshou/                 0700
ledger, run, and log files   0600
```

GitHub, Codex, and Claude Code credentials remain owned by their respective CLIs. Qianshou configuration must not copy tokens or login material.

## Placement decision

There is one central Qianshou configuration on each machine. Managed repositories such as Mamamate or AFU do not each receive a Qianshou target configuration.

Reasons:

- Qianshou must know which Projects exist before opening one of them.
- Absolute paths and local worktree bindings differ by machine and do not belong in a shared product repository.
- A repository may contain several delivery Scopes, such as M7, M8, and a standalone hotfix Issue.
- Requiring every repository to opt into Qianshou would couple product source code to one operator tool.

Repository-owned instructions such as `AGENTS.md`, architecture documents, and repo-local `.agents/skills/` remain in the managed repository because they govern work on that codebase. They are not Qianshou registry data.

## Core entities

```text
Qianshou Home
├── Engines
└── Projects
    └── Scopes
        └── Work Items
            ├── Workspace bindings
            └── Conversations
                ├── Role
                └── Engine
```

### Engine

An Engine is an executable AI coding tool. The initial built-in engines are:

```text
codex
claude-code
```

Engine does not imply workflow role. Both engines may run discussion, implementation, Review, or repair conversations.

```json
{
  "id": "codex",
  "adapter": "codex-cli",
  "command": "codex"
}
```

Whether built-in engine commands remain configurable is an implementation decision; the identity boundary is stable regardless.

### Role

Role describes what one Conversation is allowed and expected to do:

```text
discussion
implementation
review
repair
integration
```

Role belongs to a Conversation, not to a Project or Engine. A Conversation selects exactly one Engine at creation, and that Engine is immutable for the Conversation's lifetime.

```json
{
  "role": "review",
  "engineId": "codex"
}
```

Project configuration must not contain `developerEngine` or `reviewerEngine`. Remembering a user's last choice is UI preference state, not Project definition.

### Project

A Project is one code repository and its machine-local checkout binding.

Examples:

```text
mamamate
afu
qianshou
```

```json
{
  "id": "mamamate",
  "repository": {
    "provider": "github",
    "slug": "ai-daming/mamamate"
  },
  "local": {
    "path": "/Users/<user>/work/mamamate/mamamate"
  }
}
```

The repository slug and local path are locators. Qianshou must verify that the configured local Git repository corresponds to the configured GitHub repository instead of silently combining unrelated sources.

### Scope

A Scope is the delivery boundary currently managed by Qianshou. Milestone is one Scope form, not a prerequisite for using Qianshou.

Supported Scope forms:

| Type | Remote selector | Work Items |
|---|---|---|
| `milestone` | GitHub Milestone number | Issues currently in that Milestone |
| `issue` | GitHub Issue number | Exactly that Issue |
| `issue-tree` | Root GitHub Issue number | Root Issue plus its native Sub-issues |

Examples:

```json
{ "type": "milestone", "number": 7 }
```

```json
{ "type": "issue", "number": 231 }
```

```json
{ "type": "issue-tree", "rootIssueNumber": 151 }
```

Scope stores only the remote selector. It does not copy Milestone titles, Issue lists, Issue titles, labels, Parent/Sub-issue relationships, dependencies, or open/closed state.

### Work Item

A Work Item is one GitHub Issue observed through a Scope.

Work Items are derived from GitHub on refresh. They are not declared in `config.json`.

- A `milestone` Scope derives its Work Items from current Milestone membership.
- An `issue` Scope derives one Work Item.
- An `issue-tree` Scope derives the root and its current native Sub-issues.

If a Work Item leaves a Scope, Qianshou retains its local ledger history for audit but does not continue displaying it as current Scope work.

### Landing strategy

Scope answers “what work is managed?” Landing answers “where accepted code is intended to land?” These are independent dimensions.

An initiative may use a long-lived integration branch:

```json
{
  "type": "integration-branch",
  "baseBranch": "main",
  "branch": "codex/milestone-7-poster-engine-baseline",
  "worktree": "/Users/<user>/work/mamamate/mamamate.worktrees/mamamate/milestone-7-poster-engine-baseline"
}
```

A standalone Issue may target the base branch directly:

```json
{
  "type": "base-branch",
  "baseBranch": "main"
}
```

Landing describes intended topology. Git still owns whether branches, worktrees, SHAs, ancestry, and dirtiness actually exist.

### Workspace

Workspace is a local Git worktree used by one Work Item or one integration branch.

- The Project's main checkout is a local Project binding.
- A long-lived integration worktree belongs to an `integration-branch` Landing strategy.
- Issue worktrees are discovered or created through Git and recorded in the ledger; they are not listed in `config.json`.

### Conversation

A Conversation is one continuous interaction with one immutable Engine and one Role. Discussion, implementation, Review, and repair are separate Conversations even when they concern the same Work Item.

Conversation data belongs in the ledger and includes at least:

```text
conversation id
project id
scope id
work-item Issue number
role
engine id
vendor session id
status
messages or run references
created/updated timestamps
```

Outputs may become explicit inputs to later Conversations through adopted development briefs, DeliveryBaselines, and handoff packages. Qianshou never pretends that opaque vendor session context transfers between Engines.

## Configuration shape

The target `~/.qianshou/config.json` shape is:

```json
{
  "version": 1,
  "engines": [
    {
      "id": "codex",
      "adapter": "codex-cli",
      "command": "codex"
    },
    {
      "id": "claude-code",
      "adapter": "claude-code-cli",
      "command": "claude"
    }
  ],
  "projects": [
    {
      "id": "mamamate",
      "repository": {
        "provider": "github",
        "slug": "ai-daming/mamamate"
      },
      "local": {
        "path": "/Users/<user>/work/mamamate/mamamate"
      },
      "scopes": [
        {
          "id": "m7",
          "source": {
            "type": "milestone",
            "number": 7
          },
          "landing": {
            "type": "integration-branch",
            "baseBranch": "main",
            "branch": "codex/milestone-7-poster-engine-baseline",
            "worktree": "/Users/<user>/work/mamamate/mamamate.worktrees/mamamate/milestone-7-poster-engine-baseline"
          }
        },
        {
          "id": "issue-231",
          "source": {
            "type": "issue",
            "number": 231
          },
          "landing": {
            "type": "base-branch",
            "baseBranch": "main"
          }
        }
      ]
    }
  ]
}
```

## Fact ownership

| Data | Owner |
|---|---|
| Repository and Scope selectors, local paths, Landing intent, enabled Engines | `~/.qianshou/config.json` |
| Milestone title/state/membership | GitHub |
| Issue title/body/state/labels and Parent/Sub-issue | GitHub |
| Blocked by/Blocking and PR/CI state | GitHub |
| Branches, worktrees, SHA, ancestry, remote refs, dirtiness | Git |
| Test pass/fail | Actual test command output |
| Conversation, Role/Engine selection, adopted brief, DeliveryBaseline, Track lifecycle, Review rounds, notes, events | Qianshou ledger |
| Business completion or production outcome | Human acceptance plus relevant external systems |

The same fact must not be persisted as an independently editable value in two owners.

## Multiple machines

Each machine has its own `~/.qianshou` because local paths, available CLIs, worktrees, and run records differ.

```text
macOS:   /Users/<user>/.qianshou/
Ubuntu:  /home/<user>/.qianshou/
```

The GitHub repository and Scope selectors may be logically identical across machines, but the whole directory must not be synchronized blindly. A future portable-project catalog and machine-specific binding split may be introduced only when cross-machine operation is required.

## Invariants

- Engine and Role are separate entities.
- Project means repository; M7 and M8 are Scopes under the Mamamate Project, not separate Projects.
- Milestone is optional; a standalone Issue is a valid Scope.
- Scope and Landing strategy are independent.
- Work Items come from GitHub, never from a local Issue list.
- Issue worktrees come from Git and ledger bindings, never from static configuration.
- Configuration never copies GitHub titles, membership, labels, dependencies, or states.
- Local ledger assertions never override GitHub, Git, test, or business facts.

## Current implementation gap

As of 2026-08-14, V0 still:

- reads `config/projects.json` from the Qianshou source repository;
- stores ledger state in the source repository's ignored `.qianshou/state.json`;
- models each configured entry as one Milestone target rather than `Project → Scope`;
- supports only Milestone discovery;
- stores Developer/Reviewer default-engine bindings instead of a clean Engine registry and per-Conversation selection model;
- assumes an integration-branch Landing strategy.

Migrating to this contract is separate implementation work. The migration must preserve current ledger history, validate repository/local-path identity, and keep the existing manual-control safety boundaries.

## Open decisions

- Exact `issue-tree` traversal and whether the root itself is an implementation Work Item.
- Whether engine commands are built-in adapters, user-configurable commands, or both.
- Final ledger schema and migration from JSON to `qianshou.db`.
- Whether cross-machine operation warrants a portable catalog plus machine-local bindings.
- Refresh-policy placement and whether it is global, Project-level, or Scope-level.
