---
type: "architecture"
date: "2026-07-15T22:39:12.505997+00:00"
question: "How do expiring affiliate and check-in rewards flow from grant to billing and UI?"
contributor: "graphify"
outcome: "useful"
---

# Q: How do expiring affiliate and check-in rewards flow from grant to billing and UI?

## Answer

Rewards are granted into user_reward_credits with users.balance updated atomically; usage billing selects one complete layer in priority order; batch holds persist allocations; expiry is enforced lazily and by a worker; profile summaries and reward detail APIs feed the header, dashboard, and affiliate page.

## Outcome

- Signal: useful