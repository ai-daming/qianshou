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

Cell entries cite tests; `—` means `N/A` with the reason inline.

| Layer | 良构 | 绑定 | 歧义 | 信道 | 新鲜度 |
|---|---|---|---|---|---|
| 传输 transport | ENFORCED: limit+1 read, UTF-8 gate, single-body JSON (`TestRestResponseOverLimit…`, `TestNetworkJSONRejectsDuplicateKeys`, round-7 utf8 cases) | N/A — transport carries no identity semantics; binding lives where identity fields decode (schema 层) | ENFORCED: duplicate keys under the decoder's equivalence relation (`TestDecoderEquivalenceClassesFailClosed`) | ENFORCED: deadline, exact single `application/json`, redirects refused (`TestRequestsTimeOutInsteadOfHanging`, `TestHeaderCardinalityIsChecked`, `TestRedirectsAreRefusedAndTokenNeverLeaves`) | N/A — a single response is atomic; only multi-response merges can conflict (聚合层) |
| 协议 protocol | ENFORCED: `hasNextPage`/`endCursor` presence (`TestRelationshipsFailsClosedWhenInnerSchemaMissing`, `…NextPageLacksCursor`) | ENFORCED: next page bound to canonical request — origin, path, immutable params, monotonic page, rebuilt URL (`TestNextLinkMustStayWithinRequestedScope`) | DOWNGRADED (round 8): ambiguity is enforced at header-VALUE and JSON-key granularity, NOT at parameter-occurrence × quote-state granularity inside one link-value — `rel="next"; rel="prev"` last-wins erases next, an unclosed quote hides it. Tests cover value/key levels only. | ENFORCED: same-origin next only (`TestListMilestoneIssuesFailsClosedOnForeignNextLink`) | ACCEPTED RESIDUAL: cross-request atomicity of pagination is undetectable without re-listing; contradictions that ARE observable are enforced at aggregation |
| 语法 syntax | ENFORCED: Unmarshal full-input single document; empty/null bodies fail (`TestListMilestoneIssuesFailsClosedOnNullBody`) | N/A — syntax has no identity | ENFORCED: strict scan before decode (`TestNetworkJSONRejectsDuplicateKeys`) | N/A | N/A |
| Schema | ENFORCED (listing root `[]` only — round 8 falsified the wider claim): presence-aware DTOs + exact-case key contracts for the LISTING root path (`TestRelationshipsFailsClosedOnPresenceDrift`, `…TitleDrift`, `TestDecoderEquivalenceClassesFailClosed/lone_wrong-case…`). **GAP (round 8): GetIssue root path `""` has no exact-case contract — wrong-case-only fields pass.** | DOWNGRADED (round 8): GraphQL echoes `nameWithOwner`+`number` (ENFORCED, `TestGraphQLResponseMustEchoRequestedIdentity`); REST `repository_url` is compared on PATH ONLY — a same-path foreign host passes as a binding witness (`TestRestResponsesMustBindToRequestedRepository` proves the path direction only). **GAP: canonical API identity (scheme/host/userinfo/query/fragment) unverified.** Listing `milestone` ENFORCED. | ENFORCED: presence vs null vs zero-value vs wrong-case are distinct facts (same tests, listing scope) | N/A | N/A |
| 语义 semantic | ENFORCED: `Issue.Validate`/`Relationships.Validate` — enums, positive numbers, non-empty names (`…ItemDrift`, `TestBuildFailsClosedOnInvalidFactShapes`) | ENFORCED: self-parent/self-blocker rejected (`TestRelationshipsFailClosedOnSelfReferences`) | ENFORCED: duplicate labels/blockers fail (unified invariants) | N/A | N/A |
| 聚合 aggregation | ENFORCED: cross-page parent agreement, blocker dedup, member dedup (`TestRelationshipsFailClosedOnContradictoryPages`, `…OnDuplicateNumbers`) | ENFORCED: facts belong to the requested issue number (`TestFromMilestoneFailsClosedOnRelationshipNumberMismatch`) | ENFORCED: contradictions fail rather than resolve | N/A | ENFORCED (observable part): one state per issue across members and all blocker references (`TestBuildFailsClosedOnCrossFactStateContradictions`); ACCEPTED RESIDUAL: unobservable between-request drift |
| 消费 consumption | DOWNGRADED (round 8): `scope.Build` re-validates VALUES for any `Facts` source, but the fact TYPES are forgeable — a zero-value `Relationships{Number:n}` passes validation and fabricates "read, no dependencies"; `Parent:nil` cannot distinguish "explicitly none" from "never read". Provenance is not carried by the types (`TestBuildFailsClosed…` proves value validation only). **GAP: unforgeable facts — validated constructors or presence-bearing representation.** | N/A — consumption adds no facts | N/A | N/A | N/A |

## Primitive failure semantics

| Primitive | Failure semantics | Absorber (scope: tests that exercise it) |
|---|---|---|
| `io.LimitReader` | Silently truncates at the limit | envelope limit+1 (REST + GraphQL exits: `TestRestResponseOverLimitFailsClosed`, round-7 GraphQL padding repro) |
| `http.Client` redirects | Follows redirects; forwards `Authorization` to same-host targets | private client refuses all redirects (`TestRedirectsAreRefusedAndTokenNeverLeaves`) |
| `Header.Get` | Returns only the first header value | `Header.Values` + cardinality/ambiguity rejection — **applied to Link AND Content-Type** (`TestAllLinkHeaderValuesAreConsidered`, `TestHeaderCardinalityIsChecked`) |
| `encoding/json` field matching | Case-insensitive fold: distinct byte keys collapse to one field | exact-case key contracts + EqualFold duplicate scan (`TestDecoderEquivalenceClassesFailClosed`) |
| `encoding/json` invalid UTF-8 | Silently replaces with U+FFFD | `utf8.Valid` gate in `scanStrictJSON` (both exits: round-7 utf8 cases) |
| Link regex | Space-separated relation lists, quoted params with separators | RFC 8288 quote-aware parser (`TestLinkRelationTypeListsAreParsedPerRFC8288`) |
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
