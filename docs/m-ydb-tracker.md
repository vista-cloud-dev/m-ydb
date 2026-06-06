# m-ydb implementation tracker (D2)

Per-repo tracker — the step-2 target for m-ydb driver sessions (org Increment
Protocol). Update the active row here, in this repo, every increment. The shared
`docs/m-engine-drivers/driver-implementation-plan.md` §4 is the coordinator's
cross-repo roll-up, synced at milestone boundaries — do not edit it from a driver
spike. Status: ☐ todo · ◐ in progress · ☑ done.

Pinned: `m-driver-sdk v0.2.0`. Branch: `m-ydb-driver`. Transports: local·docker.

| M | Axis | Status | Notes |
|---|---|---|---|
| M0 | scaffold + SDK seam + `meta` | ☑ | honest caps golden |
| M1 | lifecycle + health + doctor | ☑ | daemonless (gde/mupip/rundown); real r2.07 |
| M2 | sync (8 verbs) | ☑ | filesystem-native over $ydb_routines; read+write; real r2.07 |
| M3 | exec (load/run/eval/abort) + engineError | ☑ | $ETRAP→$ZSTATUS; $ZROUTINES-layered; compile-error stderr parse; real r2.07 |
| M4 | data (get/set/kill/query/export/import) | ☐ | %GO/%GI/mupip extract |
| M5 | cover (view "TRACE" → LCOV) | ☐ | **next on ladder** — port mcov view-TRACE; golden |
| M6 | admin (backup/restore/check/journal) | ☐ | mupip backup/restore/integ/journal |
| M7 | native passthrough (mupip/dse/gde/lke/yottadb) | ☐ | |
| M8 | conformance green local+docker | ☐ | release gate |

**needs SDK:** (record here any shared shape M4/M5 requires that isn't in the pinned
SDK yet, for the coordinator to batch — none currently.)
