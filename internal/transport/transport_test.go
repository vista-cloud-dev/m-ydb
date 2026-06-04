package transport

import (
	"context"
	"reflect"
	"testing"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// Session must satisfy the SDK Transport. A compile failure here is the first
// signal the interface shape drifted.
var _ mdriver.Transport = (*Session)(nil)

// recordingRunner captures the last argv/env/stdin and returns canned output,
// so the session strategy's command construction is verified without a real
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

	res, err := s.Exec(context.Background(), mdriver.ExecRequest{Command: "write \"hello\",!"})
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

	_, err := s.Exec(context.Background(), mdriver.ExecRequest{EntryRef: "RUN^STDHARN", Args: []string{"zzt42", "VERBOSE"}})
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

	_, err := s.Exec(context.Background(), mdriver.ExecRequest{Script: "set x=1\nwrite x"})
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

func TestLocal_Util_Argv(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "", Code: 0}}
	s := NewSession(Config{Transport: "local", Dist: "/opt/yottadb", GblDir: "/data/m.gld"}, rr.run)

	if _, err := s.Util(context.Background(), "mupip", []string{"rundown", "-region", "*"}); err != nil {
		t.Fatalf("util: %v", err)
	}
	// A YDB utility resolves to $ydb_dist/<util> locally and carries the env.
	want := []string{"/opt/yottadb/mupip", "rundown", "-region", "*"}
	if !reflect.DeepEqual(rr.argv, want) {
		t.Errorf("argv = %v, want %v", rr.argv, want)
	}
	assertEnv(t, rr.env, "ydb_dist=/opt/yottadb", "ydb_gbldir=/data/m.gld")
}

func TestDocker_Util_Argv(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "", Code: 0}}
	s := NewSession(Config{Transport: "docker", Container: "m-test-engine"}, rr.run)

	if _, err := s.Util(context.Background(), "gde", nil); err != nil {
		t.Fatalf("util: %v", err)
	}
	want := []string{"docker", "exec", "-i", "m-test-engine", "gde"}
	if !reflect.DeepEqual(rr.argv, want) {
		t.Errorf("argv = %v, want %v", rr.argv, want)
	}
}

func TestVersion_ParsesRelease(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{
		Stdout: "YottaDB release r2.02\nUpstream V7.0-005\n", Code: 0,
	}}
	s := NewSession(Config{Transport: "local", Dist: "/opt/yottadb"}, rr.run)

	v, err := s.Version(context.Background())
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if v != "r2.02" {
		t.Errorf("version = %q, want r2.02", v)
	}
	want := []string{"/opt/yottadb/yottadb", "-version"}
	if !reflect.DeepEqual(rr.argv, want) {
		t.Errorf("argv = %v, want %v", rr.argv, want)
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
