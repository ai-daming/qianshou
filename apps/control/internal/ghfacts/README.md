# ghfacts falsification matrix

This package is governed by one invariant:

> **A successful return proves the returned facts are complete, internally
> consistent, about the object that was requested, obtained through a channel
> whose identity we chose, and unambiguous. Anything else must return an
> error — never a partial fact set, never "no dependencies", never facts
> about something else.**

## Model: layers × dimensions, cells by operation × primitive

Obligations are derived deductively from the invariant, not inductively from
past review findings. **Layers** (transport, protocol, syntax, schema,
semantic, aggregation, consumption) each own one completeness conjunct;
**dimensions** each own one truth property spanning all layers:

| Dimension | Question | Derivation |
|---|---|---|
| 良构 well-formedness | Can malformed input reach success? | "complete and consistent" |
| 绑定 binding | Can a well-formed response about something else reach success? | "about the object that was requested" |
| 歧义 ambiguity | Can repeated/conflicting representations be silently resolved by picking one? | "unambiguous" |
| 信道 channel | Can a response from an unchosen origin/type reach success? | "channel whose identity we chose" |
| 新鲜度 freshness | Can merged facts describe a world-state that never existed? | "internally consistent" |

A cell is not an atom: each cell is the set of obligations induced by every
operation × primitive pair inside it. A cell is closed only when each such
obligation is individually classified. **Cell classification** (exactly one):

- `ENFORCED` — with a test that fails if the enforcement is removed.
- `N/A` — with the reason and the counterexample condition under which it
  becomes applicable (re-checked when operations change).
- `UNVERIFIED` — suspected but not pinned by a test; must not be claimed as
  coverage.
- `ACCEPTED RESIDUAL` — proven undetectable at this layer; the detection gap
  is the reason.

**Claim-scope rule**: a claim's cited test must exercise the claim's full
stated scope. "Header.Get absorbed by Values" is a claim about every header;
it must cite tests for every header it names, or be narrowed to what the
tests exercise. Round 7 broke exactly this seam (Link was tested,
Content-Type was not).

## Matrix

Cells reference obligation IDs in
[`obligations.json`](obligations.json). DOWNGRADED (round 10): the shell
checker proves citation EXISTENCE only — a typo'd status silently exempts
an obligation, a citation may bind a same-named test in the wrong
package, 17 of 61 tests are unclassified, and nothing checks
README↔manifest consistency or the checker itself. It is a smoke check,
not a binding proof, until the manifest grows a schema, package-qualified
identities, and tamper tests.

