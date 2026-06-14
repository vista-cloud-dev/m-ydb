package clikit

import (
	"bytes"
	"encoding/json"
	"testing"
)

// ResultExit emits the data envelope with a deliberate exit code so the stdout
// envelope's exit matches the process exit (the driver-contract §2 invariant the
// conformance suite enforces). This is the "data + non-zero exit" path for verbs
// like doctor whose payload is still a full result on a non-zero outcome.

func TestResultExit_JSONEnvelopeMatchesExit(t *testing.T) {
	var buf bytes.Buffer
	c := &Context{Stdout: &buf, Format: FormatJSON, Command: "meta doctor"}
	type doc struct {
		N int `json:"n"`
	}
	if err := c.ResultExit(doc{N: 3}, ExitUnreachable, nil); err != nil {
		t.Fatalf("ResultExit: %v", err)
	}
	if c.ExitCode() != ExitUnreachable {
		t.Errorf("ExitCode() = %d, want %d", c.ExitCode(), ExitUnreachable)
	}
	var env Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.OK {
		t.Error("ok must be false for a non-zero exit")
	}
	if env.Exit != ExitUnreachable {
		t.Errorf("envelope.exit = %d, want %d", env.Exit, ExitUnreachable)
	}
	if env.Data == nil {
		t.Error("data must be present (the payload is the result)")
	}
}

func TestResultExit_ZeroExitIsOK(t *testing.T) {
	var buf bytes.Buffer
	c := &Context{Stdout: &buf, Format: FormatJSON, Command: "meta doctor"}
	if err := c.ResultExit(struct{}{}, ExitOK, nil); err != nil {
		t.Fatalf("ResultExit: %v", err)
	}
	if c.ExitCode() != ExitOK {
		t.Errorf("ExitCode() = %d, want 0", c.ExitCode())
	}
	var env Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !env.OK || env.Exit != ExitOK {
		t.Errorf("env = {ok:%v exit:%d}, want ok+exit0", env.OK, env.Exit)
	}
}

// A plain Result leaves the exit at 0 (back-compat: Run returns ExitOK).
func TestResult_DefaultExitZero(t *testing.T) {
	var buf bytes.Buffer
	c := &Context{Stdout: &buf, Format: FormatJSON, Command: "x"}
	_ = c.Result(struct{}{}, nil)
	if c.ExitCode() != ExitOK {
		t.Errorf("ExitCode() = %d, want 0", c.ExitCode())
	}
}
