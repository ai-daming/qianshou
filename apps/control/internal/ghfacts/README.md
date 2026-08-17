# ghfacts falsification matrix

This package is governed by one invariant:

> **A successful return proves the input was complete and internally
> consistent. Any input that cannot be proven complete and consistent must
> return an error — never a partial fact set, never "no dependencies".**

Every layer below owns one conjunct of that proof. For each layer the
falsification question is the negation of its conjunct: "construct an input
that violates this layer but still reaches success." A cell with no enforcing
code and no covering test is an open defect, regardless of whether a reviewer
has found it yet.

| Layer | Falsification question | Enforced by | Covered by |
|---|---|---|---|
| Transport | Can bytes be unread, over-limit, hung, or mis-typed (200 + HTML) while succeeding? | `readResponse`: request deadline, limit+1 read, Content-Type check | round-5 RED set: over-limit, padding+`]`, timeout, content-type |
| Protocol | Can the server say "more pages" (Link `rel="next"`, `hasNextPage`) while we treat the list as complete — or lead us to a foreign origin? | Link-driven REST pagination with same-origin validation and page caps; GraphQL cursor loop with `maxRelationshipPages` | round-5 RED set: short page + next, foreign next, malformed Link, unbounded pages |
| Syntax | Can extra or illegal JSON content after the first document be swallowed? | `getJSON` rejects empty/null bodies; single-document enforcement downstream (config parser enforces strict EOF) | null/empty body tests |
| Schema | Can a missing or null field fold into a default value? | Presence-aware DTOs: `Title *string`, `Labels *[]…`, `parent json.RawMessage`, `HasNextPage *bool`, `Nodes *[]*…` | presence/drift test groups |
| Semantic | Can an out-of-domain value pass (state enums, zero numbers, empty strings)? | `Issue.Validate`, `Relationships.Validate` (unified invariants) | round-3/4 test groups |
| Aggregation | Can multi-page or multi-fetch facts contradict each other and still merge? | Cross-page parent agreement, cross-page blocker dedup, duplicate member rejection | round-4 test groups |
| Consumption | Can invalid facts be trusted downstream? | `scope.Build` re-validates both invariants; `Validate` shared by all exits | `TestBuildFailsClosed…` |

## Primitive failure semantics on this path

| Primitive | Failure semantics | Absorbed by |
|---|---|---|
| `io.LimitReader` | Silently truncates at the limit | `readResponse` reads limit+1 and errors on overrun |
| `json.Decoder` duplicate keys | Last-wins silently | Documented policy: `Validate()` checks effective semantics (see `config.parse`) |
| `http.Client` redirects | May forward `Authorization` cross-origin | ghfacts never redirects (JSON POST/GET to fixed origins); Link following validates same-origin before any request |
| Count-based loop termination | Infers protocol from page math | REST termination is Link-driven only; both loops carry page caps |
| Count-based `for` without bound | Infinite loop on adversarial servers | `maxRelationshipPages` / REST page cap |

New primitives or layers must be registered here with their failure semantics
and absorber before use. Repairs follow the discipline in `AGENTS.md`: matrix
first, RED falsification commit, then GREEN.
