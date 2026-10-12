# Frontend independent-review finding repairs

Status: two fixes implemented; dedicated RED/GREEN and final full/CI/build verification passed. Author is not the fresh independent reviewer; root will arrange a new complete review. This author performed no stage, commit, push, dependency change or production access.

## Coordinates and scope

Working tree: `/home/alvin/tokenstation3/.worktrees/upstream-sub2api-20261012-9591`.
Starting HEAD `8a181834da9cf656951ffb59e8f2c47150f97b43` (documentation); underlying merged production code `896ca30d6b809f72ddb20a455e2410508792183d`; previously verified docs `75cada11fd365bbeccf5af45609b99e815e2e1d2`. Fix delta is exactly the four frontend source/spec paths below. Root owns concurrent backend edits; those are outside this evidence and were not modified here.

Read the two accepted reviewer findings, the affected form/save and Ops load/save/watch call chains, and `Account.GetAPIProtocol` in backend/internal/service/account.go. That backend helper trims stored protocol and defaults unsupported/missing protocol to adaptive for by-model profiles, CC for by-inbound profiles. Receiving-code-review/systematic-debugging/TDD instructions applied; tests preceded production fixes.

## Finding 1: unrelated account edits rewrote implicit routing

The edit form assigned absent/invalid protocol to CC, then save serialized CC and removed stored per-protocol endpoint map. Corrected initialization to trim stored protocol and choose the same profile-driven fallback as backend; explicit supported adaptive/CC/Anthropic/Responses remain effective, unsupported Responses also takes the profile fallback, and by-inbound providers keep CC. Save now preserves custom chat/Anthropic/Responses URLs and protocol rules when only name changes; no backend or save API schema changed.

Added 11 cases: three absent/empty/unknown Command Code name-only edits preserve adaptive/custom endpoint map; five explicit protocol cases including whitespace preserve normalized overrides; Cline missing protocol stays CC; generic by-model unsupported/unknown native protocol preserves custom supported endpoint map. Expectations are literal payload contracts, not calls to the production default helper.

RED final before source fix: 6 failures / 76 passes, correctly showing CC payload, erased endpoint map, and whitespace override loss. GREEN after minimal source fix: all 82 tests passed. Earlier partial RED (before adding the two generic profile cases) had 4 failures / 76 passes, retained in a separate log.

## Finding 2: obsolete Ops loads unlocked stale saves

A close/reopen starts A and B loads; without ownership, A could update configuration, set settingsLoaded, clear B loading or report obsolete errors. Added a monotonically increasing load request ID on each load, close and unmount. Success state writes, catch notification and finally loading clear require the matching ID; closing clears save readiness/loading. The four child read APIs still use the same Promise.all and save child APIs/payload behavior remain unchanged.

Added six cases: old A success before/after B failure cannot enable save or invoke any update API; obsolete A failure cannot end B pending load or notify; A success after successful B cannot overwrite B's persisted runtime/threshold payload; closing and unmount discard pending error notifications. RED before source fix: all six new cases failed while three existing cases passed. GREEN after fix: all nine passed.

## Source coordinates (SHA256 of file bytes)

| Path | Before fix at starting HEAD | Validated candidate |
| --- | --- | --- |
| `frontend/src/components/account/EditAccountModal.vue` | `ed187188b28696e92f72cbe41a71827860306228723aba20ff3da5a31227d4ee` | `e33748805313003fd3a8c8462866e2a0b52b896d92fc19d56360207839d9d929` |
| `frontend/src/components/account/__tests__/EditAccountModal.spec.ts` | `f819642ef986dcea3d2f5f31ac4c2e1f43dfb11e0d979b64a23cbf510300809c` | `3330078c2262e116ec15b34be2c5885045b24b8d2c11ad61c8aca924011613c9` |
| `frontend/src/views/admin/ops/components/OpsSettingsDialog.vue` | `242e1eea6adb50c46efa3d4794741f4f770249898fd4aca85e1f6ac11f6c15b1` | `20bb6d2891e440809026efedd04eff8004ab9d5dd2516aba201a23e4d1ef85f1` |
| `frontend/src/views/admin/ops/components/__tests__/OpsSettingsDialog.loading.spec.ts` | `77648608489eb705bd280032ecfbd2e32aa57f8bb46812f7c31f4fb80d358bf5` | `afb1ac531927cbfd240e0eae0548012ec050f442067db253742510bc631c75f6` |

