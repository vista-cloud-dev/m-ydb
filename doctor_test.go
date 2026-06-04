package main

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/vista-cloud-dev/m-ydb/clikit"
	"github.com/vista-cloud-dev/m-ydb/internal/config"
)

// okChecks returns a dependency set where every check passes (local transport).
func okChecks() checks {
	return checks{
		lookPath: func(string) (string, error) { return "/opt/yottadb/yottadb", nil },
		version:  func(context.Context) (string, error) { return "r2.02", nil },
		statRead: func(string) error { return nil },
		writable: func(string) error { return nil },
		dockerOK: func(context.Context) error { return nil },
	}
}

func findCheck(res []checkResult, name string) (checkResult, bool) {
	for _, c := range res {
		if c.Name == name {
			return c, true
		}
	}
	return checkResult{}, false
}

func TestDoctor_AllGreen(t *testing.T) {
	conn := &config.Conn{Transport: "local", Dist: "/opt/yottadb", GblDir: "/data/m.gld", Routines: "/src"}
	res, exit := okChecks().run(context.Background(), conn)
	if !res.OK || exit != clikit.ExitOK {
		t.Fatalf("ok=%v exit=%d, want ok=true exit=0; checks=%+v", res.OK, exit, res.Checks)
	}
	for _, want := range []string{"binary", "version", "gld", "routines"} {
		if c, ok := findCheck(res.Checks, want); !ok || !c.OK {
			t.Errorf("check %q missing or failed: %+v", want, c)
		}
	}
}

func TestDoctor_MissingBinary_Unreachable(t *testing.T) {
	k := okChecks()
	k.lookPath = func(string) (string, error) { return "", errors.New("not found") }
	res, exit := k.run(context.Background(), &config.Conn{Transport: "local", GblDir: "/g", Routines: "/r"})
	if exit != clikit.ExitUnreachable {
		t.Errorf("exit = %d, want 6 (unreachable) when the binary is missing", exit)
	}
	if c, _ := findCheck(res.Checks, "binary"); c.OK {
		t.Error("binary check should fail")
	}
}

func TestDoctor_OldVersion_CheckFailed(t *testing.T) {
	k := okChecks()
	k.version = func(context.Context) (string, error) { return "r1.20", nil }
	res, exit := k.run(context.Background(), &config.Conn{Transport: "local", Dist: "/d", GblDir: "/g", Routines: "/r"})
	if exit != clikit.ExitRuntime {
		t.Errorf("exit = %d, want 5 (a check failed) for an old version", exit)
	}
	if c, _ := findCheck(res.Checks, "version"); c.OK {
		t.Error("version check should fail for r1.20")
	}
}

func TestDoctor_UnwritableRoutines_CheckFailed(t *testing.T) {
	k := okChecks()
	k.writable = func(string) error { return errors.New("permission denied") }
	_, exit := k.run(context.Background(), &config.Conn{Transport: "local", Dist: "/d", GblDir: "/g", Routines: "/ro"})
	if exit != clikit.ExitRuntime {
		t.Errorf("exit = %d, want 5 when routines path is unwritable", exit)
	}
}

func TestDoctor_DockerDown_Unreachable(t *testing.T) {
	k := okChecks()
	k.dockerOK = func(context.Context) error { return errors.New("cannot connect to the Docker daemon") }
	res, exit := k.run(context.Background(), &config.Conn{Transport: "docker", Container: "m-test-engine"})
	if exit != clikit.ExitUnreachable {
		t.Errorf("exit = %d, want 6 when docker is down", exit)
	}
	if c, _ := findCheck(res.Checks, "docker"); c.OK {
		t.Error("docker check should fail")
	}
}

// statRead signature sanity: a missing gld surfaces as a failed (not panicking) check.
func TestDoctor_UnreadableGld_CheckFailed(t *testing.T) {
	k := okChecks()
	k.statRead = func(string) error { return fs.ErrNotExist }
	_, exit := k.run(context.Background(), &config.Conn{Transport: "local", Dist: "/d", GblDir: "/missing.gld", Routines: "/r"})
	if exit != clikit.ExitRuntime {
		t.Errorf("exit = %d, want 5 when the gld is unreadable", exit)
	}
}
