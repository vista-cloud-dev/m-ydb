# Memory index — m-ydb (YottaDB engine driver, D2)

Driver-local memory for the **m-ydb** repo. A session started in
`~/vista-cloud-dev/m-ydb` recalls from here (the harness memory path is symlinked
to this dir). Write m-ydb-specific facts here — NOT to `~/claude/memory` and NOT
to the `docs` repo's `docs/memory/`.

Cross-repo coordination (the consistency protocol, the SDK version ledger, the
driver contract, the frozen-SDK-window rhythm) lives in the **`docs` repo's
`docs/memory/`** + the org/per-repo `CLAUDE.md` — those load as rules; read them
for how m-ydb stays in lockstep with m-iris via `m-driver-sdk`.

- [m-ydb driver M0–M3](m-ydb-driver-m0.md) — YottaDB driver (D2), work moves to branch `m-ydb-driver`. M0+M1+M2+M3 done (exec axis: load/run/eval/abort + engineError from $ZSTATUS), real-validated YottaDB r2.07. Next M5 cover / M4 data. Filesystem-native sync over $ydb_routines. Pins m-driver-sdk v0.2.0.
- [m-ydb docker gbldir fix](m-ydb-docker-gbldir.md) — FIXED 2026-06-12: the docker transport set no `$ydb_gbldir`, so all global-accessing M (the whole KIDS lifecycle) faulted `%YDB-E-ZGBLDIRUNDEF`; `buildTrapped` now `SET $ZGBLDIR` at runtime (mirrors `$ZROUTINES` layering). Live-proven on vehu — `v pkg … --engine ydb` full lifecycle green. Corrects the M0a "YDB driver-path proven" record (was raw-M). Surfaced by m-stdlib T0b.2.
- [m-ydb remote (SSH) transport](m-ydb-remote-ssh-transport.md) — NEW 3rd transport `remote` (SSH host-shell) + public `ydbdriver` facade, for VistaEngine's unified YDB+IRIS access behind `mdriver.Transport`. Reverses the old "YottaDB has no remote" stance (user-approved 2026-06-11; SSH ≠ network engine API). Unit-green; **live SSH `make test-it` PENDING** (sandbox blocks ssh/docker). lifecycle attach-only; sync-over-remote not yet wired.
