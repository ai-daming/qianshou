# ADR 0001: Web, Server, and Runner technology direction

Status: Proposed (adopted with the M1-01 delivery PR)
Decision date: 2026-08-15

## Context

Qianshou starts from a working TypeScript prototype (apps/server, apps/web, packages/core) that has never been committed. The M1 milestone must bootstrap a public, reproducible repository baseline and a trustworthy local delivery loop. M2 then evolves remote, multi-host operation: a central server on one host and runners on macOS and Linux machines.

The open questions this ADR settles:

1. Final implementation language and process split for Web, Server, and Runner.
2. Single binary with subcommands versus two independent binaries.
3. Communication protocol, SQLite ownership, and TypeScript client generation.
4. Migration order from the TypeScript prototype to the target form.

## Decision

### Process responsibilities

| Process | Language | Responsibilities |
|---|---|---|
| Web | React + TypeScript SPA | Interaction only. Owns transient UI state; never business truth or `allowedActions` logic. |
| Server | Go | Sole owner of SQLite and the versioned HTTP/JSON API. Computes derived stages, capabilities, and blocked reasons. Aggregates GitHub, Git, and runner facts. |
| Runner | Go | Executes structured local commands (Git, worktree, test, agent) on its host and reports events. Never opens the central SQLite; talks only to the server API. |

### Repository layout

The monorepo keeps one declared structure for every language:

```text
apps/control/       Go single-binary module: go.mod, cmd/qianshou, internal/{server,runner}
apps/server/        TypeScript prototype (frozen executable spec; retires in M2-01)
apps/web/           Web console (vanilla TS now, React scaffold from M1-03)
packages/core/      TypeScript hand-written contracts (transitional; retires in M2-01)
protocol/           OpenAPI contract, the language-neutral single truth source
.agents/skills/     Repository-local Agent role skills
```

`pnpm-workspace.yaml` globs `apps/*` and `packages/*` but ignores directories without a `package.json`, so the Go module coexists without interference. Protocol lives outside any app because both the Go server and the generated TypeScript client consume it.

### Binary shape: one binary with subcommands

A single Go module compiles into one `qianshou` binary:

```text
qianshou serve   central control server
qianshou run     runner process (functional from M2-02)
```

Rationale: cross-compiled static distribution of one artifact, one version to track, and shared protocol code without a second module. The "runner never touches central SQLite" boundary is enforced by network architecture (the runner only holds an API identity, never database access), not by binary splitting. The tradeoff accepted: runner hosts carry inert server code in the binary.

### Protocol and contract

- The server exposes a versioned HTTP/JSON API. `protocol/openapi.yaml` is the single contract source for every endpoint.
- The runner command protocol is idempotent by `commandId`, version-gated, and designed in M2-02; it rides the same HTTP transport.
- The TypeScript client is generated from the OpenAPI contract with `@hey-api/openapi-ts` into a generated package. Hand-written contract types in `packages/core` are transitional and retire with the prototype.
- Web build output is embedded into the `qianshou` binary at compile time via `go:embed`; development keeps the Vite dev server with an `/api` proxy.

### SQLite ownership

The server is the only live operational process that opens the SQLite ledger, using the pure-Go driver `modernc.org/sqlite` to keep cross-compilation and the single-binary goal painless. An exclusive ownership lock prevents a second Server from racing recovery. The explicit offline `inspect-frame` maintenance command may open the database read-only only while the Server is stopped. Schema DDL and ordered checksum-verified migrations live with the Go server.

### Language and migration order: Go from the start

Compared alternatives:

- **All TypeScript** — lowest immediate cost, but M2 runner distribution would drag a Node runtime or a bundler onto every host, and the port would merely be postponed.
- **TypeScript Server + Go Runner** — keeps the prototype evolving, but every M1 feature written in TypeScript server code becomes M2 migration debt.
- **Go Server + Go Runner (chosen)** — the prototype is frozen on day one; every line of M1 business code lands directly in the target form. Accepted cost: the bootstrap loop (M1 DoD) completes later because the Go foundation is rebuilt first.

Migration order:

1. M1-01 commits the TypeScript prototype as the executable specification, then freezes it (bug fixes only). This ADR, the Go skeleton, the OpenAPI seed, CI, and the formatting baseline land through this PR.
2. M1-02 through M1-07 are implemented in Go directly against the frozen contract: the API surface of the prototype is captured in `protocol/openapi.yaml` as API v1 so the existing web keeps working unchanged.
3. During M1, agent commands execute inside the server process behind an internal executor interface (the runner exists as a library). M2-02 extracts `qianshou run` as a standalone client that actively connects to the central Server; the Server never dials Runner hosts or uses SSH as its execution protocol.
4. The React scaffold starts at M1-03 (#4); M1-05 and M1-06 build their UI on it. The Workbench finishing work stays in M2-03 (#12).
5. The TypeScript prototype and `packages/core` retire in M2-01 (#10) once Go tests cover the comparison points.

## Consequences

- Zero porting debt for M1 business code; the bootstrap milestone runs longer instead.
- The central Server remains bound to loopback through M1. Standalone Runner extraction does not authorize remote plaintext operation: remote registration, credential validation, authentication, TLS, and any non-loopback exposure must land together behind the M2-05 security boundary.
- Contract-first development: server changes must update `protocol/openapi.yaml`, and CI regenerates and type-checks the client so drift fails the build instead of surfacing at runtime.
- The frozen TypeScript prototype remains runnable for comparison until M2-01 removes it.
