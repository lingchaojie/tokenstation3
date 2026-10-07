# Billing integration — in progress

No service/repository integration result is claimed yet. Shared-package merge
conflicts still block compilation; the approved plan permits assembling the
candidate before behavioral RED/GREEN mutation checks.

## Decisions and implementation

- User selected option 1 for account-cost long context: catalog/provider
  long-context tiers remain independent of customer billing switches. Preserve
  local pricing rules, media dimensions and provenance. The upstream RecordUsage
  regression now checks the retained cost policy across account/group gates.
- Image price overrides use one helper for flat/interval/channel paths.
  Unset values retain the catalog bucket, explicit zero remains configured,
  and local input/output provenance and sparse-bucket validation stay intact.
  Retained both local missing-price tests and upstream catalog-inheritance tests.
- New models and Astra Ultrafast 6× were retained; the latter is model-owned,
  independent of Fast overrides. The policy's early return does not skip Astra:
  its existing Fast ratio is positive. Fable 5.1 max fallback remains 3× and
  explicit effort maps take precedence.
- Deleted API Keys skip only ErrAPIKeyNotFound in their quota/window counters.
  Local reward/subscription/account settlement and simple-mode quota helper
  remain in the same transaction. Expanded the new integration test to check
  both user and provider-account costs and idempotent replay. Existing SQL-error
  rollback tests remain.
- Inflight reservation defaults OFF (config test RED then GREEN). Enabled
  reservation uses the local balance snapshot, never sums funding layers,
  honors reward priority even with a subscription, skips quota-only subscriptions
  and uses account funds only on permitted subscription fallback. Simple mode
  still skips reservations. First-request admission and Redis fail-open remain.
- Handler estimation consults funding before unpriced fail-closed checks.
  Estimation uses the existing token pricing engine for sparse validation,
  long context, service tiers and effort; an explicit zero is priced, not
  unknown. Explicit per-request cards are not replaced by generic media prices.
  Unsupported Composite/Grok audio references were removed from the new helper.
- Existing layered-balance settlement invalidates the snapshot synchronously;
  the upstream synchronous scalar decrement is retained only for legacy
  scalar-result paths. It does not double-deduct layered snapshots.
- Free Fast: preserve local missing-price rejection, including Standard-tier
  re-evaluation. Upstream's new comment assumes earlier zero-cost fallback, but
  the local cost path returns an error instead. Adapted the new usage-log test
  to assert rejection/no zero-cost row, consistent with the existing local
  MissingPricingFailsClosed regression. No new pricing policy was introduced.

## Pending evidence

- Compile shared candidate, then run/mutation-check new local funding/pricing
  tests, full account-cost and payment regressions.
- Add/verify expiry refresh, handler subscription/reward/fallback and failure
  paths with real cache behavior; run Redis reservation integration/race tests.
- Audit remaining pricing resources, model-plaza/account reporting consumers,
  cache lifecycle and all gateway reservation hand-offs.
- Verify full tests, generation, independent review and exact-SHA CI before push.
