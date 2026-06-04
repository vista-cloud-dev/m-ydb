package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
	"github.com/vista-cloud-dev/m-ydb/clikit"
	"github.com/vista-cloud-dev/m-ydb/internal/config"
	"github.com/vista-cloud-dev/m-ydb/internal/mirror"
	"github.com/vista-cloud-dev/m-ydb/internal/transport"
)

// execCmd is the exec axis (driver-contract §5.3) — run M code against the
// instance. load stages .m source into the routine path and compiles it (ZLINK,
// surfacing compile faults from the listing); run/eval execute an entryref / a
// single command under a $ETRAP that surfaces a structured engineError
// ($ZSTATUS, §7) on a runtime fault; abort stops jobs tagged with an ephemeral
// --prefix. The configured routine path is layered onto $ZROUTINES at runtime so
// staged routines resolve while %XCMD stays linked.
type execCmd struct {
	Load  execLoadCmd  `cmd:"" name:"load" help:"Stage .m source into the instance routine path and compile it (ZLINK); compile faults surface as engineError."`
	Run   execRunCmd   `cmd:"" name:"run" help:"Run an entryref (LABEL^ROUTINE); args → $ZCMDLINE. Faults surface as engineError."`
	Eval  execEvalCmd  `cmd:"" name:"eval" help:"Evaluate a single M command. Faults surface as engineError."`
	Abort execAbortCmd `cmd:"" name:"abort" help:"Stop running jobs tagged with an ephemeral --prefix (pgrep + mupip stop)."`
}

type execResult struct {
	Stdout string `json:"stdout"`
	Status int    `json:"status"`
}

// --- load --------------------------------------------------------------------

type execLoadCmd struct {
	Paths     []string `arg:"" optional:"" help:".m source files to stage."`
	From      string   `help:"Stage every .m routine in this directory." placeholder:"DIR"`
	NoCompile bool     `name:"no-compile" help:"Stage only; skip the ZLINK compile check."`
}

type execLoadResult struct {
	Loaded   []string `json:"loaded"`
	Compiled bool     `json:"compiled"`
}

func (c *execLoadCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if len(c.Paths) == 0 && c.From == "" {
		return clikit.Fail(clikit.ExitUsage, "NO_SOURCE", "exec load needs <paths…> or --from <dir>", "")
	}
	store, err := conn.SourceStore()
	if err != nil {
		return usageErr(err)
	}
	srcs, err := c.gather(conn.Filter)
	if err != nil {
		return err
	}
	ctx := context.Background()
	loaded := make([]string, 0, len(srcs))
	for _, sc := range srcs {
		if _, err := store.Write(ctx, sc.name, mirror.Normalize(sc.content)); err != nil {
			return runtimeErr(err)
		}
		loaded = append(loaded, sc.name)
	}
	sort.Strings(loaded)

	if c.NoCompile {
		return cc.Result(execLoadResult{Loaded: loaded, Compiled: false}, func() {
			cc.Title("load complete (staged, not compiled)")
			cc.KV([2]string{"loaded", fmt.Sprint(len(loaded))})
		})
	}

	ee, err := conn.NewSession().Compile(ctx, bareNames(loaded))
	if err != nil {
		return runtimeErr(err)
	}
	if ee != nil {
		msg := strings.TrimSpace(ee.Mnemonic + " " + ee.Text)
		return clikit.FailEngine(clikit.ExitRuntime, "COMPILE_ERROR", "compile failed: "+msg, "", toClikitEngineError(ee))
	}
	return cc.Result(execLoadResult{Loaded: loaded, Compiled: true}, func() {
		cc.Title("load complete")
		cc.KV([2]string{"loaded", fmt.Sprint(len(loaded))}, [2]string{"compiled", "yes"})
		fmt.Fprintln(cc.Stdout, cc.Success("routines staged + compiled"))
	})
}

type routineSrc struct {
	name    string
	content []byte
}

// gather reads the routine sources named by the explicit paths and/or the
// --from directory (the latter filtered by the bare-name --filter).
func (c *execLoadCmd) gather(glob string) ([]routineSrc, error) {
	var out []routineSrc
	for _, p := range c.Paths {
		if filepath.Ext(p) != ".m" {
			return nil, usageErr(fmt.Errorf("not a .m routine: %s", p))
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, runtimeErr(err)
		}
		out = append(out, routineSrc{name: filepath.Base(p), content: b})
	}
	if c.From != "" {
		names, err := dirRoutines(c.From, glob)
		if err != nil {
			return nil, err
		}
		for _, n := range names {
			b, err := os.ReadFile(filepath.Join(c.From, n))
			if err != nil {
				return nil, runtimeErr(err)
			}
			out = append(out, routineSrc{name: n, content: b})
		}
	}
	return out, nil
}

