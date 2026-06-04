package main

import (
	"bytes"
	"context"
	"errors"
	"testing"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
	"github.com/vista-cloud-dev/m-ydb/clikit"
	"github.com/vista-cloud-dev/m-ydb/internal/transport"
)

// fakeSession builds a local-transport Session whose OS runner is replaced by
// the given canned output, so the exec command glue is testable without an
// engine.
func fakeSession(out transport.CmdOutput) *transport.Session {
	return transport.NewSession(
		transport.Config{Transport: "local"},
		func(_ context.Context, _, _ []string, _ string) (transport.CmdOutput, error) {
			return out, nil
		},
	)
}

func TestExecDo_Success(t *testing.T) {
	var buf bytes.Buffer
	sess := fakeSession(transport.CmdOutput{Stdout: "hello\n", Code: 0})
	if err := execDo(jsonCtx(&buf, "exec eval"), sess, mdriver.ExecRequest{Command: "w \"hello\",!"}); err != nil {
		t.Fatalf("execDo: %v", err)
	}
	env := decodeEnvelope(t, buf.Bytes())
	if !env.OK || env.Exit != clikit.ExitOK {
		t.Fatalf("ok=%v exit=%d, want ok=true exit=0", env.OK, env.Exit)
	}
	d, _ := env.Data.(map[string]any)
	if d["stdout"] != "hello\n" {
		t.Errorf("data.stdout = %v, want hello", d["stdout"])
	}
}

func TestExecDo_EngineErrorSurfaced(t *testing.T) {
	var buf bytes.Buffer
	out := transport.CmdOutput{
		Stdout: "\x01150373850,xeq+1^FOO,%YDB-E-UNDEF,Undefined local variable: x\x01",
		Code:   1,
	}
	sess := fakeSession(out)
	err := execDo(jsonCtx(&buf, "exec eval"), sess, mdriver.ExecRequest{Command: "w x"})
	if err == nil {
		t.Fatal("expected an error for a faulting exec")
	}
	var ce *clikit.Error
	if !errors.As(err, &ce) {
		t.Fatalf("want *clikit.Error, got %T", err)
	}
	if ce.Exit != clikit.ExitRuntime {
		t.Errorf("exit = %d, want %d", ce.Exit, clikit.ExitRuntime)
	}
	if ce.Engine == nil || ce.Engine.Mnemonic != "%YDB-E-UNDEF" || ce.Engine.Routine != "FOO" {
		t.Errorf("engineError = %+v", ce.Engine)
	}
}
