# Qianshou data model and storage ownership contract

Status: Adopted working agreement
Decision date: 2026-08-19

## Purpose

Qianshou must not win an argument with GitHub, Git, a test command, or a Runner by showing an old local copy. The central rule is:

> Existing systems own current facts. Central SQLite owns only the global catalog and Qianshou decisions, adopted artifacts, lifecycles, and execution evidence that have no other authoritative home.

The two sides are complementary and must not overlap.

## Storage topology

One central `qianshou serve` process owns one SQLite ledger. Runners on one or many machines never open that database. They execute within their local trust boundary and actively connect to the central Server; the Server does not dial Runner hosts or use SSH as an execution protocol.

```text
central host
└── ~/.qianshou/
    ├── config.json       Runner-local execution trust for an embedded Runner
    ├── qianshou.db       central catalog and ledger
    ├── qianshou.db-wal   SQLite WAL while open
    ├── qianshou.lock     single-Server ownership lock
    └── backups/          verified pre-migration backups

runner host (M2 remote transport)
└── local config
    ├── central Server locator and Runner identity/credential
    ├── allowed filesystem roots
    └── enabled adapters and executable commands
```

M1 uses an embedded Runner behind the same domain boundary. The standalone remote command protocol, authentication, and TLS are M2 work; their absence does not move Project ownership back into config.

The central home directory and backup directory are mode `0700`. The database, WAL/SHM files, ownership lock, backups, config, and other sensitive files are mode `0600`. Raw Vendor frames are user-owned sensitive data and are not redacted, uploaded, logged, or exposed to the browser. `qianshou inspect-frame` can read one exact frame only while the Server is stopped and the exclusive ownership lock can be acquired.

## Fact ownership

| Fact or artifact | Owner |
|---|---|
| Project ID, frozen GitHub repository numeric ID, creation slug | central SQLite `projects` |
| Current repository slug, Issue/PR/Milestone/relationships/labels/checks | GitHub |
| Current branches, worktrees, SHA, ancestry, remotes, dirtiness | Git |
| Current test pass/fail | the actual test command output |
| Current process existence and local execution permission | Runner |
| Runner identity, Project binding intent, WorkItem identity | central SQLite |
| Conversations, Brief versions, Baselines, Track lifecycle, Runs, Reviews, Stops, raw frames, normalized events | central SQLite |
| `derivedStage`, `allowedActions`, `blockedReasons` | pure calculation; never persisted |
| Business completion or production outcome | human acceptance plus the relevant external system |

Ordinary refreshes are not written to the ledger. A local action freezes only the external evidence needed to explain that action later: for example, a Baseline freezes the adopted Issue body and a Review freezes its PR head SHA. A frozen snapshot is historical evidence, never permission to ignore an unavailable or contradictory current source.

## Exact SQLite boundary

The schema contains exactly fourteen tables:

| Table | Sole responsibility |
|---|---|
| `schema_migrations` | ordered migration version, name, checksum, application time |
| `projects` | global Project identity and immutable GitHub repository ID |
| `runners` | immutable central Runner identity, display name, retirement |
| `runner_project_bindings` | one Runner's main-checkout binding intent for one Project |
| `work_items` | stable `project_id + issue_number` ledger identity |
| `conversations` | immutable WorkItem, Role, Engine, and Runner-binding affinity |
| `brief_versions` | immutable development-brief content versions |
| `delivery_baselines` | frozen Issue snapshot, adopted Brief, and resolved DoD |
| `delivery_tracks` | Track lifecycle and one-shot Issue-worktree binding |
| `agent_runs` | one queued, running, or terminal Runner execution |
| `vendor_frames` | exact unredacted Vendor input frames |
| `run_events` | normalized business events and explicit sequence cursor |
| `review_rounds` | Baseline, PR, reviewed head, verdict, criteria, findings |
| `stop_conditions` | explicit pause evidence and one-shot resolution |

There is no Scope, IssueWorkspace, Activity, generic Evidence, Notes, Decision, Candidate, command-log, cache, writable Stage, or generic Status table. Views may compose those concepts from owned facts, but must not create another editable truth.

All business foreign keys use `ON DELETE RESTRICT`. Business rows are never hard-deleted in V1. Archive, retire, terminal, and resolution fields are one-way lifecycle changes; other fields are immutable.

## Project and Runner identities

A Project is one GitHub repository. `project_id` is global, immutable, never reused, and created through the central API. Creation resolves the supplied `owner/repo` locator against GitHub and freezes GitHub's numeric repository ID. The creation slug remains historical evidence; current requests re-resolve the numeric ID to GitHub's current canonical slug.

Config V1 no longer accepts `projects`. There is no import, compatibility parser, fallback read, or machine-local Project catalog.

A Runner uses a Server-issued immutable `runner_id`; hostname and IP are connection diagnostics, not identity. A Runner may change its display name and may be retired, but its ID is never reused. Central intent never overrides Runner-local permission: the Runner must independently verify allowed roots, executable adapters, main-checkout shape, and repository identity before executing.