Full tracked frontend file manifest: `frontend-review-fix-source-sha256.txt` (1057 paths), SHA256 `23b11f821bf4692f355c703c983db9c5caa7be0df365b875e028e67b17dc0316`. Created after fixes and before final full suite; all 1057 tracked file byte hashes matched after final tests and production build. This identifies the working-tree code without claiming a nonexistent final commit SHA.

## Dedicated test commands and observed results

Node `v22.22.2`; pnpm `9.15.9`. All times below are Asia/Shanghai, UTC+08:00. Commands ran from frontend/ with scratch logs at ../.superpowers/20261012-sync/.

| Exact command | Result / completion | Log |
| --- | --- | --- |
| `node /home/alvin/.cache/node/corepack/v1/pnpm/9.15.9/bin/pnpm.cjs exec vitest run --maxWorkers=2 --minWorkers=2 src/components/account/__tests__/EditAccountModal.spec.ts` before production fix | exit 1; 6 failed / 76 passed (82 total); 20:17:53 | `frontend-review-edit-red-final.log` |
| Same exact command after production fix | exit 0; 82 passed; 20:19:45 | `frontend-review-edit-green.log` |
| `node /home/alvin/.cache/node/corepack/v1/pnpm/9.15.9/bin/pnpm.cjs exec vitest run --maxWorkers=2 --minWorkers=2 src/views/admin/ops/components/__tests__/OpsSettingsDialog.loading.spec.ts` before production fix | exit 1; 6 failed / 3 passed; 20:19:17 | `frontend-review-ops-red.log` |
| Same exact command after production fix | exit 0; 9 passed; 20:20:39 | `frontend-review-ops-green.log` |

No package/lock changes, so the existing audit exception check is not rerun, as instructed. Final validation used full Vitest, exact make test-frontend (lint/typecheck/critical/WebChat) and make build-frontend serially; all exited 0. Vite output is the shared ignored backend/internal/web/dist; ownership communicated to root before any build.


## Final validation and handoff

Root committed the reviewed repair delta as `60fb738497b38700e450f33194822c57d4d4917a` while validation was running. Frontend files at that root-created HEAD were clean and byte-identical to this author’s pre-test manifest; no source change occurred after the successful dedicated/full/CI runs. This is a frontend-domain validation statement, not a backend or remote-CI completion claim.

| Exact command / environment | Result / completion | Raw log |
| --- | --- | --- |
| From frontend/: `node /home/alvin/.cache/node/corepack/v1/pnpm/9.15.9/bin/pnpm.cjs exec vitest run --maxWorkers=2 --minWorkers=2` | exit 0; 444 files / 3544 tests; `2026-10-12T20:25:09+08:00` | `frontend-review-full-tests.log` |
| From worktree root: `PATH="/home/alvin/tokenstation3/.worktrees/upstream-sub2api-20261012-9591/.superpowers/20261012-sync/bin:$PATH" VITEST_MAX_THREADS=2 VITEST_MIN_THREADS=2 VITEST_MAX_FORKS=2 VITEST_MIN_FORKS=2 make test-frontend` | exit 0; lint/typecheck, critical 48 files / 771 tests, WebChat 5 files / 75 tests; `2026-10-12T20:27:23+08:00` | `frontend-review-ci.log` |
| From worktree root: `PATH="/home/alvin/tokenstation3/.worktrees/upstream-sub2api-20261012-9591/.superpowers/20261012-sync/bin:$PATH" VITEST_MAX_THREADS=2 VITEST_MIN_THREADS=2 VITEST_MAX_FORKS=2 VITEST_MIN_FORKS=2 make build-frontend` | exit 0; i18n 3 tests, vue-tsc build, Vite production build; `2026-10-12T20:30:10+08:00` | `frontend-review-build.log` |
| `git diff --check 8a181834da9cf656951ffb59e8f2c47150f97b43 HEAD -- frontend` + all tracked frontend byte-hash comparison | clean; zero changed paths relative to test manifest | final tool output |

Scratch bin/pnpm simply executes the pinned pnpm9 cjs file. CI runs Node 20; local runtime was Node 22.22.2, so remote required checks remain root’s later exact-tip gate. Build retains existing Browserslist-age and >500kB chunk warnings, with no claimed new waiver. Its configured output is ignored backend/internal/web/dist; no tracked backend files were edited and output ownership is released at handoff. Dependencies unchanged: no new audit run or exception changes.

Root/review_frontend was informed of changed file scope and remained read-only; root will arrange a fresh complete frontend re-review. No unresolved author findings or user decisions. Review-fix source manifest SHA256 remains `23b11f821bf4692f355c703c983db9c5caa7be0df365b875e028e67b17dc0316`.
