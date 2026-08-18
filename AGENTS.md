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

A Review finding is a symptom, not a work order. The fixer converges while
the reviewer falsifies, so a self-check that only confirms the fix tests a
sample while review tests the population — adopt the falsifier's questions
below as your own. When fixing any validation or fail-closed defect:

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
   because it closes inquiry. Where a machine-readable obligations manifest
   exists (as for ghfacts), CI must verify the manifest schema, resolve
   every citation in its named package, agree with the prose in both
   directions, and force every test to be classified — claims are not
   tracked by discipline alone, and the checker itself carries tamper
   tests.
6. A cell is not an atom. Classify every obligation inside it — expanded by
   operation × primitive — as exactly one of ENFORCED (with test), N/A (with
   reason and the counterexample condition that would make it applicable),
   UNVERIFIED, or ACCEPTED RESIDUAL. "One enforcing point per cell" has never
   closed a cell.
7. Claim scope must equal tested scope: a claim spanning several sites,
   headers, or exits must cite tests exercising each of them, or be narrowed
   to what the tests actually cover. The claim text must track obligation
   discharge ("contract registered for every root path"), not mechanism
   invocation ("scanner runs on every exit").
8. Ask the forger question for every exported fact type: can a consumer
   construct a value that satisfies validation without having been read?
   Completeness is provenance, not value shape — it must be carried by the
   type (opaque immutable facts, no public mint), and deliberate weakenings
   made for convenience must be registered as weakenings, never written in
   obligatory language.
9. Attack your own fix before pushing it. For every new enforcement
   mechanism, write and run tests that abuse the mechanism itself, using the
   attacker's questions: who can mint the trusted state it relies on? is the
   trusted state bound to immutable content? does any hand-written parsing
   cover the full grammar of its standard? Attack tests ship in the same
   change as the fix. A fix verified only by the finding's repro verifies a
   sample, not the class.
10. One grammar, one owner. Never implement a documented grammar (RFC, wire
    protocol, file format) from memory or in pieces: read the standard's
    grammar section before writing code, and keep exactly one parser for the
    whole grammar. Multiple partial state machines for the same syntax
    diverge on the first legal input one of them mishandles, and the
    divergence is invisible to tests that only feed familiar shapes.
11. Quote external evidence by reading it back. Run IDs, commit SHAs, URLs,
    and issue numbers are copied from a fresh query of the source, never
    transcribed from truncated or reformatted output. Evidence is what the
    source says, not what you remember it said.
12. A comment is not evidence. A safety claim attached to a mechanism
    ("this grants no authority", "this never redirects") is either backed
    by an attack test in the same change or deleted — across rounds, such
    claims were the first casualty every time. Mechanisms ship by
    adversarial strength, not by functional completeness: a checker needs
    tamper tests against itself, a parser needs the standard's full
    alphabet transcribed from the text, and a new public API needs its
    mint-surface answered before push.

See `apps/control/internal/ghfacts/README.md` for the matrix this package
is governed by.
