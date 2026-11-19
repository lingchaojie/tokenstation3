# Independent GPT-6.2 review

Reviewed range: `b3bf9419b372af1d3e0a3948e93fcec5ba103d25..833c1abcd185cd1d1fbb90f3a799eebe7044dbae`.
One fresh reviewer inspected the complete branch and reproduced the findings using temporary Go overlays and fixture upstreams, without modifying the checkout or contacting a provider.

The reviewer verified byte equality of both Codex descriptors against upstream `2af3f1e15` and parsed equality of every pre-existing pricing object. There were no Critical or Minor findings. Four Important findings were accepted for one consolidated fix pass:

| Finding | Reproduction | Required correction |
|---|---|---|
| Unknown 6.2 names inherit default pricing | Loaded catalog prices `gpt-6.2-foo`, `gpt-6.2-sol-preview`, `gpt-6.2-astra-law` at GPT-5.4 rates | Honor explicit catalog entries, then reject unknown 6.2 spellings before fuzzy/default fallback |
| Disabled reasoning is silently removed | Native custom API mapping `public` to either new model forwards `reasoning.effort=none`; WS-to-HTTP preparation also removes it | Validate the actual mapped/compact model before lossy cleanup and reject locally |
| Effort suffix is lost | Native Responses and Chat Completions OAuth calls for either `-max` alias send the base model without its effort | Preserve recognized effort aliases before canonicalization, with explicit fields taking precedence |
| API aliases advertise Responses Lite | Custom API manifest aliases inherit `use_responses_lite=true` | Canonicalize the mapped 6.2 target in both API capability suppression lookups |

Permanent regression coverage is in `backend/internal/service/openai_gpt62_review_test.go`. The initial `GPT62Review` run exited 1 on all four behaviors before implementation. It also covers compact-specific mappings/fallbacks, passthrough qualification, inherited WS models, explicit operator zero pricing, provider-prefixed aliases and explicit-effort precedence. Final correction and suite results are recorded in `validation.md`.

Following finding 3 through the related paths also reproduced two cases in permanent tests before correction: native usage metadata inferred `max` from an arbitrary `public-max` alias despite no outbound effort; Messages mappings to `-max` sent `medium`, and an exact `-max` to `-high` mapping was lost during early model normalization. Usage extraction now uses recognized aliases of the actual upstream model. Both Messages conversion paths preserve the original exact mapping and transfer its selected effort, while explicit request effort retains precedence.

The reviewer declined to judge actual production entitlements/deployment readiness because the review was offline. General 30m/12h cache behavior and older-model compatibility were outside the approved scope. These are review limits, not deferred defects. No further independent review was requested after the fix pass.
