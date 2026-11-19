# Dependency security repair before publication

The user requested security repairs before the release push on 2026-11-19. Dev previously received the model/Header implementation at `5fef6276f`; release had not been pushed. [Security Scan 44474931397](https://github.com/lingchaojie/tokenstation3/actions/runs/44474931397) supplied the failing baseline.

## Findings and targeted repairs

| Finding | Repair |
| --- | --- |
| GHSA-83rm-6c8r-g728, Vue I18n srcdoc sanitization | Vue I18n and message compiler 9.14.5 → 11.4.22; matching Intlify dependencies |
| GHSA-qr28-fpjm-wq7f, URI decoder complexity | decode-uri-component override 0.5.1 |
| GHSA-mrwp-f3q8-3g9p, redirect headers | follow-redirects override 1.16.2 |
| GO-2026-6888, Brotli | 1.2.1 → 1.2.7 |
| GO-2026-6865, compression | klauspost/compress 1.19.1 → 1.20.2 |
| GO-2026-6860, x/net | 0.60.0 → 0.62.0 |
| GO-2026-6859, net/http | Go 1.27.3 → 1.27.4 in module, Docker and CI assertions |
| GO-2026-5932, unimported OpenPGP package | x/crypto 0.57.0 → stable 0.59.0, also clearing the module-only record |

The Go module upgrades require compatible x/mod 0.42.0, x/sync 0.25.0, x/sys 0.50.0, x/term 0.48.0, x/text 0.44.0 and x/tools 0.52.0. Other selected module versions are retained. Existing checksum rows are preserved; extra old checksum rows do not select packages for builds.

Vue I18n v9/v10 have no patched release according to the [official advisory](https://github.com/advisories/GHSA-83rm-6c8r-g728). Version 11.4.22 requires Node >=22. CI, security and release frontend jobs now use Node 24, matching the existing deployment Docker build; package engines declare >=22. The app uses Composition API and none of the removed tc/$tc APIs. The runtime alias and JIT configuration remain compatible.

## Verification

- **RED:** exact baseline and feature package/lock/exception/checker files were identical. Audits of both trees failed the exception checker on the same five package records from three advisories.
- **Regression RED → GREEN:** the srcdoc test retains the dangerous DOM attribute with the old v9 compiler-enabled build and neutralizes it with the new production runtime. The new runtime tests also verify real lazy-loaded English/Chinese messages, interpolation, locale persistence and title updates, invalid locale handling, fallback, linked messages, plurals and CSP blocking of Function construction.
- **Frontend GREEN:** Node 24.22.3 / pnpm 9.15.9 frozen install, 445 test files / 3566 tests, ESLint, TypeScript and production build. Updated CI critical target: 50 files / 780 tests. Query-string encoding/decoding smoke check passed with the patched transitive decoder. Actionlint passed for all three changed workflows.
- **Audit GREEN:** production audit plus the unchanged exception checker passes. Two existing high-severity xlsx advisories remain covered by the existing valid exceptions; no new exception or suppression was added.
- **Go security GREEN:** govulncheck 1.10.0 built with Go 1.27.4 reports `No vulnerabilities found.` All selected modules verify. The local system Go launcher returned older release tags in GO111MODULE=off mode; invoking the actual Go 1.27.4 bin directory on PATH fixes package loading and matches CI setup-go behavior.
- **Go generation GREEN:** Ent/Wire generation leaves tracked generated files unchanged with the final dependency graph.
- **Review fix GREEN:** independent review found the root Dockerfile's old Go pin. Actual toolchain checks reproduce its module-loading failure before the fix and pass for all three Docker paths afterward with `GOTOOLCHAIN=local`. All four affected Docker deployment configuration test scripts pass. See [security-review.md](security-review.md).
- **Backend tests GREEN:** `GOMAXPROCS=3 go test -p 2 -timeout=20m -tags=unit ./...` and `go test -p 1 -timeout=20m -tags=integration ./...` pass; service durations 271.801s and 167.970s. Local Docker supplies PostgreSQL/Redis for integration checks.
- **Backend lint GREEN:** golangci-lint 2.14.0 built with Go 1.27.4, concurrency 1, reports 0 issues.
- **Embedded build GREEN:** `GOMAXPROCS=3 CGO_ENABLED=0 go build -p 1 -tags=embed -trimpath` succeeds. Build info confirms Go 1.27.4 and the selected patched Brotli/compress/x/net/x/crypto modules. Binary SHA-256: `03bd472fa3d6c6a2c83ecf33eeee55a1d5fe8a945e23870a7debe08de8dd1170`. The chat interruption ended the first build process; only that unfinished build was resumed.
- **Docker source build limit:** local Docker Hub access timed out while querying the manifest. The [official image catalog](https://raw.githubusercontent.com/docker-library/official-images/master/library/golang) confirms `1.27.4-alpine` for amd64/arm64 and other architectures. No source Docker build is claimed here; the release workflow builds and publishes its configured artifacts.

No production credentials, provider requests, server changes or model/billing implementation changes are involved in this repair.
