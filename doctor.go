package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
	"github.com/vista-cloud-dev/m-ydb/clikit"
	"github.com/vista-cloud-dev/m-ydb/internal/config"
)

// minRelease is the oldest YottaDB release m-ydb supports, as major*100+minor
// ("r1.34" → 134). Older r1.2x/GT.M predate APIs m-ydb relies on.
const minRelease = 134

// doctorCmd is `meta doctor` (driver-contract §5.7, plan §3): typed preflight
// diagnostics — the first thing CI and `m new` run. Each check is independent
// and self-describing ({name, ok, detail, fix}); the exit code lets CI branch:
// 0 all green, 6 engine-unreachable (no binary / docker down), 5 a check failed.
type doctorCmd struct{}

// The doctor payload shapes are SDK-owned so m-ydb and m-iris emit identical
// JSON m-cli reads.
type (
	checkResult  = mdriver.Check
	doctorResult = mdriver.DoctorResult
)

// checks holds the doctor's dependencies, injected so each failure mode is
// unit-testable without a real engine/docker/filesystem (plan §3).
type checks struct {
	lookPath func(file string) (string, error)         // resolve a binary on PATH (exec.LookPath)
	version  func(ctx context.Context) (string, error) // engine release probe (yottadb -version)
	statRead func(path string) error                   // path exists + readable
	writable func(path string) error                   // path is writable
	dockerOK func(ctx context.Context) error           // docker daemon reachable
}

// realChecks builds the production dependency set bound to this connection.
func realChecks(conn *config.Conn) checks {
	s := conn.NewSession()
	return checks{
		lookPath: exec.LookPath,
		version:  s.Version,
		statRead: func(path string) error {
			f, err := os.Open(path)
			if err != nil {
				return err
			}
			return f.Close()
		},
		writable: func(path string) error {
			info, err := os.Stat(path)
			if err != nil {
				return err
			}
			dir := path
			if !info.IsDir() {
				dir = filepath.Dir(path)
			}
			f, err := os.CreateTemp(dir, ".m-ydb-doctor-*")
			if err != nil {
				return err
			}
			name := f.Name()
			_ = f.Close()
			return os.Remove(name)
		},
		dockerOK: func(ctx context.Context) error {
			return exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").Run()
		},
	}
}

func (doctorCmd) Run(cc *clikit.Context, conn *config.Conn) error {
	conn.ResolveEnv()
	if err := conn.Validate(); err != nil {
		return clikit.Fail(clikit.ExitUsage, "BAD_CONN", err.Error(), "")
	}
	res, exit := realChecks(conn).run(context.Background(), conn)
	if rerr := cc.Result(res, func() { renderDoctor(cc, res) }); rerr != nil {
		return rerr
	}
	switch exit {
	case clikit.ExitUnreachable:
		return clikit.Fail(clikit.ExitUnreachable, "UNREACHABLE",
			"engine unreachable — the YottaDB binary or docker is not available", "see the failing checks above")
	case clikit.ExitRuntime:
		return clikit.Fail(clikit.ExitRuntime, "PREFLIGHT_FAILED",
			"one or more preflight checks failed", "see the failing checks above")
	}
	return nil
}

var releaseRe = regexp.MustCompile(`r(\d+)\.(\d+)`)

