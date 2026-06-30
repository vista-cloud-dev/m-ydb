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
	"strconv"
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
// for local the three $ydb_* paths locate the install and database; for remote
// the SSH fields locate a network-only host and EnvFile sources its YottaDB env.
type Config struct {
	Transport string // "local" | "docker" | "remote"
	Dist      string // $ydb_dist (dir containing the yottadb binary; honored on local + remote)
	GblDir    string // $ydb_gbldir (.gld global directory)
	Routines  string // $ydb_routines (source/object search path)
	Container string // docker: container name

	// remote (SSH) transport — for a network-only YottaDB VistA (e.g. a FOIA
	// `vehu` container reachable on :22 but not exec-able locally). The same
	// yottadb argv is wrapped in `ssh`; the engine env is sourced on the far side.
	Host     string // ssh target host
	Port     int    // ssh port (0 → ssh default 22)
	User     string // ssh user (empty → ssh default / current user)
	Identity string // ssh -i identity file (optional)
	EnvFile  string // remote file to `source` for the YottaDB env (e.g. /home/vehu/etc/env)
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
func (s *Session) isRemote() bool { return s.cfg.Transport == mdriver.TransportRemote }

// yottabin resolves the yottadb invocation: the container's PATH binary under
// docker, or $ydb_dist/yottadb (bare "yottadb" if Dist is unset) for local and
// remote — over SSH a Dist locates the binary when the env file does not put it
// on PATH.
func (s *Session) yottabin() string {
	if s.isDocker() {
		return "yottadb"
	}
	if s.cfg.Dist != "" {
		return filepath.Join(s.cfg.Dist, "yottadb")
	}
	return "yottadb"
}

// env returns the YottaDB environment for a local invocation; docker sources the
// container's preconfigured env via the login shell wrap() opens and remote
// sources EnvFile on the far side, so both return nil.
func (s *Session) env() []string {
	if s.isDocker() || s.isRemote() {
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

// wrap adapts the engine argv to the active transport: `docker exec -i
// <container> bash -lc` for docker, an `ssh` invocation for remote, or the bare
// argv for local.
//
// Docker goes through a login shell so the container's preconfigured engine env
// (gtmgbldir/gtmroutines/gtm_dist) is sourced — a bare `docker exec yottadb …`
// runs non-login and leaves them undefined, faulting %YDB-E-ZGBLDIRUNDEF on a
// GT.M-configured VistA like `vehu` (its env lives in a login profile, not the
// default docker-exec environment). This mirrors m-cli's DockerRunner, which
// already drives the same containers via `bash -lc`. The runtime $ZGBLDIR/
// $ZROUTINES injection in exec.go still layers an explicit --gbldir/--routines
// (or a staged routine dir) on top of whatever the login shell resolved.
func (s *Session) wrap(argv []string) []string {
	switch {
	case s.isDocker():
		return []string{"docker", "exec", "-i", s.cfg.Container, "bash", "-lc", shJoin(argv)}
	case s.isRemote():
		return s.sshWrap(argv)
	default:
		return argv
	}
}

// sshWrap turns an engine argv into an `ssh` invocation whose single remote
// command sources the instance env file (if set) and then runs the argv. The
// remote command is shell-quoted for the far-side shell; local stdin is
// forwarded by ssh to the remote command (so direct-mode scripts work).
func (s *Session) sshWrap(argv []string) []string {
	remote := shJoin(argv)
	if s.cfg.EnvFile != "" {
		remote = ". " + shToken(s.cfg.EnvFile) + " && " + remote
	}
	ssh := []string{"ssh"}
	if s.cfg.Port != 0 {
		ssh = append(ssh, "-p", strconv.Itoa(s.cfg.Port))
	}
	if s.cfg.Identity != "" {
		ssh = append(ssh, "-i", s.cfg.Identity)
	}
	// BatchMode prevents an interactive password prompt from hanging a CI run.
	ssh = append(ssh, "-o", "BatchMode=yes")
	target := s.cfg.Host
	if s.cfg.User != "" {
		target = s.cfg.User + "@" + s.cfg.Host
	}
	return append(ssh, target, remote)
}

// shSafe matches tokens that need no quoting for a POSIX shell.
var shSafe = regexp.MustCompile(`^[A-Za-z0-9_@%:=+,./-]+$`)

// shToken single-quotes a token unless it is already shell-safe (so common
// argv like `yottadb`, `-run`, `%XCMD`, and plain paths stay readable). A `$`
// inside single quotes is literal — exactly what %XCMD wants for `W $ZV`.
func shToken(s string) string {
	if s != "" && shSafe.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shJoin shell-quotes each argv element and joins them with spaces.
func shJoin(argv []string) string {
	parts := make([]string, len(argv))
	for i, a := range argv {
		parts[i] = shToken(a)
	}
	return strings.Join(parts, " ")
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

// ContainerRoutines returns the engine's own $ZROUTINES as resolved inside the
// container — the source axis's fallback when the host config pins no
// --routines/$ydb_routines. A real VistA image (vehu) keeps its routine source
// path in the container's engine profile (which wrap()'s `bash -lc` login shell
// sources), not the host environment, so the source store could not otherwise
// find a dir to stage into. Docker-only (returns "" for local/remote, where the
// host env/EnvFile already supplies the path). The returned `object*(src …)`
// form is exactly what source.ParseRoutinesDirs consumes. With no --routines set,
// Exec layers no $ZROUTINES override, so this reads the login-shell value.
func (s *Session) ContainerRoutines(ctx context.Context) (string, error) {
	if !s.isDocker() {
		return "", nil
	}
	res, err := s.Exec(ctx, mdriver.ExecRequest{Command: "write $ZROUTINES"})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(res.Stdout), nil
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
// args and optional stdin (GDE reads its layout script from stdin), wrapped for
// the active transport. It is NOT part of the neutral Transport contract — these
// binaries are YottaDB-specific — so it lives on the concrete Session for
// lifecycle/admin/native use within m-ydb. Locally the binary resolves to
// $ydb_dist/<name>; under docker it runs in the container.
func (s *Session) Util(ctx context.Context, name string, args []string, stdin string) (CmdOutput, error) {
	bin := name
	if !s.isDocker() && s.cfg.Dist != "" {
		bin = filepath.Join(s.cfg.Dist, name)
	}
	argv := append([]string{bin}, args...)
	return s.run(ctx, s.wrap(argv), s.env(), stdin)
}

// Sh runs a /bin/sh -c script in the engine's filesystem context — on the host
// for local, inside the container for docker — and returns stdout + the exit
// code (a non-zero exit is a result, not an error). It is the seam the sync
// source store uses to list/read .m files under the docker transport; it is not
// part of the neutral Transport contract. The script must be self-contained
// (the caller quotes any interpolated paths).
func (s *Session) Sh(ctx context.Context, script string) (stdout string, code int, err error) {
	out, err := s.run(ctx, s.wrap([]string{"sh", "-c", script}), s.env(), "")
	if err != nil {
		return "", 0, err
	}
	return out.Stdout, out.Code, nil
}

// Docker runs a host `docker` command (e.g. start/stop/rm/run) for managing the
// container itself — distinct from `docker exec` (which Util/Exec use to run
// inside it). It is docker-transport-only.
func (s *Session) Docker(ctx context.Context, args ...string) (CmdOutput, error) {
	return s.run(ctx, append([]string{"docker"}, args...), nil, "")
}

// Container is the docker container name for this session (empty for local).
func (s *Session) Container() string { return s.cfg.Container }

// IsDocker reports whether this is the docker transport.
func (s *Session) IsDocker() bool { return s.isDocker() }

var releaseRe = regexp.MustCompile(`r\d+\.\d+`)

// Version returns the YottaDB release (e.g. "r2.02") from `yottadb -version`.
// It is the engine-version probe for status/info/doctor (the SDK Health probe
// stays a bare readiness check).
func (s *Session) Version(ctx context.Context) (string, error) {
	out, err := s.Util(ctx, "yottadb", []string{"-version"}, "")
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
