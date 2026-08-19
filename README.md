# Qianshou

Qianshou is a local-first AI software delivery control plane. It keeps GitHub, Git, worktree, conversation, development brief, PR Review, and human handoff state visible, and can run explicitly selected Claude Code or Codex sessions without introducing a remote workflow runtime.

One central `qianshou serve` process exclusively owns the SQLite Project Catalog and ledger. Milestone and Issue Scopes are selected at runtime and refreshed from GitHub. Qianshou binds only to loopback; GitHub credentials and raw Vendor frames remain inside the process and are never sent to the browser.

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
# Edit the local Runner id, allowed roots, and enabled Engine commands.

cd apps/control
go test ./...
go build -o qianshou ./cmd/qianshou
./qianshou serve   # http://127.0.0.1:41727
./qianshou run     # runner process (functional from M2-02)
```

Create a Project through the central API; GitHub's numeric repository ID is resolved and frozen before SQLite accepts it:

```bash
curl --fail-with-body \
  -H 'Content-Type: application/json' \
  --data '{"id":"qianshou","repositorySlug":"ai-daming/qianshou"}' \
  http://127.0.0.1:41727/api/v1/projects
```

V1 lists central Projects, refreshes a Project's current GitHub Milestones and Issues, and reads one Issue directly. Config V1 rejects `projects`; there is no import or fallback. Project responses expose the immutable repository ID and historical `creationSlug`, but never checkout paths or Engine commands.

Raw Vendor frames are exact and unredacted. They have no browser or ordinary HTTP endpoint. For explicit debugging, stop the Server and read one frame directly to stdout:

```bash
./qianshou inspect-frame --run <run-id> --sequence 1
```

## Verify

```bash
pnpm check
```

`check` regenerates the TypeScript API client and rejects drift, then runs format check, typecheck, tests, and build. CI runs the same contract gate plus `go vet`/`go test`/`go build` on every push and pull request.

Machine-local configuration lives at `~/.qianshou/config.json` and owns only Runner execution trust. GitHub remains the sole source for current repository slug, Milestone titles and membership, Issue titles, labels and state, and native Parent/Sub-issue and Blocked by/Blocking relationships.

Every conversation chooses Claude Code or Codex when it is created. That engine is immutable; switching engines requires a new conversation. Qianshou can attach an adopted development brief, DeliveryBaseline, or handoff package to the new conversation, but never transfers opaque vendor session state.
