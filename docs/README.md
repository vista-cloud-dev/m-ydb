# m-ydb docs

Documentation for **m-ydb** — the YottaDB engine driver (component D2 of the
m-engine-drivers effort). m-ydb implements the neutral `m-driver-sdk` contract
for YottaDB and is consumed up by m-cli and the `v` tools; it never reaches an
engine except through the driver seam.

This `docs/` tree follows the org-standard folder set. Only the folders this repo
actually uses are present.

## Layout

- **`m-ydb-tracker.md`** (root) — the live Tier-D implementation tracker for the
  driver spike (milestones M0–M8 + the remote/SSH transport row). Updated every
  increment; this is the step-2 target of the org Increment Protocol for m-ydb
  sessions (not the shared cross-repo plan, which the coordinator rolls up).
- **`memory/`** — driver-local auto-memory for this repo (durable facts only).
  An m-ydb session's harness recall path is symlinked here. See
  [`memory/MEMORY.md`](memory/MEMORY.md) for the index.

## Key docs

- [`m-ydb-tracker.md`](m-ydb-tracker.md) — milestone/transport status, closed
  findings (CFM/DGB/DGB2), and the live-validation owed for the remote transport.
- [`memory/m-ydb-driver-m0.md`](memory/m-ydb-driver-m0.md) — driver design canon
  (M0–M3: scaffold + SDK seam, lifecycle, filesystem-native sync, the
  `$ETRAP`→`$ZSTATUS` exec axis, compile-error stderr parsing).
- [`memory/m-ydb-docker-gbldir.md`](memory/m-ydb-docker-gbldir.md) — the
  `%YDB-E-ZGBLDIRUNDEF` docker-transport gotcha and the login-shell fix.
- [`memory/m-ydb-remote-ssh-transport.md`](memory/m-ydb-remote-ssh-transport.md)
  — the third transport (SSH host-shell) and the `ydbdriver` public facade.

## Conventions

- Memory and tracker live **in this repo** (driver-effort carve-out) — not in
  `~/claude/memory` and not in the `docs` repo. See `../CLAUDE.md`.
- Cross-repo coordination (the consistency protocol, SDK version ledger, driver
  contract) lives in the central `docs` repo's `docs/memory/` and loads as rules.
