# Qianshou

Qianshou is a local-first AI software delivery control plane. It keeps GitHub, Git, worktree, conversation, development brief, PR Review, and human handoff state visible, and can run explicitly selected Claude Code or Codex sessions without introducing a remote workflow runtime.

The first configured project is Mamamate Milestone 7. Qianshou binds only to `127.0.0.1`; GitHub credentials remain inside the local `gh` process and are never sent to the browser.

## Workspace

```text
apps/control       Go control-plane binary: `qianshou serve` / `qianshou run` (ADR 0001)
apps/server        TypeScript prototype, frozen as the executable spec
apps/web           Web console (React scaffold from M1-03)
packages/core      Shared TypeScript contracts (transitional)
protocol/          OpenAPI contract, the single API truth source
.agents/skills     Repository-level Agent role skills
```

## Repository Agent skills

`.agents/skills/` is a versioned Qianshou product surface and part of every Qianshou source release. The repository version governs work performed against that Qianshou revision.

Packaged or binary distributions must ship the same-version repository skills or explicitly expose an equivalent embedded copy. A distribution that omits them is incomplete because it would lose Qianshou's role and mutation contracts.

Current repository skills include `gh-issue`, the governed GitHub Issue operator for creating, updating, commenting, classifying, relating, closing, and reopening Issues. `gh-issue` bundles the Issue-governance references it needs, so the same-version Skill folder may also be installed in a personal Agent skill directory and used outside this repository without depending on Qianshou source-tree paths. Inside a Qianshou checkout, prefer the repository version so its contract remains aligned with that source revision.

## Run in development

Prerequisites: Node.js >= 22, pnpm 10, Go >= 1.26, and an authenticated `gh` for real project data.

```bash
pnpm install
cp config/projects.example.json config/projects.json  # fill in your repo slug and local paths
pnpm dev
```

`config/projects.json` is machine-local and ignored by Git.

Open <http://127.0.0.1:41728>. The Vite dev server proxies `/api` to the local API on port `41727`.

For the built application:

```bash
pnpm build
pnpm start
```

Then open <http://127.0.0.1:41727>.

## Go control binary

```bash
cd apps/control
go test ./...
go build -o qianshou ./cmd/qianshou
./qianshou serve   # central control server (skeleton; see ADR 0001)
./qianshou run     # runner process (functional from M2-02)
```

## Verify

```bash
pnpm check
```

`check` runs format check, typecheck, tests, and build. CI runs the same suite plus `go vet`/`go test`/`go build` on every push and pull request.

Runtime handoff state is stored under `.qianshou/` and intentionally ignored by Git. `config/projects.json` contains only repository/Milestone selectors, local paths and runtime preferences. GitHub remains the sole source for the Milestone title, Issue membership, Issue roles, Issue state, and native Parent/Sub-issue and Blocked by/Blocking relationships.

Every conversation chooses Claude Code or Codex when it is created. That engine is immutable; switching engines requires a new conversation. Qianshou can attach an adopted development brief, DeliveryBaseline, or handoff package to the new conversation, but never transfers opaque vendor session state.
