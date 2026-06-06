# m-ydb — YottaDB engine driver (D2). Repo rules.

Adds to the org rules (`~/vista-cloud-dev/CLAUDE.md`) and the user global
(`~/.claude/CLAUDE.md`). Where this file says **EXCEPTION**, it *overrides* those
for this repo (the user authorized driver-effort carve-outs, 2026-06-06).

This is a **driver spike** session — one of the three coordinated repos
(`m-driver-sdk` ⟷ `m-iris` ⟷ `m-ydb`). Read [[coordination-model]]
(`docs/m-engine-drivers/coordination-model.md` in the `docs` repo) once per fresh
session that touches the driver effort.

## Lane — what this session owns
- **Owns / may push: `m-ydb` only**, on branch **`m-ydb-driver`** (never `main`).
- **Never edit `m-driver-sdk`** here, and never push `m-driver-sdk` / `m-iris` /
  `docs`. Those belong to the coordinator session. (m-cli is out of scope until D3.)

## The SDK is pinned — do not touch it mid-spike
- Consume `github.com/vista-cloud-dev/m-driver-sdk` at the **pinned tagged version**
  in `go.mod` (currently **v0.2.0**). No `replace` directives, no pseudo-versions.
- If you need a new shared shape (a type m-cli will read, or that m-iris must match):
  **do NOT bump the SDK from here.** Stub it locally, record `needs SDK: <shape>` in
  this repo's memory, and surface it for a coordinator session to batch into the next
  SDK release. Re-pin only when the coordinator tags a new version.
- `caps` stays **honest** (advertise only wired verbs). The neutral contract +
  envelope shapes are the m-cli surface; they change only via the SDK/contract,
  which you don't edit here — so you cannot drift the surface.

## Increment Protocol — EXCEPTIONS for this repo
Run the org Increment Protocol (persist memory → update tracker → commit+push) at
every verified increment, automatically, **but**:
- **EXCEPTION (memory):** m-ydb memory lives in **`./docs/memory/`** (this repo),
  committed here with the code. Do **NOT** write `~/claude/memory` and do **NOT**
  write the `docs` repo's `docs/memory/` (shared coordination memory, coordinator-
  owned). The harness recall path for an m-ydb session is symlinked to `./docs/memory/`.
- **EXCEPTION (tracker):** update **`./docs/m-ydb-tracker.md`** (this repo), not the
  shared `docs/m-engine-drivers/driver-implementation-plan.md` §4 — the coordinator
  rolls the shared plan up at milestone boundaries. This keeps parallel iris/ydb
  spikes from clashing on the `docs` repo.
- **Commit+push:** `m-ydb` branch `m-ydb-driver` only. Gates first:
  `go test -race ./...`, `go vet`, `gofmt`, and `make test-it` against the live
  YottaDB (`m-test-engine`, r2.07) for any engine-touching change.

## Real-engine validation
Validate every milestone slice against real YottaDB (`make test-it`, `m-test-engine`,
r2.07) — the fake transport tier alone misses engine-shape bugs.