// --- run ---------------------------------------------------------------------

type execRunCmd struct {
	EntryRef string   `arg:"" help:"Entryref to run (LABEL^ROUTINE or ^ROUTINE)."`
	Args     []string `arg:"" optional:"" help:"Arguments, joined into $ZCMDLINE."`
	Prefix   string   `help:"Ephemeral-run prefix; marks the process so 'exec abort --prefix' can stop it." placeholder:"PREFIX"`
}

func (c *execRunCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	return runExec(cc, conn, mdriver.ExecRequest{EntryRef: c.EntryRef, Args: c.Args, Prefix: c.Prefix})
}

// --- abort -------------------------------------------------------------------

type execAbortCmd struct {
	Prefix string `help:"Ephemeral-run prefix to abort (the marker passed to 'exec run --prefix')." placeholder:"PREFIX"`
}

type execAbortResult struct {
	Killed []string `json:"killed"`
}

func (c *execAbortCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	if c.Prefix == "" {
		return clikit.Fail(clikit.ExitUsage, "NO_PREFIX", "exec abort needs --prefix", "")
	}
	if err := conn.Validate(); err != nil {
		return clikit.Fail(clikit.ExitUsage, "BAD_CONN", err.Error(), "")
	}
	killed, err := conn.NewSession().Abort(context.Background(), c.Prefix)
	if err != nil {
		return runtimeErr(err)
	}
	return cc.Result(execAbortResult{Killed: nonNil(killed)}, func() {
		if len(killed) == 0 {
			fmt.Fprintln(cc.Stdout, cc.Faint("no jobs matched --prefix "+c.Prefix))
			return
		}
		fmt.Fprintln(cc.Stdout, cc.Success(fmt.Sprintf("stopped %d job(s): %s", len(killed), strings.Join(killed, ", "))))
	})
}

// --- eval --------------------------------------------------------------------

type execEvalCmd struct {
	Command []string `arg:"" help:"M command to evaluate (joined with spaces; quote it as one shell arg)."`
}

func (c *execEvalCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	return runExec(cc, conn, mdriver.ExecRequest{Command: strings.Join(c.Command, " ")})
}

// --- shared ------------------------------------------------------------------

// runExec validates the connection and dispatches to execDo with a real session.
func runExec(cc *clikit.Context, conn *config.Conn, req mdriver.ExecRequest) error {
	if err := conn.Validate(); err != nil {
		return clikit.Fail(clikit.ExitUsage, "BAD_CONN", err.Error(), "")
	}
	return execDo(cc, conn.NewSession(), req)
}

// execDo runs req under the engineError-capturing trap and renders the result:
// a §7 fault becomes an ok=false envelope with engineError (exit 5); otherwise
// {stdout, status}.
func execDo(cc *clikit.Context, sess *transport.Session, req mdriver.ExecRequest) error {
	res, err := sess.ExecTrapped(context.Background(), req)
	if err != nil {
		return runtimeErr(err)
	}
	if res.EngineError != nil {
		msg := res.EngineError.Mnemonic
		if res.EngineError.Text != "" {
			msg = strings.TrimSpace(msg + " " + res.EngineError.Text)
		}
		return clikit.FailEngine(clikit.ExitRuntime, "ENGINE_ERROR", msg, "", toClikitEngineError(res.EngineError))
	}
	return cc.Result(execResult{Stdout: res.Stdout, Status: res.Status}, func() {
		if res.Stdout != "" {
			fmt.Fprint(cc.Stdout, res.Stdout)
			if !strings.HasSuffix(res.Stdout, "\n") {
				fmt.Fprintln(cc.Stdout)
			}
		}
		fmt.Fprintln(cc.Stdout, cc.Faint(fmt.Sprintf("status %d", res.Status)))
	})
}

// toClikitEngineError converts the SDK §7 fault to clikit's own copy (drivers
// convert at the envelope boundary — consistency-protocol).
func toClikitEngineError(e *mdriver.EngineError) *clikit.EngineError {
	if e == nil {
		return nil
	}
	return &clikit.EngineError{
		Routine:  e.Routine,
		Line:     e.Line,
		Mnemonic: e.Mnemonic,
		Text:     e.Text,
	}
}
