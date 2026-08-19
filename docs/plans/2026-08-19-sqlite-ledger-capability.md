# SQLite Ledger and Capability Implementation Plan

> Execution baseline: Issue #5 at `updatedAt=2026-08-19T04:44:05Z`, body SHA-256 `a170c756d2f08ae043302bb52bc19e543ba961d29db8b189323e37b31fe9f5af`, base `ea3cc9018b4839cac054e9fc1f469486c505ed36`.
>
> Worktree-local implementation and verification were authorized with the adopted baseline. Commit, push, and PR creation were separately authorized on 2026-08-19; merge, deployment, and external Agent launch remain prohibited.

## Goal

Make the Go control server the sole owner of a strict 14-table SQLite ledger, move Project ownership out of Config V1, preserve GitHub/Git/test/Runner as owners of current facts, and derive delivery capability fail closed without persisting Stage or allowed actions.

The frozen TypeScript prototype remains an executable comparison artifact. It must not become a second SQLite writer or a compatibility data source.

## Task 1: Establish the real SQLite startup contract

**Files:**

- Create: `apps/control/internal/ledger/migrations/001_initial.sql`
- Create: `apps/control/internal/ledger/migrations.go`
- Create: `apps/control/internal/ledger/open.go`
- Create: `apps/control/internal/ledger/open_test.go`
- Create: `apps/control/internal/ledger/migrations_test.go`
- Modify: `apps/control/go.mod`
- Modify: `apps/control/go.sum`

1. Write failing tests using temporary file databases and `modernc.org/sqlite` for exact table inventory, foreign keys, partial indexes, required pragmas, migration checksums, gaps, duplicate versions, newer databases, and failed-migration rollback.
2. Add the ordered embedded migration set and checksum validation.
3. Implement secure home/database creation, connection setup, pragma verification, migration locking, consistent pre-upgrade backup, backup verification, and post-migration integrity checks.
4. Add reopen, corrupt-database, and backup/restore tests. Never use `:memory:` for an invariant test.

## Task 2: Encode the 14-table domain and immutable lifecycle rules

**Files:**

- Create: `apps/control/internal/ledger/model.go`
- Create: `apps/control/internal/ledger/catalog.go`
- Create: `apps/control/internal/ledger/delivery.go`
- Create: `apps/control/internal/ledger/runs.go`
- Create: `apps/control/internal/ledger/reviews.go`
- Create: `apps/control/internal/ledger/stops.go`
- Create: `apps/control/internal/ledger/*_test.go`

1. Write RED tests for Project repository identity, Runner retirement, active binding uniqueness, WorkItem identity, one active Track, one-shot Track worktree binding, Conversation affinity, and continuous Baseline sequence.
2. Implement transaction-scoped service methods whose idempotent retry compares the entire normalized object; same key with different content must conflict.
3. Add database constraints/triggers for invariants that must survive concurrent callers and direct SQL.
4. Test Brief, Baseline, Run, Review, and StopCondition immutability and one-way lifecycle transitions.
5. Add concurrency tests for active Track creation, Baseline append, command keys, event sequences, and StopCondition resolution.

## Task 3: Store raw frames and normalized events atomically

**Files:**

- Create: `apps/control/internal/ledger/events.go`
- Create: `apps/control/internal/ledger/events_test.go`
- Create: `apps/control/internal/ledger/events_benchmark_test.go`
- Modify: `apps/control/cmd/qianshou/main.go`

1. Write RED tests for one raw frame mapping to zero, one, or many normalized events, exact unredacted payload retention, strict contiguous sequences, conflict detection, and transaction rollback.
2. Implement append-after-normalize transaction semantics and explicit cursor pagination with no hidden 100-row cap.
3. Add an explicit local CLI for raw-frame inspection; do not expose raw frames through ordinary HTTP or logs.
4. Add a 100,000-frame/event benchmark fixture and `EXPLAIN QUERY PLAN` assertion proving the run/sequence indexes are used.

## Task 4: Derive Capability from owned facts

**Files:**

- Create: `apps/control/internal/capability/capability.go`
- Create: `apps/control/internal/capability/capability_test.go`

1. Define typed current-fact inputs from GitHub, Git, tests, and Runner plus a read-only Ledger projection.
2. Write adversarial RED tests for missing/null/stale/conflicting identity, unavailable sources, Baseline drift, PR-head drift, open stops, Runner denial, and Agent completion claims without external evidence.
3. Implement a pure deterministic derivation returning nullable Stage, at most one primary delivery action, and stable structured blocked reasons.
4. Prove no derived Stage, action, or blocked reason is persisted.

## Task 5: Move Project Catalog to SQLite and remove Config V1 fallback

**Files:**

- Modify: `apps/control/internal/config/config.go`
- Modify: `apps/control/internal/config/config_test.go`
- Modify: `apps/control/internal/githubapi/client.go`
- Modify: `apps/control/internal/githubapi/client_test.go`
- Modify: `apps/control/internal/server/server.go`
- Modify: `apps/control/internal/server/server_test.go`
- Modify: `apps/control/internal/server/integration_test.go`
- Modify: `protocol/openapi.yaml`
- Regenerate: `packages/api-client/src/generated/*`

1. Write RED tests that Config containing `projects` is rejected, while engine/Runner-local trust settings remain local.
2. Add strict GitHub repository resolution returning numeric repository ID and canonical current slug.
3. Add central Project create/list/get behavior backed only by SQLite. Repository numeric ID and Project ID are immutable and non-reusable.
4. Change all current Project read routes to resolve Project from the ledger. There is no import, fallback, or dual read.
5. Update OpenAPI and regenerate the TypeScript client. Raw frames remain absent from ordinary API schemas.

## Task 6: Wire startup, recovery, and the in-process Runner boundary

**Files:**

- Modify: `apps/control/cmd/qianshou/main.go`
- Modify: `apps/control/internal/server/server.go`
- Modify: `apps/control/internal/runner/runner.go`
- Create/modify: relevant integration tests

1. Open/migrate/verify SQLite before the HTTP listener can serve writes.
2. On restart, leave QUEUED Runs queued and atomically mark unrecoverable M1 RUNNING Runs INTERRUPTED.
3. Keep the in-process Runner behind the same structured command boundary used by ledger Run creation; do not add the M2 remote protocol, auth, or TLS.
4. Verify server loopback enforcement and that Runner never opens SQLite.

## Task 7: Reconcile architecture documents and run all gates

**Files:**

- Modify: `docs/architecture/control-plane.md`
- Modify: `docs/architecture/data-model-and-qianshou-home.md`
- Modify: `docs/architecture/issue-workspace-and-discussion-hub.md`
- Modify: `docs/architecture/issue-types-goals-and-definition-of-done.md` only if an actual contradiction is present
- Modify: `docs/adr/0001-web-server-runner-technology-direction.md`

1. Remove Config-owned Project and per-machine-ledger claims. Document central Project Catalog, Runner-owned local trust config, active Runner connection direction, and the M1/M2 protocol boundary.
2. Search for every stale ownership statement; do not maintain both old and new explanations.
3. Run `gofmt`, `go test -race ./...`, `go vet ./...`, and `go build ./cmd/qianshou` in `apps/control`.
4. Run `pnpm api:generate`, `pnpm api:check`, `pnpm format:check`, `pnpm lint`, `pnpm typecheck`, `pnpm test`, and `pnpm build` at repository root.
5. Inspect the exact worktree diff and report remaining risks. Stop before commit/push/PR; independent review belongs to the later authorized handoff.
