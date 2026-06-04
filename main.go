// Command m-ydb is the YottaDB engine driver for the m toolchain: a vendor
// adapter exposing the neutral engine-driver contract (driver-contract.md v1.0)
// over YottaDB. m-cli speaks only the contract; all YottaDB specifics
// (mupip/dse/gde/lke, files+rundown lifecycle, $ZSTATUS error parsing) live
// here, behind it.
//
// It exposes two surfaces: the neutral contract verbs (grouped into the axes
// lifecycle/sync/exec/data/cover/admin plus the top-level meta verbs) and — for
// power users — the complete native passthrough (mupip/dse/gde/lke/yottadb).
//
// Every invocation writes one JSON envelope to stdout under --output json and
// uses the toolchain-wide clikit conventions. Transports: local and docker
// (YottaDB has no network API, so there is no remote).
package main

import (
	"os"
	"runtime"
	"strings"

	"github.com/willabides/kongplete"

	"github.com/vista-cloud-dev/m-ydb/clikit"
	"github.com/vista-cloud-dev/m-ydb/internal/driver"
)

// CLI is the root command grammar — one typed struct Kong parses and `schema`
// reflects. Axis groups (lifecycle/sync/exec/…) are added as each milestone
// lands; the meta verbs (caps/version/info/…) sit at the top level per the
// contract's invocation examples (§4, §6).
type CLI struct {
	clikit.Globals

	Caps    capsCmd          `cmd:"" help:"Emit the capability document (axes, transports, features) as JSON."`
	Version versionCmd       `cmd:"" help:"Show driver, engine, and contract version + build info."`
	Schema  clikit.SchemaCmd `cmd:"" help:"Emit the command/flag tree as JSON (agent discovery)."`

	InstallCompletions kongplete.InstallCompletions `cmd:"" help:"Install shell tab-completions."`
}

func main() {
	cli := &CLI{}
	os.Exit(clikit.Run(
		"m-ydb",
		"m-ydb — the YottaDB engine driver for the m toolchain (driver-contract.md v1.0).",
		cli, &cli.Globals,
	))
}

// --- meta caps ---------------------------------------------------------------

type capsCmd struct{}

func (capsCmd) Run(cc *clikit.Context) error {
	c := driver.Caps()
	return cc.Result(c, func() {
		cc.Title("m-ydb capabilities")
		cc.KV(
			[2]string{"engine", cc.Accent(c.Engine)},
			[2]string{"contract", c.Contract},
			[2]string{"transports", strings.Join(c.Transports, ", ")},
		)
		cc.Rule("axes")
		// Only the wired (non-empty) axes are advertised; show just those.
		var axes [][2]string
		for _, a := range c.Axes.Wired() {
			axes = append(axes, [2]string{a.Name, strings.Join(a.Verbs, " ")})
		}
		cc.KV(axes...)
	})
}

// --- meta version ------------------------------------------------------------

type buildInfo struct {
	Commit string `json:"commit"`
	Date   string `json:"date"`
	Go     string `json:"go"`
}

// versionData is the contract §5.7 version document: the driver's own version,
// the engine it adapts, the contract level it implements, and build metadata.
// The engine's *release* version is reported by `info`/`status`, which probe a
// live install; this is static and needs no engine.
type versionData struct {
	Driver   string    `json:"driver"`
	Engine   string    `json:"engine"`
	Contract string    `json:"contract"`
	Build    buildInfo `json:"build"`
}

type versionCmd struct{}

func (versionCmd) Run(cc *clikit.Context) error {
	v := versionData{
		Driver:   clikit.Version,
		Engine:   "ydb",
		Contract: driver.Caps().Contract,
		Build:    buildInfo{Commit: clikit.Commit, Date: clikit.Date, Go: runtime.Version()},
	}
	return cc.Result(v, func() {
		cc.KV(
			[2]string{"driver", cc.Accent(v.Driver)},
			[2]string{"engine", v.Engine},
			[2]string{"contract", v.Contract},
			[2]string{"commit", v.Build.Commit},
			[2]string{"built", v.Build.Date},
			[2]string{"go", v.Build.Go},
		)
	})
}
