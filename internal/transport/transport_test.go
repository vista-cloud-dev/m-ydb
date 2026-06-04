package transport

import (
	"context"
	"reflect"
	"testing"
)

// The Fake and the two real strategies must all satisfy Transport. A compile
// failure here is the first signal the interface shape drifted.
var (
	_ Transport = (*Fake)(nil)
	_ Transport = (*Session)(nil)
)

// recordingRunner captures the last argv/env/stdin and returns canned output,
// so the session strategies' command construction is verified without a real
// engine (the in-strategy seam, distinct from the verb-level Transport).
type recordingRunner struct {
	out  CmdOutput
	err  error
	argv []string
	env  []string
	in   string
}

func (r *recordingRunner) run(_ context.Context, argv, env []string, stdin string) (CmdOutput, error) {
	r.argv, r.env, r.in = argv, env, stdin
	return r.out, r.err
}

func TestLocal_Health_Argv(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "1", Code: 0}}
	s := NewSession(Config{Transport: "local", Dist: "/opt/yottadb", GblDir: "/data/m.gld", Routines: "/src"}, rr.run)

	h, err := s.Health(context.Background())
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if !h.Running || !h.Healthy {
		t.Errorf("health = %+v, want running+healthy", h)
	}
	// Readiness probe is `yottadb -run %XCMD 'write 1'` (plan §3).
	want := []string{"/opt/yottadb/yottadb", "-run", "%XCMD", "write 1"}
	if !reflect.DeepEqual(rr.argv, want) {
		t.Errorf("argv = %v, want %v", rr.argv, want)
	}
	// Local transport carries the YottaDB env (risk: env must reach the process).
	assertEnv(t, rr.env, "ydb_dist=/opt/yottadb", "ydb_gbldir=/data/m.gld", "ydb_routines=/src")
}

func TestLocal_Exec_CommandMode(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "hello\n", Code: 0}}
	s := NewSession(Config{Transport: "local", Dist: "/opt/yottadb"}, rr.run)

	res, err := s.Exec(context.Background(), ExecRequest{Mode: ExecCommand, Command: "write \"hello\",!"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.Stdout != "hello\n" {
		t.Errorf("stdout = %q, want %q", res.Stdout, "hello\n")
	}
	if res.EngineError != nil {
		t.Errorf("unexpected engineError: %+v", res.EngineError)
	}
	want := []string{"/opt/yottadb/yottadb", "-run", "%XCMD", "write \"hello\",!"}
	if !reflect.DeepEqual(rr.argv, want) {
		t.Errorf("argv = %v, want %v", rr.argv, want)
	}
}

func TestLocal_Exec_RoutineMode_ArgsToCmdline(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "ok", Code: 0}}
	s := NewSession(Config{Transport: "local"}, rr.run) // empty Dist → bare "yottadb" on PATH

	_, err := s.Exec(context.Background(), ExecRequest{Mode: ExecRoutine, EntryRef: "RUN^STDHARN", Args: []string{"zzt42", "VERBOSE"}})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	// `yottadb -run <entryref> arg1 arg2` — args become $ZCMDLINE.
	want := []string{"yottadb", "-run", "RUN^STDHARN", "zzt42", "VERBOSE"}
	if !reflect.DeepEqual(rr.argv, want) {
		t.Errorf("argv = %v, want %v", rr.argv, want)
	}
}

func TestLocal_Exec_ScriptMode_DirectWithHalt(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "", Code: 0}}
	s := NewSession(Config{Transport: "local"}, rr.run)

	_, err := s.Exec(context.Background(), ExecRequest{Mode: ExecScript, Script: "set x=1\nwrite x"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	want := []string{"yottadb", "-direct"}
	if !reflect.DeepEqual(rr.argv, want) {
		t.Errorf("argv = %v, want %v", rr.argv, want)
	}
	// Direct-mode scripts are fed on stdin and MUST end with halt (risk: hung session).
	wantIn := "set x=1\nwrite x\nhalt\n"
	if rr.in != wantIn {
		t.Errorf("stdin = %q, want %q", rr.in, wantIn)
	}
}

func TestDocker_WrapsArgv(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "1", Code: 0}}
	s := NewSession(Config{Transport: "docker", Container: "m-test-engine"}, rr.run)

	if _, err := s.Health(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
	// docker transport prefixes `docker exec -i <container>` and uses the
	// container's yottadb on PATH (no host dist path).
	want := []string{"docker", "exec", "-i", "m-test-engine", "yottadb", "-run", "%XCMD", "write 1"}
	if !reflect.DeepEqual(rr.argv, want) {
		t.Errorf("argv = %v, want %v", rr.argv, want)
	}
}

func TestFake_RecordsAndReturns(t *testing.T) {
	f := &Fake{
		ExecResults:  []ExecResult{{Stdout: "canned", Status: 0}},
		HealthResult: HealthResult{Running: true, Healthy: true, Version: "r2.02"},
	}
	res, err := f.Exec(context.Background(), ExecRequest{Mode: ExecCommand, Command: "write 1"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.Stdout != "canned" {
		t.Errorf("stdout = %q, want canned", res.Stdout)
	}
	if len(f.ExecCalls) != 1 || f.ExecCalls[0].Command != "write 1" {
		t.Errorf("ExecCalls = %+v, want one recorded call", f.ExecCalls)
	}
	h, _ := f.Health(context.Background())
	if h.Version != "r2.02" {
		t.Errorf("health version = %q, want r2.02", h.Version)
	}
}

func assertEnv(t *testing.T, env []string, want ...string) {
	t.Helper()
	set := map[string]bool{}
	for _, e := range env {
		set[e] = true
	}
	for _, w := range want {
		if !set[w] {
			t.Errorf("env missing %q (got %v)", w, env)
		}
	}
}