// run executes the check matrix and returns the typed result + exit code. A
// missing binary / down docker is "unreachable" (6) — the engine can't be run at
// all; any other failed check is 5; all-green is 0.
func (k checks) run(ctx context.Context, conn *config.Conn) (doctorResult, int) {
	res := doctorResult{Transport: conn.Transport}
	var unreachable bool
	add := func(name string, ok bool, detail, fix string) {
		res.Checks = append(res.Checks, checkResult{Name: name, OK: ok, Detail: detail, Fix: fix})
	}

	if conn.Transport == "docker" {
		if err := k.dockerOK(ctx); err != nil {
			add("docker", false, "docker daemon not reachable: "+err.Error(), "start Docker / check the socket")
			unreachable = true
		} else {
			add("docker", true, "docker daemon reachable", "")
		}
	}

	// binary: locally the yottadb binary must resolve; under docker the host
	// docker client is the reachability gate (checked above) and the in-container
	// binary is proven by the version probe.
	if conn.Transport == "local" {
		name := "yottadb"
		if conn.Dist != "" {
			name = filepath.Join(conn.Dist, "yottadb")
		}
		if p, err := k.lookPath(name); err != nil {
			add("binary", false, "yottadb not found ("+name+")", "set --ydb-dist / $ydb_dist or put yottadb on PATH")
			unreachable = true
		} else {
			add("binary", true, p, "")
		}
	}

	// version: probe the engine release (works for both transports). A launch
	// failure here is also "unreachable".
	if v, err := k.version(ctx); err != nil {
		add("version", false, "could not probe version: "+err.Error(), "ensure the engine is installed and runnable")
		unreachable = true
	} else if ok, detail, fix := releaseOK(v); ok {
		add("version", true, detail, "")
	} else {
		add("version", false, detail, fix)
	}

	// gld / routines: filesystem checks meaningful for a local install (under
	// docker these live in the container; report not-applicable).
	if conn.Transport == "local" {
		if conn.GblDir == "" {
			add("gld", false, "no global directory set", "set --gbldir / $ydb_gbldir")
		} else if err := k.statRead(conn.GblDir); err != nil {
			add("gld", false, "global directory not readable: "+err.Error(), "create it (gde) or fix the path/permissions")
		} else {
			add("gld", true, conn.GblDir, "")
		}

		if conn.Routines == "" {
			add("routines", false, "no routines path set", "set --routines / $ydb_routines")
		} else if err := k.writable(firstPath(conn.Routines)); err != nil {
			add("routines", false, "routines path not writable: "+err.Error(), "fix permissions on the routines source dir")
		} else {
			add("routines", true, conn.Routines, "")
		}
	} else {
		loc := "managed by the container"
		if conn.Transport == "remote" {
			loc = "managed on the remote host"
		}
		add("gld", true, loc, "")
		add("routines", true, loc, "")
	}

	res.OK = true
	for _, c := range res.Checks {
		if !c.OK {
			res.OK = false
			break
		}
	}
	switch {
	case unreachable:
		return res, clikit.ExitUnreachable
	case !res.OK:
		return res, clikit.ExitRuntime
	default:
		return res, clikit.ExitOK
	}
}

// releaseOK parses "rMAJOR.MINOR" and checks it meets the supported minimum.
func releaseOK(v string) (ok bool, detail, fix string) {
	m := releaseRe.FindStringSubmatch(v)
	if m == nil {
		return true, "could not parse version " + strconv.Quote(v) + " — accepting", ""
	}
	major, _ := strconv.Atoi(m[1])
	minor, _ := strconv.Atoi(m[2])
	if major*100+minor < minRelease {
		return false, fmt.Sprintf("YottaDB %s is older than the supported minimum r1.34", m[0]),
			"upgrade YottaDB to a supported release"
	}
	return true, "YottaDB " + m[0], ""
}

// firstPath returns the first entry of a space-separated $ydb_routines list (the
// first is the writable object/source dir; the rest are read-only search paths).
func firstPath(routines string) string {
	for i := 0; i < len(routines); i++ {
		if routines[i] == ' ' {
			return routines[:i]
		}
	}
	return routines
}

func renderDoctor(cc *clikit.Context, res doctorResult) {
	cc.Title("m-ydb doctor — " + res.Transport)
	for _, c := range res.Checks {
		line := c.Name + ": " + c.Detail
		if c.OK {
			fmt.Fprintln(cc.Stdout, cc.Success(line))
		} else {
			fmt.Fprintln(cc.Stdout, cc.Failure(line))
			if c.Fix != "" {
				fmt.Fprintln(cc.Stdout, "    fix: "+c.Fix)
			}
		}
	}
}