Each `runner_project_bindings` row freezes Runner, Project, absolute main-checkout path, and repository ID at binding. A Runner–Project pair and Runner–path pair each have at most one active binding. A path change retires the old binding and creates another; it never edits the old row. An active Track prevents retirement of the binding it consumes.

## WorkItem, Track, Baseline, and Conversation

`work_items` uses `(project_id, issue_number)` as its key. GitHub still owns the current Issue. One WorkItem may retain historical Tracks but has at most one active Track across all Runners.

A Track stores nullable `terminal_kind` and `terminal_at`; `ACTIVE`, `COMPLETED`, and `ABANDONED` are derived from those fields. Its worktree binding is initially all null. One atomic bind fills Runner binding, workspace path, branch, base branch, and base SHA together. The same bind is idempotent; any changed retry conflicts. Moving machines requires terminating the old Track and starting a new one.

A Brief version is immutable. Draft, adopted, and superseded are derived from Baseline references. Starting a Track and its sequence-1 Baseline is one transaction. Later Baselines append strict contiguous sequences; the current Baseline is the maximum sequence, not a stored pointer. An adoption key with the same normalized payload is idempotent; changed evidence conflicts.

A Conversation freezes WorkItem, Role, Engine, and Runner Project binding. Its Vendor session starts null and can be set exactly once. Engine, Role, or Runner changes require a new Conversation and an explicit handoff. A Conversation has at most one unfinished AgentRun.

## Runs, frames, and events

Creating an AgentRun first persists `QUEUED` with a command key and hash. The Runner starts the real process idempotently and only then records `started_at`. Terminal kind, time, and detail are written together and once. On M1 Server restart, every unfinished Run becomes `INTERRUPTED`: an unrecoverable `RUNNING` process records that it had started, while a `QUEUED` Run retains `started_at = null` and records that it never started. A queued command is never auto-started after restart, and its terminal row no longer blocks a replacement Run for the Conversation.

Frame and event sequences start at 1 and are strict and contiguous per Run. A duplicate key with identical normalized content is an idempotent retry; a changed duplicate, gap, or out-of-order append fails. One raw frame maps to zero through many events:

```text
VendorFrame 1 ──► RunEvent 1
              ├─► RunEvent 2
              └─► ...
```

Normalization occurs in memory. One transaction writes the exact raw frame and all events, and the adapter acknowledges receipt only after commit. `IGNORED` noise stores the frame with no event. `FAILED` parsing stores the frame plus an `ERROR` event. Synthetic Qianshou events use a null source-frame sequence. Event reads use an explicit sequence cursor and caller-visible limit; there is no hidden 100-row truncation.

## Capability and failure behavior

```text
SQLite assertions and artifacts
+ current GitHub facts
+ current Git facts
+ current test evidence
+ current Runner capability
→ derivedStage
→ at most one primary delivery action
→ structured blockedReasons
```

Identity is checked first, then evidence completeness, ledger invariants, reconciliation and Baseline freshness, derived Stage, Stops, and Runner permission. A missing, null, unavailable, stale, or contradictory required fact disables the affected action. When no reliable Stage can be calculated, `derivedStage` is null; there is no invented `UNKNOWN` Stage. Agent prose, a COMPLETED Run, a button click, or a cached snapshot cannot independently advance delivery.

Discussion remains visible. Starting a Discussion Agent still requires an active binding and current Runner/Engine permission. Open StopConditions remove delivery actions without overwriting a reliable derived Stage.

## Migration and recovery

The Server enables and verifies foreign keys, WAL, FULL synchronous writes, and a finite busy timeout. Embedded migrations are ordered from 1 with SHA-256 checksums. A gap, duplicate, checksum drift, database newer than the binary, corrupt database, failed backup, or failed migration refuses startup.

An existing database is consistently backed up with SQLite `VACUUM INTO` before upgrade, the backup passes `integrity_check`, and all pending migrations run in one transaction. Failure rolls back the whole migration. There is no automatic down migration; rollback means stop the Server, restore the verified backup, and run the compatible older binary. Copying a live `.db` file directly is not a valid backup because WAL state may be outside that file.

## Config V1

The strict machine-local configuration contains only the embedded Runner's execution trust:

```json
{
  "version": 1,
  "runner": {
    "id": "runner-1",
    "allowedRoots": ["/Users/operator/work"]
  },
  "engines": [
    {"id": "codex", "adapter": "codex-cli", "command": "codex"}
  ]
}
```

Unknown fields are errors. In particular, `projects`, `scopes`, `landing`, GitHub facts, and credentials are not accepted. Remote Server locator and Runner credential transport join this local trust contract only with the M2 authenticated remote protocol.

## Scope and Landing

A Scope remains a current user/API selector such as GitHub Milestone number or Issue number. It is not a config or SQLite row. A WorkItem viewed through two Scopes retains one ledger identity.

Landing remains adopted delivery intent. Git owns whether its branch, SHA, ancestry, and worktree currently exist. The Track freezes only the exact binding evidence accepted for that delivery attempt.
