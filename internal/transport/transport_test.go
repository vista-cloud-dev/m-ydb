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
type recordedCall struct {
	argv []string
	env  []string
	in   string
}

type recordingRunner struct {
	out   CmdOutput
	err   error
	argv  []string // last call (convenience for single-call tests)
	env   []string
	in    string
	calls []recordedCall // every call, in order (for multi-step ops)
	// outs, when non-nil, supplies a per-call CmdOutput in sequence (else out).
	outs []CmdOutput
}

func (r *recordingRunner) run(_ context.Context, argv, env []string, stdin string) (CmdOutput, error) {
	r.argv, r.env, r.in = argv, env, stdin
	r.calls = append(r.calls, recordedCall{argv: argv, env: env, in: stdin})
	if r.outs != nil {
		i := len(r.calls) - 1
		if i < len(r.outs) {
			return r.outs[i], r.err
		}
	}
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
	// docker transport prefixes `docker exec -i <container> bash -lc` so the
	// container's preconfigured engine env (gtmgbldir/gtmroutines on a GT.M
	// VistA like vehu) is sourced by the login shell; the yottadb argv is
	// shell-joined into the -lc command string.
	want := []string{"docker", "exec", "-i", "m-test-engine", "bash", "-lc", "yottadb -run %XCMD 'write 1'"}
	if !reflect.DeepEqual(rr.argv, want) {
		t.Errorf("argv = %v, want %v", rr.argv, want)
	}
}

func TestLocal_Util_Argv(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "", Code: 0}}
	s := NewSession(Config{Transport: "local", Dist: "/opt/yottadb", GblDir: "/data/m.gld"}, rr.run)

	if _, err := s.Util(context.Background(), "mupip", []string{"rundown", "-region", "*"}, ""); err != nil {
		t.Fatalf("util: %v", err)
	}
	// A YDB utility resolves to $ydb_dist/<util> locally and carries the env.
	want := []string{"/opt/yottadb/mupip", "rundown", "-region", "*"}
	if !reflect.DeepEqual(rr.argv, want) {
		t.Errorf("argv = %v, want %v", rr.argv, want)
	}
	assertEnv(t, rr.env, "ydb_dist=/opt/yottadb", "ydb_gbldir=/data/m.gld")
}

func TestUtil_FeedsStdin(t *testing.T) {
	rr := &recordingRunner{}
	s := NewSession(Config{Transport: "local", Dist: "/opt/yottadb"}, rr.run)
	if _, err := s.Util(context.Background(), "gde", nil, "change -segment DEFAULT -file=m.dat\nexit\n"); err != nil {
		t.Fatalf("util: %v", err)
	}
	if rr.argv[0] != "/opt/yottadb/gde" {
		t.Errorf("argv0 = %q, want /opt/yottadb/gde", rr.argv[0])
	}
	if rr.in != "change -segment DEFAULT -file=m.dat\nexit\n" {
		t.Errorf("gde stdin = %q", rr.in)
	}
}

func TestDocker_Util_Argv(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "", Code: 0}}
	s := NewSession(Config{Transport: "docker", Container: "m-test-engine"}, rr.run)

	if _, err := s.Util(context.Background(), "gde", nil, ""); err != nil {
		t.Fatalf("util: %v", err)
	}
	want := []string{"docker", "exec", "-i", "m-test-engine", "bash", "-lc", "gde"}
	if !reflect.DeepEqual(rr.argv, want) {
		t.Errorf("argv = %v, want %v", rr.argv, want)
	}
}

