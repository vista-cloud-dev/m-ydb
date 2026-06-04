package transport

import (
	"context"
	"os"
	"strings"
	"testing"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// TestDockerEngine_RealHealthAndVersion exercises the docker transport against a
// real YottaDB container (the gated integration tier — plan §1). It is READ-ONLY
// (Health + Version + a trivial Exec); it never stops/destroys the shared
// container. Run with:
//
//	M_YDB_IT=1 M_YDB_CONTAINER=m-test-engine go test ./internal/transport/ -run RealHealth
func TestDockerEngine_RealHealthAndVersion(t *testing.T) {
	if os.Getenv("M_YDB_IT") != "1" {
		t.Skip("gated: set M_YDB_IT=1 (+ a running YottaDB container) to run the real-engine tier")
	}
	container := os.Getenv("M_YDB_CONTAINER")
	if container == "" {
		container = "m-test-engine"
	}
	s := NewSession(Config{Transport: "docker", Container: container}, nil)
	ctx := context.Background()

	h, err := s.Health(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if !h.Running || !h.Healthy {
		t.Fatalf("engine not healthy: %+v", h)
	}

	v, err := s.Version(ctx)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if v == "" {
		t.Error("version probe returned empty")
	}
	t.Logf("real YottaDB healthy, version %s", v)

	res, err := s.Exec(ctx, mdriver.ExecRequest{Command: "write 1"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.Stdout == "" {
		t.Error("exec produced no output")
	}
}

// TestRealExecTrapped validates the $ETRAP/$ZSTATUS engineError capture against
// real YottaDB: a clean eval returns its output with no fault, and a
// deliberately-broken eval surfaces a structured engineError (the M3 §7 path).
func TestRealExecTrapped(t *testing.T) {
	if os.Getenv("M_YDB_IT") != "1" {
		t.Skip("gated: set M_YDB_IT=1 (+ a running YottaDB container) to run the real-engine tier")
	}
	container := os.Getenv("M_YDB_CONTAINER")
	if container == "" {
		container = "m-test-engine"
	}
	s := NewSession(Config{Transport: "docker", Container: container}, nil)
	ctx := context.Background()

	// Clean eval: output captured, no fault.
	ok, err := s.ExecTrapped(ctx, mdriver.ExecRequest{Command: "write 1+1"})
	if err != nil {
		t.Fatalf("eval ok: %v", err)
	}
	if ok.EngineError != nil {
		t.Fatalf("clean eval reported engineError: %+v (stdout %q)", ok.EngineError, ok.Stdout)
	}
	if got := strings.TrimSpace(ok.Stdout); got != "2" {
		t.Errorf("eval 1+1 stdout = %q, want 2", got)
	}

	// Faulting eval: reading an undefined local must surface a structured fault.
	bad, err := s.ExecTrapped(ctx, mdriver.ExecRequest{Command: "write zzundef"})
	if err != nil {
		t.Fatalf("eval bad: %v", err)
	}
	if bad.EngineError == nil {
		t.Fatalf("faulting eval did not surface engineError (stdout %q, status %d)", bad.Stdout, bad.Status)
	}
	if !strings.Contains(bad.EngineError.Mnemonic, "UNDEF") {
		t.Errorf("mnemonic = %q, want an UNDEF code", bad.EngineError.Mnemonic)
	}
	t.Logf("real engineError: routine=%q line=%d mnemonic=%q text=%q",
		bad.EngineError.Routine, bad.EngineError.Line, bad.EngineError.Mnemonic, bad.EngineError.Text)
}
