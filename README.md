# m-ydb — the YottaDB engine driver for the `m` toolchain

`m-ydb` is the YottaDB vendor adapter (component **D2** of the
[m-engine-drivers](../docs/m-engine-drivers/README.md) project). It exposes the
neutral [engine-driver contract](../docs/m-engine-drivers/driver-contract.md)
v1.0 over YottaDB so that `m` (m-cli) speaks only the contract and all YottaDB
specifics — `mupip`/`dse`/`gde`/`lke`, the files-plus-`rundown` lifecycle,
`$ZSTATUS` error parsing, `view "TRACE"` coverage — live here, behind it.

> **Device-driver principle.** Adding an engine is a new `m-<engine>` binary that
> passes `m-driver-conformance`, with zero changes to m-cli.

## Two surfaces

1. **Contract verbs** — the neutral axes `m` invokes: `lifecycle`, `sync`,
   `exec`, `data`, `cover`, `admin`, plus the top-level `meta` verbs
   (`caps`/`version`/`info`/`doctor`/`selftest`/`native`).
2. **Native passthrough** — the complete YottaDB surface for humans:
   `m-ydb native -- mupip backup …` and first-class `m-ydb mupip … | dse … | gde … | lke … | yottadb …`.

## Transports

YottaDB has no network API, so `m-ydb` supports **`local`** and **`docker`**
only (no `remote`). Both pipe M into a `yottadb` session; docker wraps the same
argv in `docker exec`. The transport seam is **verb-level**
(`Exec`/`Load`/`Compile`/`ReadGlobal`/`Health`) — not a low-level `run(argv)` —
so the same interface fits the IRIS driver's Atelier-SQL shape too (the seed of
the shared `m-driver-sdk`).

```
m-ydb caps    --output json      # the capability document (axes, transports, features)
m-ydb version --output json      # { driver, engine, contract, build }
m-ydb schema  | jq .             # the reflected command/flag tree (agent discovery)
```

## Build & test

```
make build          # static binary → dist/m-ydb
make test           # go test -race -cover ./...
make lint           # golangci-lint
```

Test-driven throughout: every verb is a failing table-driven test (fake
`Transport`, golden JSON envelopes) → red → implement → green → `go test -race`.
Unit tests never touch a real engine; the real YottaDB container (`m-test-engine`)
is the gated integration tier.

## Status

See the m-ydb tracking table in
[`driver-implementation-plan.md` §4](../docs/m-engine-drivers/driver-implementation-plan.md).
**M0 (scaffold + transport seam + `meta`) is complete.** Next: M1 (lifecycle,
health probes, `doctor`).
