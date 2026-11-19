# Independent security repair review

The fresh reviewer inspected the complete dependency repair range `5fef6276f..5e04f0495`, its spec/plan, lockfiles, build paths, exception policy and validation evidence. No Critical or Minor findings; the declined-to-judge list was empty. One Important finding required a fix before publication.

## Fixed: root source Docker build used the old Go version

`Dockerfile` still selected Go 1.27.3 after the module floor became 1.27.4. This path is consumed by `deploy/build_image.sh` and `deploy/docker-compose.dev.yml`, and official Go images disable automatic toolchain upgrades. The reviewer and implementer independently reproduced the failure with the old toolchain and `GOTOOLCHAIN=local`.

The root image now selects `golang:1.27.4-alpine`, matching the other two Dockerfiles. The development guide's version instructions were updated to list the current module floor and all three Docker paths.

**RED → GREEN:** a check reads each Dockerfile's selected Go version and invokes that actual installed toolchain with `GOTOOLCHAIN=local` against `backend/go.mod`. Before the fix, the root path fails `go.mod requires go >= 1.27.4`; afterward all three paths successfully load the module. The existing Docker Compose security, gateway environment, simple-mode environment and runtime-resource test scripts all pass, as does `git diff --check`.

The fix changes the root image pin and documentation only. Application code and dependency versions remain exactly those covered by the complete passing application suites, security scans and embedded build. CI on the final published commit provides the final integration gate. No second review was dispatched; the single review's sole finding is covered by the failing-then-passing check.
