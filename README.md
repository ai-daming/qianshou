# Qianshou

Qianshou is a local-first AI software delivery control plane. It keeps GitHub, Git, worktree, conversation, development brief, PR Review, and human handoff state visible, and can run explicitly selected Claude Code or Codex sessions without introducing a remote workflow runtime.

One `qianshou serve` process can read several configured GitHub repositories. Milestone and Issue Scopes are selected at runtime and refreshed from GitHub. Qianshou binds only to loopback; GitHub credentials remain inside the process and are never sent to the browser.

## Workspace

```text
apps/control       Go control-plane binary: `qianshou serve` / `qianshou run` (ADR 0001)
apps/server        TypeScript prototype, frozen as the executable spec
apps/web           Web console (React scaffold from M1-03)
packages/core      Shared TypeScript contracts (transitional)
packages/api-client Generated TypeScript client for the Go API
protocol/          OpenAPI contract, the single API truth source
.agents/skills     Repository-level Agent role skills
```

## Repository Agent skills

`.agents/skills/` is a versioned Qianshou product surface and part of every Qianshou source release. The repository version governs work performed against that Qianshou revision.

Packaged or binary distributions must ship the same-version repository skills or explicitly expose an equivalent embedded copy. A distribution that omits them is incomplete because it would lose Qianshou's role and mutation contracts.

Current repository skills include `gh-issue`, the governed GitHub Issue operator for creating, updating, commenting, classifying, relating, closing, and reopening Issues. `gh-issue` bundles the Issue-governance references it needs, so the same-version Skill folder may also be installed in a personal Agent skill directory and used outside this repository without depending on Qianshou source-tree paths. Inside a Qianshou checkout, prefer the repository version so its contract remains aligned with that source revision.

## Run in development

Prerequisites: Node.js >= 22, pnpm 10, Go >= 1.26, and an authenticated `gh` for real project data.

Install repository dependencies with `pnpm install`. The TypeScript server under `apps/server` is a frozen prototype, not the API V1 implementation.

## Go control binary

```bash
install -d -m 700 ~/.qianshou
install -m 600 config/config.example.json ~/.qianshou/config.json
# Edit repository slugs and absolute main-checkout paths.

cd apps/control
go test ./...
go build -o qianshou ./cmd/qianshou
./qianshou serve   # http://127.0.0.1:41727
./qianshou run     # runner process (functional from M2-02)
```

The read-only V1 endpoints list configured Projects, refresh a Project's GitHub Milestones, refresh the Issues currently in a Milestone, and read one Issue directly. Project responses omit local paths and Engine commands. Scope, Milestone membership, Issue facts, dependencies, Landing, and Workspace are not stored in configuration.

## Verify

```bash
pnpm check
```

`check` regenerates the TypeScript API client and rejects drift, then runs format check, typecheck, tests, and build. CI runs the same contract gate plus `go vet`/`go test`/`go build` on every push and pull request.

Machine-local configuration lives at `~/.qianshou/config.json`. GitHub remains the sole source for Milestone titles and membership, Issue titles, labels and state, and native Parent/Sub-issue and Blocked by/Blocking relationships.

Every conversation chooses Claude Code or Codex when it is created. That engine is immutable; switching engines requires a new conversation. Qianshou can attach an adopted development brief, DeliveryBaseline, or handoff package to the new conversation, but never transfers opaque vendor session state.
