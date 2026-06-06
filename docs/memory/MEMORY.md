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
