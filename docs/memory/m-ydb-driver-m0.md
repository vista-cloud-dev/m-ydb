---
name: m-ydb-driver-m0
description: m-ydb YottaDB engine driver (D2); M0+M1+M2+M3 done (exec full axis); next M5 cover / M4 data; all real-validated r2.07.
metadata: 
  node_type: memory
  type: project
  originSessionId: a1478d30-d94d-4932-af02-baa0bf9a4944
---

`m-ydb` (component D2 of [[m-engine-drivers-project]]) is a new greenfield repo at
`~/vista-cloud-dev/m-ydb` — the YottaDB vendor adapter implementing the neutral
driver contract (`docs/m-engine-drivers/driver-contract.md` v1.0). Go, clikit,
go-cli-template scaffold; module `github.com/vista-cloud-dev/m-ydb`; binary `m-ydb`.
clikit is vendored per-repo (copied, self-contained) — same pattern as m-cli/irissync.

**M0 done (2026-06-04), all green + race-clean + gofmt-clean:**
- `internal/contract/` — thin local vendor of the neutral contract types (Caps, Axes,
  Features, EngineError, Version="1.0", Transport* consts). Future `m-driver-sdk` seed;
  keep vendor-neutral (no YottaDB specifics).
- `internal/driver/caps.go` — `Caps()` returns m-ydb capability doc; golden test in
  `testdata/caps.json` (engine=ydb, transports local+docker, features.remote=false).
  `UPDATE_GOLDEN=1` regenerates.
- `internal/transport/` — the **verb-level** Transport seam (risk B1): interface
  `Exec/Load/Compile/ReadGlobal/Health`. TWO-level design: (1) Transport = what vendor
  logic consumes, faked via `Fake`; (2) inner `runFunc(ctx,argv,env,stdin)` seam inside
  `Session` (local/docker), faked in tests to assert argv. local builds
  `$ydb_dist/yottadb -run %XCMD "<cmd>"` / `-run <entryref> args` / `-direct`+halt-on-stdin;
  docker wraps `docker exec -i <container> …`. Load/Compile/ReadGlobal return
  `ErrNotImplemented` until M2/M3/M4.
- `main.go` — top-level meta verbs `caps`/`version`/`schema` (contract §2/§4: meta is
  top-level, axes are `<axis> <verb>` groups added per milestone). `version` data =
  {driver,engine,contract,build}.

**M1 DONE (2026-06-04, real-engine validated):** command tree restructured to `meta {caps,
info,version,schema,doctor}` + `lifecycle {up,down,restart,status,wait,provision,destroy}`
(parity with m-iris; m-cli invokes both identically). internal/config.Conn (M_YDB_* +
ydb_*/gtm_* env fallback). transport.Session gained Util(name,args,stdin)/Docker/Version +
lifecycle (Provision=gde+mupip create / Up=ensure+rundown-stale C1 / Down=rundown /
Destroy=rm files; docker=run/start/stop/rm). doctor matrix (binary/version≥r1.34/gld/
routines/docker; exit 0/5/6) with injectable deps. status/--probe/wait via Health+Version.
Uses SDK shapes Status/StateResult/Check/DoctorResult. **Gated test M_YDB_IT=1 PASSED green
against m-test-engine = YottaDB r2.07** (docker up/status/doctor). Logs verb deferred (parity).
SDK now v0.2.0 (both drivers pinned). Plan §4 tasks 1-5 ☑.

**Next: M2** (sync — filesystem-native over $ydb_routines; list/pull/status/verify, then
push --from/deploy --prune/diff/rm) per critical path M0→M1→M2(staging)→M3→M5→M8.
Known follow-up: harmonize `meta version` shape with m-iris (m-iris uses generic clikit
VersionCmd; m-ydb uses contract {driver,engine,contract,build}) — see [[m-engine-drivers-consistency-protocol]].

Git repo on `main`, pushed to public **`github.com/vista-cloud-dev/m-ydb`** (2026-06-04).
Tracking table = plan §4 (task 1 ☑, task 2 ◐).

**UPDATE (Phase-0, 2026-06-04 — see [[m-driver-sdk-phase0]]):** m-ydb switched onto the
extracted `m-driver-sdk`. `internal/contract` DELETED; `internal/transport/{transport,fake}.go`
DELETED (types + FakeTransport now in `mdriver`). `internal/transport/session.go` implements
`mdriver.Transport`: Compile dropped, SetGlobal added, ReadGlobal→GlobalNode, field-based Exec
(no ExecMode). **Caps now HONEST** — M0 advertises only `meta:[caps,version,schema]`,
`snapshot:false` (grows per milestone); golden regenerated. clikit aligned to contract ladder.
go.mod has `replace …/m-driver-sdk => ../m-driver-sdk`.

