package transport

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// ErrNotImplemented marks a Transport verb whose milestone has not landed yet.
// The interface is frozen at M0; the local/docker bodies fill in per milestone
// (Load/Compile → M2/M3, ReadGlobal → M4).
var ErrNotImplemented = errors.New("transport: verb not yet implemented")

// Config is the resolved connection for a session transport. For docker only
// Container is used (the container's preconfigured database + yottadb on PATH);
// for local the three $ydb_* paths locate the install and database.
type Config struct {
	Transport string // "local" | "docker"
	Dist      string // $ydb_dist (dir containing the yottadb binary)
	GblDir    string // $ydb_gbldir (.gld global directory)
	Routines  string // $ydb_routines (source/object search path)
	Container string // docker: container name
}

// CmdOutput is the captured result of one OS command (the in-strategy seam).
type CmdOutput struct {
	Stdout string
	Stderr string
	Code   int
}

// runFunc runs a prepared argv with env (K=V) and stdin, returning the captured
// output. It is the lower-level seam *inside* the session strategies — distinct
// from the verb-level Transport. Tests inject a fake runFunc to verify argv
// construction without a real engine; production uses osRun.
type runFunc func(ctx context.Context, argv, env []string, stdin string) (CmdOutput, error)

// Session is the local/docker Transport: it pipes M into a `yottadb` session
// and captures stdout. The docker variant wraps the same argv in `docker exec`.
type Session struct {
	cfg Config
	run runFunc
}

// NewSession builds a session transport. A nil run uses the real OS runner.
func NewSession(cfg Config, run runFunc) *Session {
	if run == nil {
		run = osRun
	}
	return &Session{cfg: cfg, run: run}
}

func (s *Session) isDocker() bool { return s.cfg.Transport == "docker" }

// yottabin resolves the yottadb invocation: the container's PATH binary under
// docker, or $ydb_dist/yottadb locally (bare "yottadb" if Dist is unset).
func (s *Session) yottabin() string {
	if s.isDocker() {
		return "yottadb"
	}
	if s.cfg.Dist != "" {
		return filepath.Join(s.cfg.Dist, "yottadb")
	}
	return "yottadb"
}

// env returns the YottaDB environment for a local invocation; docker relies on
// the container's preconfigured env, so it returns nil.
func (s *Session) env() []string {
	if s.isDocker() {
		return nil
	}
	var env []string
	if s.cfg.Dist != "" {
		env = append(env, "ydb_dist="+s.cfg.Dist)
	}
	if s.cfg.GblDir != "" {
		env = append(env, "ydb_gbldir="+s.cfg.GblDir)
	}
	if s.cfg.Routines != "" {
		env = append(env, "ydb_routines="+s.cfg.Routines)
	}
	return env
}

// wrap prefixes `docker exec -i <container>` under the docker transport.
func (s *Session) wrap(argv []string) []string {
	if s.isDocker() {
		return append([]string{"docker", "exec", "-i", s.cfg.Container}, argv...)
	}
	return argv
}

// buildExec turns an ExecRequest into a yottadb argv + stdin.
func (s *Session) buildExec(req ExecRequest) (argv []string, stdin string) {
	bin := s.yottabin()
	switch req.Mode {
	case ExecRoutine:
		argv = append([]string{bin, "-run", req.EntryRef}, req.Args...)
		stdin = req.Stdin
	case ExecScript:
		// Direct mode reads the script from stdin and MUST end with halt, else
		// the session hangs waiting for input.
		stdin = req.Script
		if !strings.HasSuffix(stdin, "\n") {
			stdin += "\n"
		}
		stdin += "halt\n"
		argv = []string{bin, "-direct"}
	default: // ExecCommand
		argv = []string{bin, "-run", "%XCMD", req.Command}
		stdin = req.Stdin
	}
	return argv, stdin
}

// Exec runs an M command/entryref/script. A non-zero engine exit is a result,
// not a transport error; $ZSTATUS-driven engineError parsing lands in M3.
func (s *Session) Exec(ctx context.Context, req ExecRequest) (ExecResult, error) {
	argv, stdin := s.buildExec(req)
	out, err := s.run(ctx, s.wrap(argv), s.env(), stdin)
	if err != nil {
		return ExecResult{}, err
	}
	return ExecResult{Stdout: out.Stdout, Status: out.Code}, nil
}

// Health runs the readiness probe `%XCMD 'write 1'` and reports ready when the
// engine echoes "1" (plan §3). Engine-version probing is added in M1.
func (s *Session) Health(ctx context.Context) (HealthResult, error) {
	res, err := s.Exec(ctx, ExecRequest{Mode: ExecCommand, Command: "write 1"})
	if err != nil {
		return HealthResult{}, err
	}
	ready := strings.TrimSpace(res.Stdout) == "1"
	return HealthResult{Running: ready, Healthy: ready}, nil
}

// Load — staging + compile lands in M2/M3.
func (s *Session) Load(context.Context, LoadRequest) (LoadResult, error) {
	return LoadResult{}, ErrNotImplemented
}

// Compile — explicit recompile lands in M3.
func (s *Session) Compile(context.Context, CompileRequest) (CompileResult, error) {
	return CompileResult{}, ErrNotImplemented
}

// ReadGlobal — global read-back lands in M4 (and powers the M5 coverage read).
func (s *Session) ReadGlobal(context.Context, GlobalRef) (GlobalResult, error) {
	return GlobalResult{}, ErrNotImplemented
}

// osRun is the production runner: it executes argv with the given env appended
// to the process environment and feeds stdin. A non-zero exit is returned as a
// CmdOutput code, not a Go error — only a failure to launch is an error.
func osRun(ctx context.Context, argv, env []string, stdin string) (CmdOutput, error) {
	if len(argv) == 0 {
		return CmdOutput{}, errors.New("transport: empty argv")
	}
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			code, err = ee.ExitCode(), nil
		}
	}
	return CmdOutput{Stdout: out.String(), Stderr: errb.String(), Code: code}, err
}
