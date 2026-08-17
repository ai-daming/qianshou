# Qianshou Agent Instructions

Before changing Qianshou architecture or workflow semantics, read:

- `docs/architecture/control-plane.md`
- every affected role contract under `.agents/skills/`

## Boundaries

- GitHub, Git, test output, PR head SHAs, and Review records are evidence sources. Never replace them with an Agent's completion claim.
- Qianshou's local ledger records handoff state; it does not redefine external facts.
- Keep Developer and Reviewer independent. Reviewer findings return to the implementer; Reviewer does not repair code.
- Never merge, push, publish, deploy, or start an Agent from the dashboard without explicit user action.
- Keep the server bound to `127.0.0.1`. Never expose GitHub credentials or raw environment values to the browser.
- Use test-first development for workflow-state changes.

## Fail-closed repair discipline

A Review finding is a symptom, not a work order. When fixing any validation
or fail-closed defect:

1. Name the invariant the finding violates and the conjunct responsible;
   find where that conjunct is (or should be) enforced.
2. Before writing code, produce the layer matrix — transport, protocol,
   syntax, schema, semantic, aggregation, consumption — and for every cell
   answer the falsification question: "can an input that violates this
   layer still reach success today?" Empty cells are the work list.
3. Commit falsification tests for the empty cells first as a standalone RED
   commit (pushed so CI records the failure), then the fix as GREEN.
4. In the PR reply, present the matrix rather than only per-finding
   dispositions, and audit every primitive on the path for its own failure
   semantics (silent truncation, redirect forwarding, last-wins decoding)
   and who absorbs each.

See `apps/control/internal/ghfacts/README.md` for the matrix this package
is governed by.
