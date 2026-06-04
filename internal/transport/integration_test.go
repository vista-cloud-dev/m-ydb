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

// TestRealExecRunStaged stages a routine into a scratch dir inside the container,
// points $ydb_routines at it (via execWrap's -e injection), and runs its
// entryref — validating exec run end-to-end (auto-compile + execution) plus the
// docker routines-env wiring.
func TestRealExecRunStaged(t *testing.T) {
	if os.Getenv("M_YDB_IT") != "1" {
		t.Skip("gated: set M_YDB_IT=1 (+ a running YottaDB container) to run the real-engine tier")
	}
	container := os.Getenv("M_YDB_CONTAINER")
	if container == "" {
		container = "m-test-engine"
	}
	const dir = "/tmp/m-ydb-it-run"
	// Stage ZZTHI.m with a `hi` label that writes a sentinel line. printf '%b'
	// interprets the \n escapes into real newlines.
	stage := NewSession(Config{Transport: "docker", Container: container}, nil)
	routine := `ZZTHI ;\nhi w "HI42",! q\n`
	setup := `rm -rf '` + dir + `'; mkdir -p '` + dir + `'; printf '%b' "$ZZT" > '` + dir + `/ZZTHI.m'`
	if _, code, err := stage.Sh(context.Background(),
		`ZZT='`+routine+`'; `+setup); err != nil || code != 0 {
		t.Fatalf("stage: code=%d err=%v", code, err)
	}
	t.Cleanup(func() { _, _, _ = stage.Sh(context.Background(), `rm -rf '`+dir+`'`) })

	// A session whose routines path is the scratch dir → exec run finds ZZTHI.
	s := NewSession(Config{Transport: "docker", Container: container, Routines: dir}, nil)
	res, err := s.ExecTrapped(context.Background(), mdriver.ExecRequest{EntryRef: "hi^ZZTHI"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.EngineError != nil {
		t.Fatalf("run surfaced engineError: %+v (stdout %q)", res.EngineError, res.Stdout)
	}
	if !strings.Contains(res.Stdout, "HI42") {
		t.Errorf("run stdout = %q, want it to contain HI42", res.Stdout)
	}
	t.Logf("real exec run of staged routine OK: %q", strings.TrimSpace(res.Stdout))
}

// TestRealCompileError validates that a ZLINK of a syntactically-broken routine
// surfaces a structured compile fault — the engineError path `exec load` relies
// on to report compile errors.
func TestRealCompileError(t *testing.T) {
	if os.Getenv("M_YDB_IT") != "1" {
		t.Skip("gated: set M_YDB_IT=1 (+ a running YottaDB container) to run the real-engine tier")
	}
	container := os.Getenv("M_YDB_CONTAINER")
	if container == "" {
		container = "m-test-engine"
	}
	const dir = "/tmp/m-ydb-it-bad"
	stage := NewSession(Config{Transport: "docker", Container: container}, nil)
	bad := `ZZTBAD ;\nbad zzznotacommand\n`
	if _, code, err := stage.Sh(context.Background(),
		`ZZT='`+bad+`'; rm -rf '`+dir+`'; mkdir -p '`+dir+`'; printf '%b' "$ZZT" > '`+dir+`/ZZTBAD.m'`); err != nil || code != 0 {
		t.Fatalf("stage: code=%d err=%v", code, err)
	}
	t.Cleanup(func() { _, _, _ = stage.Sh(context.Background(), `rm -rf '`+dir+`'`) })

	s := NewSession(Config{Transport: "docker", Container: container, Routines: dir}, nil)
	ee, err := s.Compile(context.Background(), []string{"ZZTBAD"})
	if err != nil {
		t.Fatalf("compile: %v", err)
	}
	if ee == nil {
		t.Fatal("broken routine compiled clean?")
	}
	if !strings.Contains(ee.Mnemonic, "-E-") {
		t.Errorf("mnemonic = %q, want an error-severity code", ee.Mnemonic)
	}
	t.Logf("real compile engineError: routine=%q line=%d mnemonic=%q text=%q",
		ee.Routine, ee.Line, ee.Mnemonic, ee.Text)
}

// TestRealAbortNoop validates the abort path against the real container with no
// matching job: pgrep must be available and the no-match case a clean no-op.
func TestRealAbortNoop(t *testing.T) {
	if os.Getenv("M_YDB_IT") != "1" {
		t.Skip("gated: set M_YDB_IT=1 (+ a running YottaDB container) to run the real-engine tier")
	}
	container := os.Getenv("M_YDB_CONTAINER")
	if container == "" {
		container = "m-test-engine"
	}
	s := NewSession(Config{Transport: "docker", Container: container}, nil)
	killed, err := s.Abort(context.Background(), "zztnomatch")
	if err != nil {
		t.Fatalf("abort no-op: %v", err)
	}
	if len(killed) != 0 {
		t.Errorf("killed = %v, want none", killed)
	}
}
