# m-ydb implementation tracker (D2)

Per-repo tracker — the step-2 target for m-ydb driver sessions (org Increment
Protocol). Update the active row here, in this repo, every increment. The shared
`docs/m-engine-drivers/driver-implementation-plan.md` §4 is the coordinator's
cross-repo roll-up, synced at milestone boundaries — do not edit it from a driver
spike. Status: ☐ todo · ◐ in progress · ☑ done.

Pinned: `m-driver-sdk v0.2.0`. Branch: `m-ydb-driver`. Transports: local·docker·**remote (SSH)**.

| M | Axis | Status | Notes |
|---|---|---|---|
| M0 | scaffold + SDK seam + `meta` | ☑ | honest caps golden |
| M1 | lifecycle + health + doctor | ☑ | daemonless (gde/mupip/rundown); real r2.07 |
| M2 | sync (8 verbs) | ☑ | filesystem-native over $ydb_routines; read+write; real r2.07 |
| M3 | exec (load/run/eval/abort) + engineError | ☑ | $ETRAP→$ZSTATUS; $ZROUTINES- **and $ZGBLDIR-** layered (see DGB); compile-error stderr parse; real r2.07 |
| TR | **remote (SSH) transport** | ◐ | **unit-green; live SSH `make test-it` PENDING (see below).** wraps the yottadb argv in `ssh`, sources EnvFile on the far side; Health/Exec/Version over SSH; lifecycle attach-only (provision/destroy refused, up=verify, down=no-op); sync-over-remote not yet wired (SourceStore refuses). caps now advertises `remote` + `features.remote=true`. Public facade `ydbdriver` (New→mdriver.Transport) for m-cli/VistaEngine. Contract amended (driver-contract §3). |
| M4 | data (get/set/kill/query/export/import) | ☐ | %GO/%GI/mupip extract |
| M5 | cover (view "TRACE" → LCOV) | ☐ | port mcov view-TRACE; golden |
| M6 | admin (backup/restore/check/journal) | ☐ | mupip backup/restore/integ/journal |
| M7 | native passthrough (mupip/dse/gde/lke/yottadb) | ☐ | |
| M8 | conformance green local+docker+remote | ◐ | `m-driver-conformance --driver dist/m-ydb --transport local` → **16/16** (caps/version/doctor/status). Full docker/remote run needs a real engine (yours). `meta version` already conformant. |
| CFM | **conformance finding: doctor envelope/exit mismatch — CLOSED** | ☑ | Was 15/16: `meta doctor` (unreachable) emitted stdout `exit:0` while the process exited 6. Root cause was shared **clikit**: `cc.Result` always wrote `{ok:true,exit:0}` to stdout; `Fail` wrote the error envelope to *stderr*. **Fixed:** added `clikit.Context.ResultExit(data, exit, text)` (emits the data envelope with the chosen exit/ok; `Run` returns `cc.ExitCode()`) — byte-identical across m-ydb/m-iris (m-cli copy when D3 lands). doctor now uses `ResultExit` → envelope.exit == process exit. Conformance rule relaxed to "ok=false needs error **or** data" (doctor carries `checks[]`). → 16/16. |

| DGB | **docker transport established no global directory — CLOSED** | ☑ | **The docker transport ran global-accessing M with no `$ydb_gbldir`**, so any global access faulted `%YDB-E-ZGBLDIRUNDEF` while routine load/run worked. Root cause: `execEnv()` returns `nil` for docker ("container's own env applies") and `buildTrapped` layered only `$ZROUTINES` at runtime — never the gbldir; a FOIA container (vehu) sets its VistA env solely via `/home/vehu/etc/env`, which `docker exec` doesn't source. So `v pkg install/verify/uninstall --engine ydb --transport docker` against vehu all failed at `run EN^ZVPKGINS` (ZZSKEL too — not v-pkg's fault), and **the previously-recorded "M0a YDB driver-path proven on vehu" was actually the raw-M-over-`docker exec` path, not `v pkg --engine ydb`.** **Fixed (2026-06-12):** `buildTrapped` now also `SET $ZGBLDIR=<cfg.GblDir>` when `GblDir` is configured — mirroring the `$ZROUTINES` runtime layering, so it works over docker with no `-e` plumbing and is a harmless re-assert on local/remote. TDD `TestExecTrapped_DockerSetsZGblDir`; `go test -race`/`vet`/`gofmt`/`make test-it` (r2.07) green. **Live-proven on vehu:** `m-ydb exec eval 'W $D(^XPD(9.7,0))' --transport docker` returns global data; `v pkg install/verify/uninstall --engine ydb --transport docker` runs the full KIDS lifecycle (install→#9.7 status 3 · verify installed:true · uninstall reversible→installed:false). Surfaced by m-stdlib VSL T0b.2. |

| DGB2 | **docker login-shell sources container env — CLOSED** | ☑ | The DGB fix only worked when the caller **passed** `M_YDB_GBLDIR`/`M_YDB_ROUTINES`; `m vista exec --engine ydb --transport docker` (m-cli) passes neither (`config.Resolve` fills from the *host* env, empty for a container), so `SET $ZGBLDIR` was skipped → `ZGBLDIRUNDEF` again, surfacing as a silent `ok:true,stdout:""` up the stack. **Fixed (2026-06-17):** `Session.wrap()` docker case now `docker exec -i <c> bash -lc <shJoin(argv)>` — a login shell sources the container's own `gtmgbldir`/`gtmroutines`/`gtm_dist` with **zero path flags** (mirrors m-cli's `DockerRunner`); the runtime `$ZGBLDIR`/`$ZROUTINES` injection stays as an explicit-override layer. TDD `TestDocker_WrapsArgv`/`TestDocker_Util_Argv` → `bash -lc` form; `go test -race`/`golangci-lint`/`make test-it` (m-test-engine) green. **Live on vehu (no flags):** `exec eval 'W $ZV'`→`GT.M V7.0-005`, `'W $P($G(^DIC(200,0)),"^",1)'`→`NEW PERSON`, `lifecycle status`→running/healthy/r2.02. Unblocks m-cli `m vista exec`/`status` + vdocs SKL S2.2 live-DD seam. |

**Live-validation owed for TR (remote/SSH)** — sandbox denies ssh-into / docker-exec,
so the live gate is the user's to run. To close TR: against an SSH-reachable YottaDB
(e.g. the FOIA `worldvista/vehu` container with sshd on :22), run
`m-ydb meta doctor --transport remote --host <h> --user <u> --env-file /home/vehu/etc/env`
and `m-ydb exec eval 'W $ZV' --transport remote --host <h> --user <u> --env-file …`;
expect a YottaDB version banner. Then add a gated integration test (skip-unless
`M_YDB_HOST` set) under `internal/transport/` and wire it into `make test-it`.

**needs SDK:** none — the remote transport reuses the frozen `Transport`/`TransportRemote`
(v0.2.0); no new shared shape. (Record here any shape M4/M5 needs for the coordinator to batch.)
