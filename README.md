# Qianshou

Qianshou is a local-first AI software delivery control plane. It keeps GitHub, Git, worktree, conversation, development brief, PR Review, and human handoff state visible, and can run explicitly selected Claude Code or Codex sessions without introducing a remote workflow runtime.

The first configured project is Mamamate Milestone 7. Qianshou binds only to `127.0.0.1`; GitHub credentials remain inside the local `gh` process and are never sent to the browser.

## Workspace

```text
apps/server       TypeScript HTTP API and CLI adapters
apps/web          TypeScript + Vite control console
packages/core     Shared workflow types, schemas, and contracts
.agents/skills    Repository-level Agent role skills
```

## Run in development

```bash
pnpm install
pnpm dev
```

Open <http://127.0.0.1:41728>. The Vite dev server proxies `/api` to the local API on port `41727`.

For the built application:

```bash
pnpm build
pnpm start
```

Then open <http://127.0.0.1:41727>.

## Verify

```bash
pnpm check
```

Runtime handoff state is stored under `.qianshou/` and intentionally ignored by Git. `config/projects.json` contains only repository/Milestone selectors, local paths and runtime preferences. GitHub remains the sole source for the Milestone title, Issue membership, Issue roles, Issue state, and native Parent/Sub-issue and Blocked by/Blocking relationships.

Every conversation chooses Claude Code or Codex when it is created. That engine is immutable; switching engines requires a new conversation. Qianshou can attach an adopted development brief, DeliveryBaseline, or handoff package to the new conversation, but never transfers opaque vendor session state.
