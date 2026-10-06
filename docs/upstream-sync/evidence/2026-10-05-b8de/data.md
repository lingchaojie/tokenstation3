# Data / payment integration evidence (in progress)

## 2026-10-06 follow-up (supersedes earlier compile-blocked status)

Final provider currency must re-quote both the discount and the fee, then
reselect/revalidate the channel against that final amount. New regression uses
101 at15% discount and0.1% fee: initial CNY85.94 becomes JPY87, with bonus15.
It checks persisted snapshots, max86 rejection, and a second currency change
failing checkout. No provider network request occurs. Observed RED before fix
(`payment-final-currency-red.log`); `go test -count=1 -p 2 -tags=unit
./internal/service -run 'RechargeBonus|Payment|Affiliate|Reward'` passes0.763s
(`payment-final-currency-green.log`), including the local fulfillment contract.

Combined system-settings PUT originally saved a cleared risk allowlist before
rejecting an invalid discount. Handler/store/cache roundtrip regression observed
that partial write (`admin-settings-partial-write-red.log`). The shared payment
cross-field validation now runs read-only before unrelated settings writes;
notice validation also precedes the no-tier-update shortcut. Focused service and
admin settings tests pass0.401s/0.053s (`admin-settings-partial-write-green.log`).
This prevents deterministic invalid-input partial writes, not arbitrary database
failures across separate existing configuration writes. Full final-tree tests
and independent review remain pending. Recharge page receives no further edits.

## Migration contract

The two upstream 241 migrations are remapped to 246 (bonus snapshot) and 247 (TypeSafe). 247 extends the local platform constraint, keeps KIRO, and does not reference the excluded Composite table. All 299 historical SQL files remain byte-identical to the pre-merge digest.

`TestUpstreamB8deMigrationPolicy` initially failed with 301 historical files versus expected299; after remapping, `go test -count=1 ./migrations` passed. The independently captured aggregate digest is recorded in the test.

`CI=true go test -count=1 -timeout=5m -tags=integration ./migrations` passed (exit0, UTC08:49:21–08:49:27; package runtime3.055s). `migration-upgrade.log` retains raw output. The test uses a unique disposable PostgreSQL18.1 container; it applies both SQL files twice, checks old bonus=0/non-nullability, pre-existing KIRO and all-null quota rows, all12 supported platforms, and rejects Composite. Full repository bootstrap/upgrade remains pending; this focused test is not represented as a full empty-database bootstrap.

## Payment call-chain review

`CreateOrder` reads settings, validates amount/user/provider, calculates the quote, then persists Amount/PayAmount/BonusAmount in one order transaction. Config parsing defaults to empty tiers; mode updates validate against stored tiers. Public, authenticated and admin DTOs expose the saved bonus. `doBalance` redeems saved Amount through the existing local lease/code mechanism; no new reward balance layer or expiry is introduced. Local `GrantFirstRechargeReward` remains the settlement path, fed by Amount minus BonusAmount. Refund continues to convert the credit fraction into the original real cash fraction.

Conflicts in payment config/response/handler were reviewed against both parents. Local ProviderKey, plan-seat/quota fields, visible-payment-method behavior, and removed mobile Alipay precreate option are retained. Existing `TestCheckoutContractsDoNotExposeAlipayMobilePrecreateDeepLink` is explicit evidence that the deleted option must not be resurrected by a formatting conflict.

New `TestRechargeBonusLocalFulfillmentContract` exercises real order creation and its persisted quote, a pure URL-building EasyPay provider (no network), local fulfillment/redeem/reward services, duplicate fulfillment and callbacks, changed campaign settings, below-threshold gifts, reward lifetime and proportional refunds. `TestRechargeBonusDoesNotDiscountSubscription` protects subscription checkout. These tests are authored but not yet behaviorally verified: the service package is blocked by unresolved shared-package conflicts (`account_stats_pricing_test.go:730`). This setup error is NOT a valid RED test. Red/green mutation verification, selected-currency discount re-quote regression, full payment/reward unit and repository integration remain pending.
