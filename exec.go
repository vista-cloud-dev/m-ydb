package main

import (
	"context"
	"fmt"
	"strings"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
	"github.com/vista-cloud-dev/m-ydb/clikit"
	"github.com/vista-cloud-dev/m-ydb/internal/config"
	"github.com/vista-cloud-dev/m-ydb/internal/transport"
)

// execCmd is the exec axis (driver-contract §5.3) — run M code against the
// instance. run/eval execute an entryref / a single command under a $ETRAP that
// surfaces a structured engineError ($ZSTATUS, §7) on any fault. load (stage +
// compile) and abort (cancel by --prefix) land in later M3 slices; caps grows to
// advertise each as it does.
type execCmd struct {
	Run  execRunCmd  `cmd:"" name:"run" help:"Run an entryref (LABEL^ROUTINE); args → $ZCMDLINE. Faults surface as engineError."`
	Eval execEvalCmd `cmd:"" name:"eval" help:"Evaluate a single M command. Faults surface as engineError."`
}

type execResult struct {
	Stdout string `json:"stdout"`
	Status int    `json:"status"`
}

// --- run ---------------------------------------------------------------------

type execRunCmd struct {
	EntryRef string   `arg:"" help:"Entryref to run (LABEL^ROUTINE or ^ROUTINE)."`
	Args     []string `arg:"" optional:"" help:"Arguments, joined into $ZCMDLINE."`
}

func (c *execRunCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	return runExec(cc, conn, mdriver.ExecRequest{EntryRef: c.EntryRef, Args: c.Args})
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
