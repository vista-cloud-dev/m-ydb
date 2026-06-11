---
name: m-ydb-remote-ssh-transport
description: m-ydb gained a remote (SSH) transport + a public ydbdriver facade, reversing the earlier "YottaDB has no remote" stance; foundation for VistaEngine's unified YDB+IRIS access.
metadata:
  type: project
---

m-ydb now has a **third transport, `remote` (SSH)**, beside local + docker, plus a
public **`ydbdriver`** package — both added for the VSL / `VistaEngine` effort so
m-cli reaches a filesystem-based YottaDB VistA over the network the same way it
reaches IRIS over Atelier REST, **behind one `mdriver.Transport` contract**.

**What it is.** The SSH transport wraps the *same* `yottadb` argv in `ssh`: it
shell-quotes the engine argv into one remote command, optionally prefixed by
`. <EnvFile> &&` to source the instance env on the far side (e.g.
`/home/vehu/etc/env`), and lets `ssh` forward local stdin to the remote process
(so `-direct` script mode works). Config adds `Host/Port/User/Identity/EnvFile`
(`M_YDB_HOST` etc.). `Health`, `Exec` (command/entryref/script), and `Version`
ride it for free via the existing `wrap()`/`runFunc` seam — no new SDK shape (it
reuses the frozen `Transport` + `mdriver.TransportRemote` from v0.2.0).
Implementation: `internal/transport/session.go` (`sshWrap`, `shToken`/`shJoin`
minimal POSIX quoting; `$` is literal inside single quotes so `W $ZV` passes
through to `%XCMD` intact).

**Reversal — deliberate, user-approved (2026-06-11).** m-ydb previously forbade
remote: `caps_test` asserted *"YottaDB has no network API — remote must never be
advertised"*, `features.remote=false`, and `driver-contract.md §3` marked YDB
`remote` **unsupported**. The user approved reversing it. The rationale: SSH is a
**host-shell** transport (run the local-style invocation on another host), NOT an
IRIS-style network *engine* protocol — so "no network engine API" still holds while
`remote` becomes a real transport. caps now lists `remote` + `features.remote=true`;
the contract was amended to "remote = SSH host-shell" for YDB.

**Scope / honesty guards.** lifecycle over remote is **attach-only**: `provision`/
`destroy` are refused (`errRemoteAttachOnly` — you provision a YottaDB host out of
band), `up` = verify reachable (Health), `down` = no-op. **sync over remote is not
yet wired** — `SourceStore` refuses it (would otherwise wrongly read the local fs).

**Live gate PENDING (the user's to run).** The sandbox denies ssh-into / docker-exec
of shared containers, so only the unit tier (argv assertions via the recording
`runFunc`, `go test -race`/`vet`/`gofmt`) is green here. To close it: run
`meta doctor` + `exec eval 'W $ZV'` with `--transport remote` against an
SSH-reachable YottaDB (the FOIA `worldvista/vehu` exposes sshd on :22), then add a
skip-unless-`M_YDB_HOST` integration test into `make test-it`. Tracked in
[[m-ydb-driver-m0]] and `docs/m-ydb-tracker.md` (row TR).

**Public facade `ydbdriver`** (`ydbdriver/ydbdriver.go`): `New(Config) mdriver.Transport`
with `type Config = transport.Config`. This is the importable seam m-cli's
VistaEngine holds — vendor logic stays in `internal/`.
