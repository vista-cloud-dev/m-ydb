package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
	"github.com/vista-cloud-dev/m-ydb/clikit"
	"github.com/vista-cloud-dev/m-ydb/internal/config"
	"github.com/vista-cloud-dev/m-ydb/internal/transport"
)

func newLocalConn(routines string) *config.Conn {
	return &config.Conn{Transport: "local", Routines: routines}
}

func writeTmp(dir, name, body string) error {
	return os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644)
}

func fileExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, name))
	return err == nil
}

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

func TestExecLoad_StagesFromDir(t *testing.T) {
	stageDir := t.TempDir() // instance source dir (dirs[0])
	from := t.TempDir()
	for _, n := range []string{"FOO.m", "BAR.m"} {
		if err := writeTmp(from, n, n+" ;x\n q\n"); err != nil {
			t.Fatal(err)
		}
	}
	conn := newLocalConn(stageDir)
	var buf bytes.Buffer
	c := &execLoadCmd{From: from, NoCompile: true}
	if err := c.Run(jsonCtx(&buf, "exec load"), conn); err != nil {
		t.Fatalf("load: %v", err)
	}
	d, _ := decodeEnvelope(t, buf.Bytes()).Data.(map[string]any)
	if loaded, _ := d["loaded"].([]any); len(loaded) != 2 {
		t.Errorf("loaded = %v, want 2", d["loaded"])
	}
	for _, n := range []string{"FOO.m", "BAR.m"} {
		if !fileExists(stageDir, n) {
			t.Errorf("%s not staged into the instance dir", n)
		}
	}
}

func TestExecLoad_CompileErrorSurfaced(t *testing.T) {
	// A YottaDB compile fault is a stderr listing (exit 0), parsed by Compile.
	out := transport.CmdOutput{
		Stderr: " bad zzznotacommand\n     ^-----\nAt column 5, line 2, source module /r/FOO.m\n%YDB-E-INVCMD, Invalid command keyword encountered\n",
		Code:   0,
	}
	sess := fakeSession(out)
	ee, err := sess.Compile(context.Background(), []string{"FOO"})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if ee == nil || ee.Mnemonic != "%YDB-E-INVCMD" || ee.Routine != "FOO" || ee.Line != 2 {
		t.Errorf("compile engineError = %+v, want INVCMD at FOO line 2", ee)
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
