# Frontend integration — in progress

Security gate 2026-10-06: pnpm production audit exits1 and the existing exception
checker rejects source-map-js1.2.1 GHSA-68fv-2mgg-jv7q and Vue server-renderer3.5.26
GHSA-g2v6-rqmx-r4w6. Candidate and clean DEV_BASE read-only audits reproduce both;
candidate's upstream axios1.20 upgrade removes the additional baseline axios
advisories. xlsx retains only the pre-existing documented exceptions. Raw files
frontend-audit.json/frontend-audit-dev-base.json are retained. User decision
requested for minimal coherent Vue3.5.42/source-map-js1.2.2 upgrade, no new
exceptions or recharge-page edits; no dependency upgrade applied yet.

Latest `make test-frontend` passes08:14:56–08:16:26 UTC: lint, typecheck,
critical40 suites/512 tests, WebChat5 suites/75 tests (`frontend-ci-final.log`).
Local Node22 is not equivalent to the required future Node20 CI check.

2026-10-06 latest verification: full Vitest passed411 suites/3287 tests
(`frontend-candidate-full-tests-2.log`,93.16s); typecheck and lint exit0 in
their *-2.log runs; production build exit0 (`frontend-candidate-build-2.log`,
1118 modules,38.94s, includes i18n3 tests and vue-tsc). First build attempt
failed because the nested package script could not find pnpm, not a source
failure. Rerun uses this task's private corepack-pnpm shim. Existing Browserslist
and large-chunk warnings remain. No frontend edit after these checks.

User asked not to change the recharge page, then continued after scope question:
freeze its candidate and do not add fee-inclusive UI limit validation; backend
continues final-charge validation. This supersedes the earlier pending question
below; it is not authority to revert prior approved campaign integration.

Quota/API types include TypeSafe alongside all local providers including KIRO.
Settings keep their shared platform catalog, not the smaller upstream literal
list. The modal submits12 platform rows and preserves explicit zero vs null;
all-null persisted rows retain reset availability. Dashboard sorting keeps the
local provider order and appends TypeSafe. Platform colors retain local
styles/fallbacks with TypeSafe sky entries only; Composite is not restored.

Model-list customization remains display-only. Upstream wildcard request
allowlist additions and its restored DU test remain excluded; the local
groupsModelsList implementation and tests are retained. Account editing adds
the TypeSafe base URL without changing local KIRO/base-URL initialization.
Kiro/Fable/Mythos mapping tests coexist with the new Sonnet5.5 mapping test.

AmountInput retains local RMB labels and dark-theme classes while adding bonus
badges and quote second lines. Empty campaigns have no badges/second lines.
Full PaymentView/settings/order-snapshot integration is not yet resolved.

Focused runs (scratch logs under this task's `.superpowers/sdd/` directory):

- `frontend-platform-quotas.log`:2 files32 tests passed, modal and quota cell.
- `frontend-models-payment-focused.log`:1 failed/42 passed; upstream dashboard
  test classified Kimi as unknown, conflicting with the existing local order.
- `frontend-models-payment-focused-2.log`:4 files44 tests passed, AmountInput,
  model whitelist, local model-list state and dashboard. Unknown-provider tests
  now use a genuinely unknown identifier; separate test covers KIRO/Kimi/TypeSafe
  local order. Existing Vue router-link warnings appear in the raw output.

Latest follow-up: all frontend conflicts resolved. CC Switch user option1 keeps
bare import endpoints; usage queries deduplicate /v1 independently. Actual old
URL mutation fails3/18; restored result passes18. Catalog user option1 preserves
local-file default, adds remote option through the shared local generator, with
auth.json/linx2ai/bare root unchanged. Windows config catalog path uses ~/ while
the displayed save path remains Windows-native. Sonnet5.5/GPT6.1Sol add entries
without dropping Fable/Mythos. Legacy WS tabs and Composite stay excluded.

Settings quota tables submit all12 providers. Numeric OAuth scheduling UI refuses
empty values, preserves0/default1; risk allowlist load/edit/clear covered. The
numeric backend path is now enforced too (see accounts.md for RED/GREEN evidence).
Static TypeSafe Keys now show native instructions; auto/default-follow retain
their existing guide. CMD JSON quoting regression failed before String.raw fix.
Catalog key changes/unmount abort queries and suppress stale results.

`key-settings-green-3.log`:6 suites151 tests passed (including shared config and
catalog API); prior red logs prove catalog default/remote output, TypeSafe static
guide, CMD escaping, numeric-rate rejection. PaymentView38/source4 also passed
in ccswitch-payment-keys-green.log, whose Keys suite was still compile-blocked.
`account-async-red-2.log` reproduces wrong-account debounced save, old success/error
events and post-unmount Claude re-query. Candidate fences by component generation;
priority unmount still flushes once for its original account. No real API calls.
`account-async-green.log`:Priority9/Claude28/Amount5/Pricing38 all pass.
`frontend-candidate-typecheck.log` and `frontend-candidate-lint.log` exit0 on their
respective intermediate trees. Full frontend suite/build and final-tree reruns
remain mandatory. Existing Vue router-link/Browserslist warnings documented.

The first full candidate run completed with410 passing suites and one failed
suite: settings.authSourceDefaults still asserted11 platform rows instead of12.
Those three expectations now include TypeSafe while retaining all-null rows.
`payment-method-currency-red.log` additionally reproduced cross-currency method
availability using the currently selected method's quote. Each method now quotes
with its own currency precision; displayed fees/totals use that same precision.
`payment-method-currency-green-2.log`:PaymentView41 + settings defaults13 pass.
The initial green command could not start because pnpm was not in PATH; it is
not a test result. The rerun uses the installed corepack pnpm explicitly.

Payment limit policy remains a user gate: backend selects a provider using final
pay amount including fees, whereas recharge UI availability uses pre-fee amount.
Asked whether to align UI validation with backend, without changing collection,
credited balance or refunds. No fee-inclusive availability change applied yet.
Latest candidate typecheck and lint both pass in their *-2.log runs; full tests
and production build still need rerunning after the remaining integration work.
