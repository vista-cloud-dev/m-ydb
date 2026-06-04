package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/vista-cloud-dev/m-ydb/clikit"
)

// jsonCtx builds a Context that renders the JSON envelope into buf. Only the
// exported fields matter in JSON mode (styling is text-mode only).
func jsonCtx(buf *bytes.Buffer, command string) *clikit.Context {
	return &clikit.Context{Stdout: buf, Stderr: buf, Format: clikit.FormatJSON, Command: command}
}

func decodeEnvelope(t *testing.T, b []byte) clikit.Envelope {
	t.Helper()
	var env clikit.Envelope
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatalf("unmarshal envelope: %v\n%s", err, b)
	}
	return env
}

func TestCapsCmd_Envelope(t *testing.T) {
	var buf bytes.Buffer
	if err := (capsCmd{}).Run(jsonCtx(&buf, "caps")); err != nil {
		t.Fatalf("caps run: %v", err)
	}
	env := decodeEnvelope(t, buf.Bytes())
	if !env.OK || env.Exit != clikit.ExitOK {
		t.Errorf("ok=%v exit=%d, want ok=true exit=0", env.OK, env.Exit)
	}
	if env.Command != "caps" {
		t.Errorf("command = %q, want caps", env.Command)
	}
	data, _ := env.Data.(map[string]any)
	if data["engine"] != "ydb" {
		t.Errorf("data.engine = %v, want ydb", data["engine"])
	}
	if data["contract"] != "1.0" {
		t.Errorf("data.contract = %v, want 1.0", data["contract"])
	}
}

func TestVersionCmd_Envelope(t *testing.T) {
	var buf bytes.Buffer
	if err := (versionCmd{}).Run(jsonCtx(&buf, "version")); err != nil {
		t.Fatalf("version run: %v", err)
	}
	env := decodeEnvelope(t, buf.Bytes())
	if !env.OK {
		t.Errorf("ok = false, want true")
	}
	data, _ := env.Data.(map[string]any)
	if data["engine"] != "ydb" {
		t.Errorf("data.engine = %v, want ydb", data["engine"])
	}
	if data["contract"] != "1.0" {
		t.Errorf("data.contract = %v, want 1.0", data["contract"])
	}
	if _, ok := data["driver"]; !ok {
		t.Error("data.driver missing")
	}
}
