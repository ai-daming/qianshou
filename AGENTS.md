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
2. Before writing code, produce the layer × dimension matrix. Layers:
   transport, protocol, syntax, schema, semantic, aggregation, consumption.
   Dimensions: well-formedness, binding (is the fact about the object this
   request asked for?), ambiguity (are repeated representations rejected
   rather than silently resolved?), channel (does the response come from an
   origin and media type we chose?), freshness (multi-request consistency).
   Derive both axes from the invariant — do not generalize from past
   findings, which only covers what has already been observed.
3. Commit falsification tests for the empty cells first as a standalone RED
   commit (pushed so CI records the failure), then the fix as GREEN.
4. In the PR reply, present the matrix rather than only per-finding
   dispositions, and audit every primitive on the path for its own failure
   semantics (silent truncation, redirect forwarding, last-wins decoding,
   first-header-value-only reads) and who absorbs each.
5. Matrix entries are claims: every "enforced by / absorbed by" must cite a
   test that fails when the claim is false; entries without such a test must
   be marked UNVERIFIED. An unverified entry is worse than an empty cell
   because it closes inquiry.
6. A cell is not an atom. Classify every obligation inside it — expanded by
   operation × primitive — as exactly one of ENFORCED (with test), N/A (with
   reason and the counterexample condition that would make it applicable),
   UNVERIFIED, or ACCEPTED RESIDUAL. "One enforcing point per cell" has never
   closed a cell.
7. Claim scope must equal tested scope: a claim spanning several sites,
   headers, or exits must cite tests exercising each of them, or be narrowed
   to what the tests actually cover.

See `apps/control/internal/ghfacts/README.md` for the matrix this package
is governed by.
