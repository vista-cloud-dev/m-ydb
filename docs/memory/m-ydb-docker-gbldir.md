---
name: m-ydb-docker-gbldir
description: Fixed (2026-06-12) — the docker transport established no $ydb_gbldir, so all global-accessing M faulted %YDB-E-ZGBLDIRUNDEF; buildTrapped now SET $ZGBLDIR at runtime. Live-proven on vehu (v pkg --engine ydb full lifecycle). Corrects the M0a "YDB driver-path" record (was raw-M).
metadata:
  type: project
---

# m-ydb docker transport: missing global directory (FIXED 2026-06-12)

## The bug
The **docker** transport ran user M with **no global directory established**, so
any global access faulted **`%YDB-E-ZGBLDIRUNDEF`** — while routine load/run
worked fine. Root cause, two cooperating facts in `internal/transport`:
- `exec.go execEnv()` returns **`nil` for docker** ("the container's own env
  applies") — no `ydb_gbldir`.
- `buildTrapped()` layered only `$ZROUTINES` at runtime, never the gbldir.
- A FOIA container (`worldvista/vehu`) sets its VistA env **solely via
  `/home/vehu/etc/env`**, which `docker exec` does **not** source, so the
  container's default exec env has no `ydb_gbldir`/`gtmgbldir`.

Net: the entire KIDS lifecycle (which touches `^XTMP`/`^XPD`/FileMan globals)
failed. `v pkg install/verify/uninstall --engine ydb --transport docker` against
vehu all died at `run EN^ZVPKGINS`; ZZSKEL failed identically (so **not** a v-pkg
bug). **This means the earlier-recorded "M0a YDB driver-path proven on vehu"
(v-pkg/m-stdlib trackers) was actually the raw-M-over-`docker exec` path
(sourcing the env file), NOT `v pkg … --engine ydb`.** Surfaced by m-stdlib VSL
T0b.2 (the MSL KIDS base needs the real driver path).

## The fix
`buildTrapped` now emits **`SET $ZGBLDIR=<cfg.GblDir>`** (when `GblDir` is
configured), right before the existing `SET $ZROUTINES=…_$ZROUTINES`. This
**mirrors the routine-path layering**: establish the resource at runtime inside
the `%XCMD` command rather than via process env — so it works over `docker exec`
with no `-e` plumbing, and is a harmless re-assert on local (env already sets
`ydb_gbldir`) / remote (EnvFile sources it). Only ~8 lines in `exec.go`.

Why runtime-SET (not `docker exec -e`): same philosophy as `execEnv()`
deliberately omitting `ydb_routines` — keep the engine's default search path
intact so `%XCMD` (a system percent-routine) still links; layer the configured
paths on at runtime. `$ZGBLDIR` is the read-write ISV for the global directory.

## Validation
- Unit: `TestExecTrapped_DockerSetsZGblDir` (TDD red→green); `go test -race
  ./...`, `go vet`, `gofmt`, `make test-it` (m-test-engine r2.07) all green.
  (The integration tests don't set `GblDir`, so the bare-engine path is
  unchanged — no SET added there.)
- **Live on vehu (docker):** `m-ydb exec eval 'W $D(^XPD(9.7,0))' --transport
  docker` → global data (was ZGBLDIRUNDEF); `v pkg install/verify/uninstall
  /tmp/ZZSKEL.kids --engine ydb --transport docker` → install #9.7 **status 3**,
  verify installed:true, uninstall reversible (post-verify installed:false).
  Env: `M_YDB_CONTAINER=vehu M_YDB_DIST=/home/vehu/lib/gtm
  M_YDB_GBLDIR=/home/vehu/g/vehu.gld M_YDB_ROUTINES=<sourced gtmroutines>`.

## Consequence
The YDB driver path (`v pkg … --engine ydb --transport docker`) is now genuinely
real, not just raw-M. **Unblocks m-stdlib VSL T0b.2** (branch
`t0b2-msl-kids-base`): rebuild done; resume `scripts/kids-test-in-place.sh ydb`
there. See [[m-ydb-driver-m0]] (the M3 exec axis this corrects).

## Follow-on: docker now login-shell sourced — zero gbldir/routines config (2026-06-17)
The 2026-06-12 fix worked only when the caller **passed** the paths explicitly
(the live proof used `M_YDB_GBLDIR=/home/vehu/g/vehu.gld
M_YDB_ROUTINES=<sourced gtmroutines>`). `m vista exec --engine ydb --transport
docker` (m-cli) passes **neither** — `config.Resolve` fills `GblDir`/`Routines`
from the *host* `gtmgbldir`/`gtmroutines`, which are empty when targeting a
container — so `SET $ZGBLDIR` was skipped and exec faulted `ZGBLDIRUNDEF` again
(returned `ok:true, stdout:""` up the stack, looking like a silent no-op).

**Fix:** `Session.wrap()` now wraps the docker argv in **`docker exec -i <c> bash
-lc <shJoin(argv)>`** — a *login shell* that sources the container's own
`gtmgbldir`/`gtmroutines`/`gtm_dist`, so the engine env is established with **no
explicit flags**. This mirrors m-cli's `DockerRunner` (which already drives these
same containers via `bash -lc` — see m-cli's docker-routines fallback), unifying
the two docker paths. The runtime `SET $ZGBLDIR`/`$ZROUTINES` injection stays as
an *override* layer for an explicit `--gbldir`/`--routines` (or a staged routine
dir) on top of what the login shell resolves. Requires `bash` in the container
(present in vehu + m-test-engine).

**Validation (2026-06-17):** unit `TestDocker_WrapsArgv`/`TestDocker_Util_Argv`
updated to the `bash -lc` form (TDD); `go test -race ./...` + `golangci-lint`
green; `make test-it` (m-test-engine) green. **Live on vehu (zero path flags):**
`m-ydb exec eval 'W $ZV' --transport docker` → `GT.M V7.0-005 …`;
`'W $P($G(^DIC(200,0)),"^",1)'` → `NEW PERSON`; `lifecycle status` →
running/healthy/r2.02. Unblocks `m vista exec`/`status` in m-cli (and the vdocs
SKL S2.2 live-DD seam). See [[engine-access-through-driver-stack]].

## Follow-on: source-store (load/sync) path now container-aware too (2026-06-30)
The 2026-06-17 login-shell fix made the **exec** path resolve the container's env
with zero flags — but the **source axis** (`exec load`, all of `sync`) takes a
SEPARATE path: `config.SourceStore` reads the routine *write* dirs from
`ParseRoutinesDirs($ydb_routines)` resolved off the **host** env, never the
container. Against a real VistA image (`vehu`) the host has no `ydb_routines`, so
`SourceStore` returned **`no routine source directory: set --routines or
$ydb_routines`** and `exec load` staged nothing — which is exactly how
**`v pkg install` surfaced as `stage ZVPKGRD: driver loaded no routine`** (its
`runMScript` refuses up front when `cl.Load` reports zero loaded — lifecycle.go
explicitly anticipates "no routine source directory configured → loads nothing").
This was the **OPEN ZVPKGRD install snag** the v-rpc-tap reaper-live-proof memory
flagged as a P4 blocker — NOT a v-pkg bug, NOT a regression of T0a.3 (that proof
passed `M_YDB_ROUTINES=<sourced gtmroutines>` explicitly; nothing sets it now).

**Fix:** new `Session.ContainerRoutines(ctx)` reads the engine's own
`$ZROUTINES` (docker-only; with no `--routines` set, `Exec` layers no override so
it returns the login-shell value, whose `object*(src …)` form `ParseRoutinesDirs`
already parses). `config.SourceStore` calls it as a **fallback only when host
`Routines` is empty + docker + a container is set** — so explicit `--routines`
and the bare m-test-engine path are untouched (no regression; auto-discovery is
strictly additive, filling a case that previously errored). vehu's
`$ZROUTINES` → source dirs `/home/vehu/p` (primary write target) `/home/vehu/s`
`/home/vehu/r`.

**Validation (2026-06-30):** unit `TestContainerRoutines_DockerReadsZRoutines` /
`_NonDockerIsNoop` (TDD red→green, fake runner); `gofmt`/`go vet`/`golangci-lint`/
`go test ./...` green; `make test-it` (m-test-engine) green. **Live, zero path
flags:** `m-ydb exec load ZVPROBE.m --transport docker` → `loaded:[ZVPROBE.m]
compiled:true` on **both** vehu and m-test-engine (was BAD_CONFIG); `v pkg install
vslrtap.kids --engine ydb --transport docker --dry-run` → 3 routines NEW, exit 0
(was the ZVPKGRD refusal). GOTCHA: `sync rm` removes only the `.m` **source**, not
the compiled `.o` — a staged scratch routine still links from its object after rm
(host can't delete the `.o`: `rm` is deny-gated, even via an M PIPE device). The
real `v pkg install` (not dry-run) now reaches KIDS filing and hits a SEPARATE
v-pkg issue (`stage-incomplete: staged 794 of 345 nodes`) — beyond the ZVPKGRD
seam this fix closes. See [[m-ydb-driver-m0]], [[engine-access-through-driver-stack]].