**M2 READ SIDE DONE (2026-06-04, real-validated vs YottaDB r2.07) — pushed `main` (dfa74f6).**
Plan §4 task 6 ☑.
Filesystem-native sync over `$ydb_routines` (the .m file IS the source → near-identity mirror).
New internal packages (ports of m-iris's design, independent per consistency-protocol):
- `internal/manifest` — `.m-ydb-manifest.json`; `Compare`/`Diff` over name→source-mtime
  (epoch seconds, so it is identical read locally or via container `stat -c %Y`); SHA-256 integrity.
- `internal/mirror` — flat `Layout{Root}`, atomic LF-normalized `WriteRoutine`, `HashFile`.
- `internal/source` — `Store` iface: `FileStore` (host fs, local) | `ShellStore` (container,
  docker — runs `sh -c` scripts via new `transport.Session.Sh(ctx,script)→(stdout,code,err)`
  seam, NOT part of neutral contract). `ParseRoutinesDirs` extracts source dirs from
  `$ydb_routines` (handles `obj(src…)`, `*` autorelink, skips `.so`). `Match` = bare-name
  (ext-stripped) `--filter`. `nameRe` validates routine names (also shell-injection guard).
- `sync.go` (pkg main) — `sync {list,pull,status,verify}` wired into CLI; pull incremental +
  prunes deletions, status drift→exit 3, verify re-hash→exit 3; honors `--filter`/`--dry-run`.
  config.Conn gained `Mirror`/`Filter`/`DryRun` + `SourceStore()`/`SourceDirs()`/`Layout()`.
- caps advertises `sync:[list,pull,status,verify]` (write verbs withheld — honest); golden regen.
- Gated `TestRealShellStore` (internal/source) stages fixtures in m-test-engine over the session
  shell, lists/reads back. `make test-it` now runs `-run Real` across internal/transport + source.
**M2 WRITE SIDE DONE (2026-06-04, real-validated vs YottaDB r2.07) — pushed `m-ydb` 9c419c9 on
`main`.** Plan §4 task 7 ☑ → **M2 sync axis COMPLETE** (8 verbs). Added:
- `source.Store` gained `Write`(→new TS)/`Remove`; FileStore (host fs) + ShellStore (docker:
  content base64→`base64 -d` into container, mtime via `stat`; targets primary source dir).
- `internal/manifest/conflict.go` — `CheckConflict`/`Conflict` (Changed/Deleted/Exists) cross-writer guard.
- `internal/udiff` — compact LCS single-hunk unified diff (3-line context).
- `sync diff <name>` (instance vs mirror/`--from`, normalized both sides → {unified}); `sync push`
  (from mirror or `--from <dir>`; conflict-checked vs manifest, exit 4 unless `--force`; lands content
  in BOTH mirror+instance, updates manifest; {pushed[],compiled}); `sync deploy <dir> [--prune]`
  (library install; prune true-syncs instance routines under the lib's common bare-name prefix not in
  the lib — REFUSES exit 4 when no common prefix = no safe scope; {installed[],pruned[]}); `sync rm
  <name>` (clears instance+mirror+manifest; {removed[]}). All honor `--filter`/`--dry-run`.
- caps advertises all 8 sync verbs; golden + meta schema regenerated.
- Gated `TestRealShellStoreWrite` (make test-it): write/read/remove round-trip with tabs/quotes/specials.
mirror gained exported `Normalize`. push `compiled`=0 (YottaDB compiles implicitly; `exec load` forces).
Tracker (m-driver-sdk 9c357c5) + plan (docs 970b540) reconciled.

**M3 EXEC slice 1 DONE (2026-06-04, real-validated r2.07) — pushed `m-ydb` a3952f0 on `main`.** Plan §4
task 10 ☑, tasks 8/9 ◐ (run+eval done). `internal/transport/exec.go`: `ExecTrapped` runs the work
(EntryRef→`DO` with Args→`$ZCMDLINE`, or Command) under `$ETRAP` via **`-run %XCMD`** — NOT `-direct`
(direct mode prints `YDB>` prompts + handles errors at the prompt, bypassing the trap; confirmed). Trap
writes `$C(1)`-delimited `$ZSTATUS` to $PRINCIPAL + `ZHALT 1`; `splitStatus` strips it; `parseZStatus`
→ §7 EngineError, detecting the mnemonic by **`%FAC-S-NAME` regex** (so a `%`-routine location like
`%XCMD+5^%XCMD` isn't mis-parsed). `exec run`/`exec eval` cmds (exec.go, pkg main) → {stdout,status};
fault = ok=false + engineError (exit 5, `clikit.FailEngine`, SDK→clikit EngineError convert). caps
`exec:[run,eval]`. Gated `TestRealExecTrapped` (real `%YDB-E-LVUNDEF`). Raw `Session.Exec` unchanged
(Health still uses clean `-run %XCMD "write 1"`). **M3 EXEC slice 2 DONE (2026-06-04, real-validated r2.07) — pushed `m-ydb` 09157e0 on `main`. M3 COMPLETE
(plan §4 tasks 8/9/10 ☑).** Routines-env solved NOT via `-e` but by layering the configured path onto
`$ZROUTINES` at runtime (overriding `ydb_routines` env breaks `%XCMD` resolution since the engine's
default path holds the plugin dirs where `%XCMD` lives). `exec load <paths>|--from <dir>` stages
(store.Write) + compiles via `Session.Compile` (ZLINK). KEY FINDING: a YottaDB **compile** fault is a
stderr listing (`%YDB-E-INVCMD, … / At column C, line L, source module PATH`) with **exit 0** and does
NOT hit `$ETRAP` — so Compile parses the stderr listing (`parseCompileError`), distinct from runtime
faults (`$ZSTATUS` via trap). `exec abort --prefix` greps the `;<prefix>` marker that `run --prefix`
embeds, filters to real yottadb procs via `/proc/<pid>/comm` (else pgrep matches its own `sh -c`), →
`mupip stop`. caps `exec:[load,run,eval,abort]`. Gated tests: run staged→"HI42", compile→%YDB-E-INVCMD
L2, abort no-op. Tracker m-driver-sdk 6cb6b63, plan docs e0ba0dd. **NEXT: M5 cover**
(`view "TRACE":1:"^ycov"`→LCOV; port m-cli/internal/mcov) and/or **M4 data** (get/set/kill/query +
export/import). Per-driver critical path M4/M6 after M3; M5 next on the ladder.
