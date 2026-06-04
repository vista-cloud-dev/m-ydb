package main

import (
	"runtime"
	"strings"

	"github.com/vista-cloud-dev/m-ydb/clikit"
	"github.com/vista-cloud-dev/m-ydb/internal/config"
	"github.com/vista-cloud-dev/m-ydb/internal/driver"
)

// metaCmd is the meta axis (driver-contract §5.7): introspection + power tools.
// caps/info/version/schema/doctor are wired; selftest (M8) and native + shell
// (M7) join as their milestones land — and caps grows to advertise them.
type metaCmd struct {
	Caps    capsCmd          `cmd:"" help:"Emit the capability document (axes, transports, features) m-cli reads before calling optional verbs."`
	Info    infoCmd          `cmd:"" help:"Driver identity + resolved engine target (paths; engine version via the probe)."`
	Version versionCmd       `cmd:"" help:"Show driver / engine / contract version + build info."`
	Schema  clikit.SchemaCmd `cmd:"" help:"Emit the command/flag tree as JSON (agent discovery)."`
	Doctor  doctorCmd        `cmd:"" help:"Typed preflight: binary / version / gld / routines / docker (exit 0/5/6)."`
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
// The engine's *release* version is reported by info/status, which probe a live
// install; this is static and needs no engine.
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

// --- meta info ---------------------------------------------------------------

type infoCmd struct{}

// infoResult is the driver identity + the resolved engine target. The engine
// release/regions come from a live probe (lifecycle status); info reports the
// static, no-engine facts (driver identity + resolved paths) so it is always
// safe to call — the first thing scaffolding runs. The {driver,engine,contract,
// build} prefix matches m-iris's info for cross-driver consistency.
type infoResult struct {
	Driver    string `json:"driver"`
	Engine    string `json:"engine"`
	Contract  string `json:"contract"`
	Build     string `json:"build"`
	Transport string `json:"transport"`
	Dist      string `json:"dist,omitempty"`
	GblDir    string `json:"gbldir,omitempty"`
	Routines  string `json:"routines,omitempty"`
	Container string `json:"container,omitempty"`
}

func (infoCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	conn.ResolveEnv()
	res := infoResult{
		Driver:    "m-ydb",
		Engine:    "ydb",
		Contract:  driver.Caps().Contract,
		Build:     clikit.Version,
		Transport: conn.Transport,
		Dist:      conn.Dist,
		GblDir:    conn.GblDir,
		Routines:  conn.Routines,
		Container: conn.Container,
	}
	return cc.Result(res, func() {
		cc.Title("m-ydb — driver info")
		cc.KV(
			[2]string{"driver", res.Driver},
			[2]string{"engine", res.Engine},
			[2]string{"contract", res.Contract},
			[2]string{"build", res.Build},
			[2]string{"transport", res.Transport},
			[2]string{"dist", res.Dist},
			[2]string{"gbldir", res.GblDir},
			[2]string{"routines", res.Routines},
		)
	})
}
