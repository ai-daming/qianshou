# ghfacts falsification matrix

This package is governed by one invariant:

> **A successful return proves the returned facts are complete, internally
> consistent, about the object that was requested, obtained through a channel
> whose identity we chose, and unambiguous. Anything else must return an
> error — never a partial fact set, never "no dependencies", never facts
> about something else.**

## Model: layers × dimensions

Obligations are derived deductively from the invariant, not inductively from
past review findings. Each **layer** of the read pipeline owns one
completeness conjunct; each **dimension** owns one truth property that spans
all layers.

Dimensions:

| Dimension | Question | Derivation |
|---|---|---|
| 良构 well-formedness | Can malformed input reach success? | "complete and consistent" |
| 绑定 binding | Can a well-formed response about something else reach success? | "about the object that was requested" |
| 歧义 ambiguity | Can repeated/conflicting representations be silently resolved by picking one? | "unambiguous" |
| 信道 channel | Can a response from an unchosen origin/type reach success? | "channel whose identity we chose" |
| 新鲜度 freshness | Can multi-request reads merge a world-state that never existed? | "internally consistent" (bounded; see residual risks) |

### Falsifiability rule

Every "enforced by" entry below cites a test that fails if the claim is
false. An entry without such a test must be marked `UNVERIFIED` — an
unverified entry is worse than an empty cell because it closes inquiry.
Round 6 demonstrated both failure modes: `http.DefaultClient` redirect
behavior and JSON duplicate keys were claimed as absorbed without tests.

### Matrix

| Layer \ Enforcement | Well-formedness | Binding | Ambiguity | Channel |
|---|---|---|---|---|
| 传输 transport | limit+1 read; empty/null body rejected; duplicate keys rejected (`TestRestResponseOverLimit…`, `TestNetworkJSONRejectsDuplicateKeys`) | — | `rejectDuplicateKeys` at every object level (`…/rest_item_field`, `…/graphql_pagination_signal`) | request deadline (`TestRequestsTimeOutInsteadOfHanging`); exact `application/json` via `mime.ParseMediaType` (`TestContentTypeMustBeExactApplicationJSON`); redirects refused by private client (`TestRedirectsAreRefusedAndTokenNeverLeaves`) |
| 协议 protocol | `hasNextPage` presence; `endCursor` required to continue (`TestRelationshipsFailsClosedWhenInnerSchemaMissing`, `…NextPageLacksCursor`) | next page bound to canonical request: origin, endpoint path, immutable params, monotonic page; URL rebuilt (`TestNextLinkMustStayWithinRequestedScope`) | all `Link` values considered; multiple `rel=next` fail (`TestAllLinkHeaderValuesAreConsidered`) | same-origin next only (`…/two_next_links…`, foreign link in `TestListMilestoneIssuesFailsClosedOnForeignNextLink`) |
| 语法 syntax | single JSON document (Unmarshal full-input), null/empty bodies fail (`TestListMilestoneIssuesFailsClosedOnNullBody`) | — | duplicate keys rejected before decode (`TestNetworkJSONRejectsDuplicateKeys`) | — |
| Schema | presence-aware DTOs: `Title *string`, `Labels *[]…`, `parent RawMessage`, `HasNextPage *bool`, `Nodes *[]*…` (`TestRelationshipsFailsClosedOnPresenceDrift`, `…InnerSchemaMissing`, `…TitleDrift`) | GraphQL echoes `nameWithOwner` + issue `number`; REST `GetIssue` matches numbers (`TestGraphQLResponseMustEchoRequestedIdentity`, `TestGetIssueFailsClosedOnNumberMismatch`) | presence vs null vs zero are distinct facts (presence tests) | — |
| 语义 semantic | `Issue.Validate` / `Relationships.Validate`: enums, positive numbers, non-empty names (`TestListMilestoneIssuesFailsClosedOnItemDrift`, `TestBuildFailsClosedOnInvalidFactShapes`) | identity checks in `Relationships.Validate` (self-parent/self-blocker) | duplicate labels/blockers fail (unified invariants) | — |
| 聚合 aggregation | cross-page facts must agree: parent consistent, blockers deduped, members unique (`TestRelationshipsFailClosedOnContradictoryPages`, `…OnDuplicateNumbers`) | facts belong to the requested issue number (`TestFromMilestoneFailsClosedOnRelationshipNumberMismatch`) | — | — |
| 消费 consumption | `scope.Build` re-validates both unified invariants for any `Facts` source (`TestBuildFailsClosed…`) | — | — | — |

## Primitive failure semantics

| Primitive | Failure semantics | Absorber (verified by) |
|---|---|---|
| `io.LimitReader` | Silently truncates at the limit | envelope reads limit+1 and errors (`TestRestResponseOverLimitFailsClosed`) |
| `http.Client` redirects | Follows redirects; forwards `Authorization` to same-host targets (empirically verified in round 6) | private client refuses all redirects (`TestRedirectsAreRefusedAndTokenNeverLeaves`) |
| `Header.Get` | Returns only the first header value | `Header.Values` + ambiguity rejection (`TestAllLinkHeaderValuesAreConsidered`) |
| `json.Unmarshal` duplicate keys | Last-wins silently | `rejectDuplicateKeys` for all network bodies (`TestNetworkJSONRejectsDuplicateKeys`); config files keep the documented last-wins policy — different domain, different rule |
| Content-Type prefix match | Accepts `application/jsonp` | `mime.ParseMediaType` exact match (`TestContentTypeMustBeExactApplicationJSON`) |
| Count-based loop termination | Infers protocol from page math | REST termination is Link-driven; page caps both sides (`TestListMilestoneIssuesFollowsLinkHeaderNext`, `…UnboundedPagination`) |

## Residual risks (registered, not covered)

- **Freshness between pages**: a multi-page listing cannot atomically snapshot
  GitHub; membership changes between page requests are only partially
  detectable (duplicates, contradictions). Registered as accepted.
- **Proxy environment**: Go's default transport honors `HTTP(S)_PROXY`; with
  HTTPS the proxy sees only CONNECT, but the trust posture is inherited from
  the local environment. UNVERIFIED — no test pins this.
- **TLS trust**: relies on the system root store via the default transport.
  UNVERIFIED — no test pins this.

New primitives or dimensions must be registered here with a failing-if-false
test before the entry may claim enforcement. Repairs follow `AGENTS.md`:
matrix first, RED falsification commit, then GREEN.
