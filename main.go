// Command m-ydb is the YottaDB engine driver for the m toolchain: a vendor
// adapter exposing the neutral engine-driver contract (driver-contract.md v1.0)
// over YottaDB, plus the complete native YottaDB surface for power users. m-cli
// speaks only the contract; all YottaDB specifics (mupip/dse/gde/lke, the
// files-plus-rundown lifecycle, $ZSTATUS error parsing) live here, behind it.
//
// The contract surface is grouped into axes — m-cli speaks only these:
//
//	m-ydb meta caps        capability document (axes/transports/features)
//	m-ydb meta info        driver identity + resolved engine target
//	m-ydb meta version     driver/engine/contract + build info
//	m-ydb meta schema      machine-readable command tree (agent discovery)
//	m-ydb meta doctor      typed preflight diagnostics (exit 0/5/6)
//	m-ydb lifecycle status report running/healthy/version; --probe for CI gating
//	m-ydb lifecycle wait   block until healthy or --timeout (exit 6)
//	m-ydb sync list        inventory routine source ($ydb_routines)
//	m-ydb sync pull        source → mirror, incremental via the manifest
//	m-ydb sync status      source vs. local manifest drift (exit 3 on drift)
//	m-ydb sync verify      re-hash mirror files against the manifest (exit 3)
//
// Later milestones add the sync write verbs (push/deploy/diff/rm) and the exec,
// data, cover, admin, and native axes; caps grows to advertise each as it lands
// (caps is honest — advertised == implemented).
//
// Connection config comes from flags or M_YDB_* env (flags win), with unset
// paths falling back to the standard ydb_*/gtm_* env; see internal/config.
// Transports: local | docker (YottaDB has no network API, so no remote).
package main

import (
	"os"

	"github.com/alecthomas/kong"
	"github.com/willabides/kongplete"

	"github.com/vista-cloud-dev/m-ydb/clikit"
	"github.com/vista-cloud-dev/m-ydb/internal/config"
)

// CLI is the root command grammar — one typed struct Kong parses and `schema`
// reflects. clikit.Globals and config.Conn are embedded so both contribute
// global flags; config.Conn is bound so commands receive a *config.Conn. The
// contract verbs are grouped into axis subcommands (meta, lifecycle, …).
type CLI struct {
	clikit.Globals
	config.Conn

	Meta      metaCmd      `cmd:"" help:"Introspection + power tools: caps / info / version / schema / doctor."`
	Lifecycle lifecycleCmd `cmd:"" help:"Manage the engine instance: status / wait (up/down/restart/provision/destroy land in M1b)."`
	Sync      syncCmd      `cmd:"" help:"Source axis: routine source ↔ instance (list / pull / status / verify; push/deploy/diff/rm land next)."`

	InstallCompletions kongplete.InstallCompletions `cmd:"" help:"Install shell tab-completions."`
}

func main() {
	cli := &CLI{}
	os.Exit(clikit.Run(
		"m-ydb",
		"YottaDB engine driver for the m toolchain — neutral contract verbs (meta, lifecycle, …) over YottaDB, plus the native YottaDB surface.",
		cli, &cli.Globals,
		kong.Bind(&cli.Conn),
	))
}