func TestDocker_HostCommand(t *testing.T) {
	rr := &recordingRunner{}
	s := NewSession(Config{Transport: "docker", Container: "m-test-engine"}, rr.run)
	if _, err := s.Docker(context.Background(), "stop", "m-test-engine"); err != nil {
		t.Fatalf("docker: %v", err)
	}
	want := []string{"docker", "stop", "m-test-engine"}
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

// --- remote (SSH) transport --------------------------------------------------
//
// The remote transport reaches a network-only YottaDB VistA (e.g. a FOIA `vehu`
// container that exposes :22 but no engine-local shell to us) by wrapping the
// same yottadb argv in `ssh`, sourcing the instance env file on the far side.
// These verify the ssh argv + remote-command construction without a real host
// (the in-strategy seam); live SSH is the gated integration tier.

func TestRemote_Health_Argv(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "1", Code: 0}}
	s := NewSession(Config{
		Transport: mdriver.TransportRemote,
		Host:      "vehu.local", Port: 2222, User: "vehu",
		EnvFile: "/home/vehu/etc/env",
	}, rr.run)

	h, err := s.Health(context.Background())
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if !h.Running || !h.Healthy {
		t.Errorf("health = %+v, want running+healthy", h)
	}
	// `ssh -p 2222 -o BatchMode=yes vehu@vehu.local '. <envfile> && yottadb -run %XCMD <cmd>'`.
	// The env file is sourced remotely; the probe command "write 1" is shell-quoted.
	want := []string{
		"ssh", "-p", "2222", "-o", "BatchMode=yes", "vehu@vehu.local",
		". /home/vehu/etc/env && yottadb -run %XCMD 'write 1'",
	}
	if !reflect.DeepEqual(rr.argv, want) {
		t.Errorf("argv =\n  %v\nwant\n  %v", rr.argv, want)
	}
	// Remote env is sourced on the far side, never pushed as local process env.
	if len(rr.env) != 0 {
		t.Errorf("env = %v, want nil (sourced via EnvFile)", rr.env)
	}
}

func TestRemote_Exec_DollarZV(t *testing.T) {
	// The T0.1 gate: `W $ZV` over the network. `$` is literal inside the single
	// quotes the remote sh strips, so %XCMD receives `W $ZV` as one argument.
	rr := &recordingRunner{out: CmdOutput{Stdout: "YottaDB r2.02\n", Code: 0}}
	s := NewSession(Config{
		Transport: mdriver.TransportRemote,
		Host:      "h", User: "u", EnvFile: "/env",
	}, rr.run)

	res, err := s.Exec(context.Background(), mdriver.ExecRequest{Command: "W $ZV"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.Stdout == "" {
		t.Error("want $ZV output")
	}
	want := []string{
		"ssh", "-o", "BatchMode=yes", "u@h",
		". /env && yottadb -run %XCMD 'W $ZV'",
	}
	if !reflect.DeepEqual(rr.argv, want) {
		t.Errorf("argv =\n  %v\nwant\n  %v", rr.argv, want)
	}
}

func TestRemote_Exec_ScriptMode_StdinForwarded(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Code: 0}}
	s := NewSession(Config{
		Transport: mdriver.TransportRemote,
		Host:      "vehu.local",
		Identity:  "/keys/id_ed25519",
	}, rr.run)

	if _, err := s.Exec(context.Background(), mdriver.ExecRequest{Script: "set x=1\nwrite x"}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	// No EnvFile → bare remote command; no User → host-only target; Identity → -i;
	// no Port → default (omit -p).
	want := []string{
		"ssh", "-i", "/keys/id_ed25519", "-o", "BatchMode=yes", "vehu.local",
		"yottadb -direct",
	}
	if !reflect.DeepEqual(rr.argv, want) {
		t.Errorf("argv =\n  %v\nwant\n  %v", rr.argv, want)
	}
	// Direct-mode script is forwarded on stdin (ssh pipes local stdin to the
	// remote command) and MUST end with halt or the remote session hangs.
	if rr.in != "set x=1\nwrite x\nhalt\n" {
		t.Errorf("stdin = %q, want %q", rr.in, "set x=1\nwrite x\nhalt\n")
	}
}

func TestRemote_HonorsDistAndVersion(t *testing.T) {
	// Version (a Util call) also rides the ssh wrap; Dist locates the binary on
	// the far side when the env file does not put yottadb on PATH.
	rr := &recordingRunner{out: CmdOutput{Stdout: "YottaDB release r2.02\n", Code: 0}}
	s := NewSession(Config{
		Transport: mdriver.TransportRemote,
		Host:      "h", User: "u", Dist: "/opt/yottadb",
	}, rr.run)

	v, err := s.Version(context.Background())
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if v != "r2.02" {
		t.Errorf("version = %q, want r2.02", v)
	}
	want := []string{"ssh", "-o", "BatchMode=yes", "u@h", "/opt/yottadb/yottadb -version"}
	if !reflect.DeepEqual(rr.argv, want) {
		t.Errorf("argv =\n  %v\nwant\n  %v", rr.argv, want)
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
