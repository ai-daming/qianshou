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
| 协议 protocol | ENFORCED: `hasNextPage`/`endCursor` presence (`TestRelationshipsFailsClosedWhenInnerSchemaMissing`, `…NextPageLacksCursor`) | ENFORCED: next page bound to canonical request — origin, path, immutable params, monotonic page, rebuilt URL (`TestNextLinkMustStayWithinRequestedScope`) | ENFORCED at all three granularities: header-value (multiple rel=next across values fails), JSON-key (EqualFold dup scan), and parameter-occurrence × quote-state — first occurrence wins per RFC 8288 §3.3, unclosed quotes fail (`TestAllLinkHeaderValuesAreConsidered`, `TestLinkRepeatedRelParamKeepsFirstOccurrence`, `TestLinkUnclosedQuoteFailsClosed`) | ENFORCED: same-origin next only (`TestListMilestoneIssuesFailsClosedOnForeignNextLink`) | ACCEPTED RESIDUAL: cross-request atomicity of pagination is undetectable without re-listing; contradictions that ARE observable are enforced at aggregation |
| 语法 syntax | ENFORCED: Unmarshal full-input single document; empty/null bodies fail (`TestListMilestoneIssuesFailsClosedOnNullBody`) | N/A — syntax has no identity | ENFORCED: strict scan before decode (`TestNetworkJSONRejectsDuplicateKeys`) | N/A | N/A |
| Schema | ENFORCED: presence-aware DTOs + exact-case key contracts on BOTH REST root paths — listing `[]` and single-issue `""` (`TestRelationshipsFailsClosedOnPresenceDrift`, `…TitleDrift`, `TestDecoderEquivalenceClassesFailClosed`, `TestGetIssueRootEnforcesExactCaseSchema`) | ENFORCED: GraphQL echoes `nameWithOwner`+`number` (`TestGraphQLResponseMustEchoRequestedIdentity`); REST `repository_url` must equal the FULL canonical API identity — https, api.github.com, /repos/{slug}, no userinfo/query/fragment (`TestRepositoryURLMustMatchCanonicalAPIIdentity`, 5 variants; path direction in `TestRestResponsesMustBindToRequestedRepository`); listing `milestone` ENFORCED | ENFORCED: presence vs null vs zero-value vs wrong-case are distinct facts (same tests, both root paths) | N/A | N/A |
| 语义 semantic | ENFORCED: `Issue.Validate`/`Relationships.Validate` — enums, positive numbers, non-empty names (`…ItemDrift`, `TestBuildFailsClosedOnInvalidFactShapes`) | ENFORCED: self-parent/self-blocker rejected (`TestRelationshipsFailClosedOnSelfReferences`) | ENFORCED: duplicate labels/blockers fail (unified invariants) | N/A | N/A |
| 聚合 aggregation | ENFORCED: cross-page parent agreement, blocker dedup, member dedup (`TestRelationshipsFailClosedOnContradictoryPages`, `…OnDuplicateNumbers`) | ENFORCED: facts belong to the requested issue number (`TestFromMilestoneFailsClosedOnRelationshipNumberMismatch`) | ENFORCED: contradictions fail rather than resolve | N/A | ENFORCED (observable part): one state per issue across members and all blocker references (`TestBuildFailsClosedOnCrossFactStateContradictions`); ACCEPTED RESIDUAL: unobservable between-request drift |
| 消费 consumption | ENFORCED: fact types are unforgeable — `Issue`/`Relationships` carry an unexported provenance bit set only by decoding or by `NewIssue`/`NewRelationships`; `scope.Build` re-validates both, so struct literals from ANY Facts implementation are refused (`TestBuildRejectsForgedZeroValueFacts`), and invalid facts cannot even be constructed (`TestConstructorsRejectInvalidFacts`) | N/A — consumption adds no facts | N/A | N/A | N/A |

## Primitive failure semantics

| Primitive | Failure semantics | Absorber (scope: tests that exercise it) |
|---|---|---|
| `io.LimitReader` | Silently truncates at the limit | envelope limit+1 (REST + GraphQL exits: `TestRestResponseOverLimitFailsClosed`, round-7 GraphQL padding repro) |
| `http.Client` redirects | Follows redirects; forwards `Authorization` to same-host targets | private client refuses all redirects (`TestRedirectsAreRefusedAndTokenNeverLeaves`) |
| `Header.Get` | Returns only the first header value | `Header.Values` + cardinality/ambiguity rejection — **applied to Link AND Content-Type** (`TestAllLinkHeaderValuesAreConsidered`, `TestHeaderCardinalityIsChecked`) |
| `encoding/json` field matching | Case-insensitive fold: distinct byte keys collapse to one field | exact-case key contracts + EqualFold duplicate scan (`TestDecoderEquivalenceClassesFailClosed`) |
| `encoding/json` invalid UTF-8 | Silently replaces with U+FFFD | `utf8.Valid` gate in `scanStrictJSON` (both exits: round-7 utf8 cases) |
| Link regex | Space-separated relation lists, quoted params with separators | RFC 8288 quote-aware parser (`TestLinkRelationTypeListsAreParsedPerRFC8288`); repeated params keep first occurrence §3.3 (`TestLinkRepeatedRelParamKeepsFirstOccurrence`); unclosed quotes fail (`TestLinkUnclosedQuoteFailsClosed`) |
| Exported fact structs | Zero values forge "read and empty" | unexported provenance bit + validating constructors (`TestBuildRejectsForgedZeroValueFacts`, `TestConstructorsRejectInvalidFacts`) |
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