| Layer | 良构 | 绑定 | 歧义 | 信道 | 新鲜度 |
|---|---|---|---|---|---|
| 传输 transport | ENFORCED: GH-T-WF-LIMIT, GH-T-WF-BODY | N/A — transport carries no identity; binding lives where identity fields decode (schema) | ENFORCED: GH-T-AM-DUPKEYS, GH-T-AM-UTF8 | ENFORCED: GH-T-CH-DEADLINE, GH-T-CH-MEDIATYPE, GH-T-CH-REDIRECT | N/A — single responses are atomic; multi-response conflicts belong to aggregation |
| 协议 protocol | ENFORCED: GH-P-WF-PAGEINFO, GH-P-WF-TERMINATION | ENFORCED: GH-P-BD-NEXTURL | DOWNGRADED (round 10): parseLinkHeader owns the grammar's STRUCTURE (one state machine: quoting, quoted-pair, separators, first-occurrence, required rel) but validates no ABNF alphabet — a quote inside an unquoted value (`rel=ne"xt`), an invalid relation-type (`rel="next,"`), and an illegal parameter name (`bad@name`) all pass, the first two reading as termination. **GAP: transcribe tchar/qdtext/quoted-pair/relation-type from the RFC text and reject every illegal character.** | ENFORCED: GH-P-CH-ORIGIN | ACCEPTED RESIDUAL: GH-A-FR-ATOMICITY |
| 语法 syntax | ENFORCED (as part of GH-T-WF-BODY: full-input Unmarshal, empty/null rejected) | N/A | ENFORCED (GH-T-AM-DUPKEYS runs before decode at every object level) | N/A | N/A |
| Schema | ENFORCED: GH-S-WF-PRESENCE, GH-S-WF-EXACTCASE (both REST root paths and GraphQL) | ENFORCED: GH-S-BD-GQLIDENTITY, GH-S-BD-RESTIDENTITY (full canonical repository_url identity) | ENFORCED (presence vs null vs zero vs wrong-case are distinct facts, same IDs) | N/A | N/A |
| 语义 semantic | ENFORCED: GH-SE-WF-INVARIANTS (unified Validate on opaque types) | ENFORCED (self-parent/self-blocker inside the unified invariants) | ENFORCED (duplicate labels/blockers fail, unified invariants) | N/A | N/A |
| 聚合 aggregation | ENFORCED: GH-A-WF-CONSISTENCY | ENFORCED (facts belong to the requested issue; enforced in Build) | ENFORCED (contradictions fail rather than resolve) | N/A | ENFORCED: GH-A-FR-STATES; GH-A-FR-ATOMICITY is ACCEPTED RESIDUAL |
| 消费 consumption | DOWNGRADED (round 10): opaque facts removed value-forging, but the exported `NewWithBase` constructor is a public mint — any consumer points the client at a self-controlled server whose self-reported `repository_url`/`nameWithOwner` come from the same attacker, and scope.FromMilestone accepts the result (the reviewer's stub repro). GH-C-WF-MULTICONTROL stays ENFORCED at its tested scope. **GAP: test authority must exist only inside test compilation (export_test.go), never via a production export.** | ENFORCED: GH-C-BD-FETCHFAIL | N/A | N/A | N/A |

## Primitive failure semantics

| Primitive | Failure semantics | Absorber (scope: tests that exercise it) |
|---|---|---|
| `io.LimitReader` | Silently truncates at the limit | envelope limit+1 (REST + GraphQL exits: `TestRestResponseOverLimitFailsClosed`, round-7 GraphQL padding repro) |
| `http.Client` redirects | Follows redirects; forwards `Authorization` to same-host targets | private client refuses all redirects (`TestRedirectsAreRefusedAndTokenNeverLeaves`) |
| `Header.Get` | Returns only the first header value | `Header.Values` + cardinality/ambiguity rejection — **applied to Link AND Content-Type** (`TestAllLinkHeaderValuesAreConsidered`, `TestHeaderCardinalityIsChecked`) |
| `encoding/json` field matching | Case-insensitive fold: distinct byte keys collapse to one field | exact-case key contracts + EqualFold duplicate scan (`TestDecoderEquivalenceClassesFailClosed`) |
| `encoding/json` invalid UTF-8 | Silently replaces with U+FFFD | `utf8.Valid` gate in `scanStrictJSON` (both exits: round-7 utf8 cases) |
| Link regex | Space-separated relation lists, quoted params with separators | RFC 8288 quote-aware parser (`TestLinkRelationTypeListsAreParsedPerRFC8288`); repeated params keep first occurrence §3.3 (`TestLinkRepeatedRelParamKeepsFirstOccurrence`); unclosed quotes fail (`TestLinkUnclosedQuoteFailsClosed`) |
| Exported fact structs | Zero values forge "read and empty"; public constructors are a mint; mutable exported fields unbind the stamp | opaque immutable types, no public constructor, concrete *ghfacts.Client seam (`GH-C-WF-UNFORGEABLE`) |
| Link grammar (splitter/validator/unquoter ×4) | Divergent state machines mis-split legal quoted-pairs | single grammar owner parseLinkHeader (`GH-P-AM-GRAMMAR`) |
| `json.Unmarshal` duplicate keys | Last-wins silently | strict scan at every object level for network bodies (`TestNetworkJSONRejectsDuplicateKeys`); config files keep the documented local last-wins policy — different domain, different rule |
| Count-based loop termination | Infers protocol from page math | Link-driven termination; page caps both sides (`TestListMilestoneIssuesFollowsLinkHeaderNext`, `…UnboundedPagination`) |

## Residual risks (registered)

- **Cross-request atomicity** (ACCEPTED RESIDUAL): a multi-page listing cannot
  atomically snapshot GitHub. Observable contradictions are enforced
  (aggregation freshness); membership changes between page requests that
  leave no contradiction are undetectable here.
- **Proxy environment** (UNVERIFIED): Go's default transport honors
  `HTTP(S)_PROXY`; HTTPS limits exposure to CONNECT but no test pins this.
- **TLS trust** (UNVERIFIED): relies on the system root store via the
  default transport; no test pins this.

New primitives or dimensions must be registered here with a
failing-if-false test before an entry may claim ENFORCED. Repairs follow
`AGENTS.md`: matrix first, RED falsification commit, then GREEN.
