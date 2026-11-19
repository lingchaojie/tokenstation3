# GPT-6.2 security gate implementation plan

**Goal:** Repair the existing dependency findings and publish the approved GPT-6.2 change through dev and release with passing checks.
**Architecture:** Targeted dependency and toolchain upgrades; retain the current gateway and billing implementation.
**Tech Stack:** pnpm 9, Vue 3 / Vue I18n 11, Node 24 CI/build, Go 1.27.4.
**Spec:** `docs/superpowers/specs/2026-11-19-gpt62-security-design.md`

## Global Constraints

- No new audit exemptions or suppression of reachable vulnerabilities.
- Preserve unrelated dependencies, model behavior, release history and user edits in other worktrees.
- User authorized fixes and non-force pushes to dev/release; no production server changes.
- Execute inline in the existing isolated worktree; one fresh final reviewer.

## Review Focus

Check runtime message compilation and interpolation under CSP, locale fallback/switching, paired compiler/runtime versions, all Node and Go build paths, unexpected transitive dependency changes, and unchanged security exception policy. Check Go compression/HTTP compatibility and whether production builds actually use the patched toolchain.

## Task 1: Repair dependencies and toolchain gates

**Interfaces:** package/lock and Go module files supply the versions consumed by CI, Docker builds and runtime. All consumers must agree.

- [x] Preserve the existing failing pnpm audit/checker and govulncheck outputs as RED evidence. Expected: the known frontend/backend security gates fail before repair.
- [x] Upgrade vue-i18n and compiler to 11.4.22, follow-redirects to 1.16.2 and decode-uri-component to 0.5.1 using narrow overrides; regenerate pnpm lock without broad upgrades. Align CI/release Node with existing Docker Node 24 and declare the minimum supported Node version. Inspect v10/v11 migration requirements and verify existing Composition API use.
- [x] Upgrade Go 1.27.4, Brotli 1.2.7, compress 1.20.2, x/net 0.62.0 and consistent CI/Docker pins; inspect required transitive changes. Also upgrade x/crypto to stable 0.59.0 for GO-2026-5932 found by the extended scan, retaining its required companion modules.
- [x] Run frozen install, pnpm audit/exception checker and govulncheck. Expected: no new non-exempt high/critical frontend findings and no reachable Go vulnerabilities.
- [x] Run complete frontend tests, lint, typecheck and production build; Go generation consistency, unit/integration suites, lint and embedded production build. Expected: all pass. Preserve logs outside tracked source.
- [x] Record evidence and commit the targeted repairs. Task completion command: `git diff --check` after successful gates, whose detailed commands/results are archived with the evidence.

## Task 2: Review and publish

**Interfaces:** Task 1 validated tree becomes dev, then a release merge must preserve that tree and existing release ancestry.

- [x] Fresh independent review of the full dependency repair diff; fix Important/Critical findings with regression evidence and rerun affected checks.
- [ ] Push updated dev without force and wait for exact-head CI/Security Scan success. Expected: every required job passes.
- [ ] Merge into the prepared release branch, verify tree equality with validated dev and release ancestry, push without force. Expected: release CI/Security Scan and publication complete successfully.
- [ ] Record final branch SHAs, release tag and workflow URLs. Task completion command: `git status --short` with clean worktree and verified remote heads.
