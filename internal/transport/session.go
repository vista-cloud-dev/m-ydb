// Package transport holds m-ydb's YottaDB-specific Transport strategies. The
// verb-level seam itself (the Transport interface and its request/result types)
// lives in the shared SDK (github.com/vista-cloud-dev/m-driver-sdk); this
// package implements it for the local and docker transports by piping M into a
// `yottadb` session and capturing stdout.
package transport

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// Session implements mdriver.Transport for local and docker. The docker variant
// wraps the same yottadb argv in `docker exec`.
var _ mdriver.Transport = (*Session)(nil)

// ErrNotImplemented marks a Transport verb whose milestone has not landed yet.
// The interface is frozen in the SDK; the YottaDB bodies fill in per milestone
// (Load → M2/M3, ReadGlobal/SetGlobal → M4).
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

// Session is the local/docker Transport.
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

func (s *Session) isDocker() bool { return s.cfg.Transport == mdriver.TransportDocker }

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

// buildExec turns an ExecRequest into a yottadb argv + stdin. The shape is
// selected by which field is set, with precedence Script > EntryRef > Command
// (mdriver.ExecRequest).
func (s *Session) buildExec(req mdriver.ExecRequest) (argv []string, stdin string) {
	bin := s.yottabin()
	switch {
	case req.Script != "":
		// Direct mode reads the script from stdin and MUST end with halt, else
		// the session hangs waiting for input.
		stdin = req.Script
		if !strings.HasSuffix(stdin, "\n") {
			stdin += "\n"
		}
		stdin += "halt\n"
		argv = []string{bin, "-direct"}
	case req.EntryRef != "":
		argv = append([]string{bin, "-run", req.EntryRef}, req.Args...)
		stdin = req.Stdin
	default:
		argv = []string{bin, "-run", "%XCMD", req.Command}
		stdin = req.Stdin
	}
	return argv, stdin
}

// Exec runs an M command/entryref/script. A non-zero engine exit is a result,
// not a transport error; $ZSTATUS-driven engineError parsing lands in M3.
func (s *Session) Exec(ctx context.Context, req mdriver.ExecRequest) (mdriver.ExecResult, error) {
	argv, stdin := s.buildExec(req)
	out, err := s.run(ctx, s.wrap(argv), s.env(), stdin)
	if err != nil {
		return mdriver.ExecResult{}, err
	}
	return mdriver.ExecResult{Stdout: out.Stdout, Status: out.Code}, nil
}

// Health runs the readiness probe `%XCMD 'write 1'` and reports ready when the
// engine echoes "1" (plan §3). Engine-version probing is added in M1.
func (s *Session) Health(ctx context.Context) (mdriver.Health, error) {
	res, err := s.Exec(ctx, mdriver.ExecRequest{Command: "write 1"})
	if err != nil {
		return mdriver.Health{}, err
	}
	ready := strings.TrimSpace(res.Stdout) == "1"
	return mdriver.Health{Running: ready, Healthy: ready}, nil
}

// Load — staging + compile lands in M2/M3.
func (s *Session) Load(context.Context, mdriver.LoadRequest) (mdriver.LoadResult, error) {
	return mdriver.LoadResult{}, ErrNotImplemented
}

// ReadGlobal — global read-back lands in M4 (and powers the M5 coverage read).
func (s *Session) ReadGlobal(context.Context, mdriver.GlobalRef) (mdriver.GlobalNode, error) {
	return mdriver.GlobalNode{}, ErrNotImplemented
}

// SetGlobal — global write (fixture seeding) lands in M4.
func (s *Session) SetGlobal(context.Context, string, string) error {
	return ErrNotImplemented
}

// Util runs a YottaDB utility (yottadb, mupip, gde, lke, dse) with the given
// args, wrapped for the active transport. It is NOT part of the neutral
// Transport contract — these binaries are YottaDB-specific — so it lives on the
// concrete Session for lifecycle/admin/native use within m-ydb. Locally the
// binary resolves to $ydb_dist/<name>; under docker it runs in the container.
func (s *Session) Util(ctx context.Context, name string, args []string) (CmdOutput, error) {
	bin := name
	if !s.isDocker() && s.cfg.Dist != "" {
		bin = filepath.Join(s.cfg.Dist, name)
	}
	argv := append([]string{bin}, args...)
	return s.run(ctx, s.wrap(argv), s.env(), "")
}

var releaseRe = regexp.MustCompile(`r\d+\.\d+`)

// Version returns the YottaDB release (e.g. "r2.02") from `yottadb -version`.
// It is the engine-version probe for status/info/doctor (the SDK Health probe
// stays a bare readiness check).
func (s *Session) Version(ctx context.Context) (string, error) {
	out, err := s.Util(ctx, "yottadb", []string{"-version"})
	if err != nil {
		return "", err
	}
	if m := releaseRe.FindString(out.Stdout); m != "" {
		return m, nil
	}
	return "", fmt.Errorf("transport: could not parse YottaDB version from %q", strings.TrimSpace(out.Stdout))
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
