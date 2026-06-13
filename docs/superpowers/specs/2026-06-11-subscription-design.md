# Subscription Plans and Seven-Day Quota Design

## Goal

Implement LINX2 subscription plans modeled after the provided Fusecode pricing screenshot, while preserving the existing recharge balance system. Users should see and use two balances:

- Subscription balance: the remaining quota in the current seven-day subscription window.
- Recharge balance: the existing wallet balance from top-ups.

API usage must consume subscription quota first. If no active subscription exists, or the subscription quota is insufficient, usage falls back to the recharge balance.

## Plans

Create four monthly subscription plans under one shared subscription group:

| Plan | Price | Seven-day quota | Positioning |
| --- | ---: | ---: | --- |
| Basic monthly | ¥179 / month | $50 / 7 days | Lightweight trials and low-frequency individual development. |
| Plus monthly | ¥399 / month | $110 / 7 days | Everyday development and steady personal usage. |
| Pro monthly | ¥799 / month | $260 / 7 days | Main development workflows and higher-frequency usage. |
| Max monthly | ¥1599 / month | $550 / 7 days | Heavy usage, parallel projects, and higher concurrency needs. |

The copy should be rephrased for LINX2 and should not directly copy the source screenshot. Existing UI color language should be preserved: dark Linear-style surfaces, thin borders, and orange accent hierarchy.

## Data Model

Use a dual-ledger model:

- Keep `users.balance` as the recharge balance.
- Add plan-level seven-day quota fields to subscription plan data so one subscription group can support multiple paid tiers.
- Store the purchased plan and quota snapshot on `user_subscriptions`, including the active `plan_id`, seven-day limit, window start, and usage.
- Existing active users must continue working; missing plan snapshots can be treated as legacy subscriptions that fall back to group-level limits until migrated or renewed.

The quota snapshot prevents historical subscriptions from changing when an admin later edits plan price or quota.

## Purchase, Renewal, and Upgrade Semantics

All four plans attach to the same subscription group.

When a user buys a plan for the first time:

- Create an active subscription for that group.
- Set the subscription plan snapshot from the purchased plan.
- Start the seven-day window at the activation time.
- Set the subscription expiry from activation time plus the plan validity period.

When a user already has an active subscription in the same group and buys any plan:

- Switch the subscription to the newly purchased plan immediately.
- Reset the current seven-day quota window at the switch time to avoid mixing old and new plan allowances.
- Extend the subscription expiry from the current expiry time by the purchased validity period.
- If the existing subscription is expired, restart from the purchase completion time.

The subscription should only change after payment fulfillment completes. Failed, pending, or cancelled orders must not affect the current subscription.

## Quota Window Behavior

The seven-day quota is implemented as a rolling entitlement window, not as money added to `users.balance`.

- Window start is the exact activation or plan-switch time, not the start of the calendar day.
- Window reset occurs when `now >= window_start + 7*24h`.
- Reset clears subscription usage and sets the new window start to the reset time.
- Frontend displays this as the subscription balance: remaining quota, total quota, and next reset time.

Daily and monthly subscription limits are not part of the new public pricing requirement. Existing daily/monthly fields can remain for compatibility, but the new plans are driven by seven-day quota.

## Billing Flow

For API requests using a subscription group:

1. Load the active subscription and its plan quota snapshot.
2. If the seven-day window expired, reset subscription usage before evaluating quota.
3. If subscription remaining quota can cover the request, use subscription billing.
4. If subscription quota is absent or insufficient, fall back to the existing recharge-balance billing path.
5. If neither subscription quota nor recharge balance can cover usage, reject the request with an insufficient quota/balance error.
6. After the upstream response, record actual usage against whichever ledger was selected.

Final usage updates should be atomic enough to avoid substantial overuse under concurrent requests.

## Frontend

### Home Pricing

Replace the current model-pricing table in `/home` with four subscription plan cards. The cards should match the current LINX2 landing page style:

- Dark panels and thin Linear-style borders.
- Orange accent hierarchy consistent with recent homepage work.
- Clear pricing, seven-day quota, concise benefits, and a CTA.

### Purchase Page

Keep `/purchase` and the subscription tab. Show the same four plans using backend-provided plan data. Plan cards should show:

- Plan name.
- Monthly price.
- Seven-day quota.
- Rephrased benefits.
- Renewal state if the user already has a subscription.

### User Balance Display

Show two balances in the user UI:

- Subscription balance: current seven-day remaining quota for the active subscription, with next reset time.
- Recharge balance: existing wallet balance from top-ups.

The dashboard and subscription page should make clear that subscription quota is consumed before recharge balance.

### Subscription Page

Update `/subscriptions` to show:

- Current active plan.
- Seven-day quota usage and remaining amount.
- Next reset time.
- Subscription expiry.
- Renewal action.

## Admin

Admin payment plan management should support the plan-level seven-day quota field. Seed or migrate the default Basic, Plus, Pro, and Max plans as for-sale plans on the shared subscription group.

Admins can later edit plan price, copy, sort order, and quota. Existing users keep their purchased quota snapshot until they buy/renew/switch again.

## Error Handling and Idempotency

- Reuse existing payment order states and fulfillment idempotency.
- Duplicate payment callbacks must not extend or switch the same subscription twice.
- If plan or group configuration becomes unavailable after order creation, fulfillment should fail safely and keep the current subscription unchanged.
- If recharge balance fallback is used, existing balance insufficient handling applies.

## Testing

Cover these cases:

- Public and purchase pages render Basic, Plus, Pro, and Max with the expected prices and seven-day quotas.
- Payment success creates a subscription with the purchased plan quota snapshot.
- Buying a different plan while active switches the plan immediately, resets the seven-day window, and extends expiry.
- Seven-day quota resets from the activation or switch timestamp, not calendar midnight.
- API billing consumes subscription quota first.
- When subscription quota is insufficient, billing falls back to recharge balance.
- When both ledgers are insufficient, the request is rejected.
- Dashboard/subscription UI displays subscription balance and recharge balance separately.
