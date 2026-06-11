package transport

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// This file is the YottaDB lifecycle orchestration (driver-contract §5.1). YDB
// is daemonless: "the instance" is the global directory (.gld) + database (.dat)
// + environment. provision lays out the .gld (GDE) and creates the .dat (mupip
// create); up ensures those exist and clears any stale shared memory left by a
// crashed process (risk C1); down releases shared memory (mupip rundown);
// destroy deletes the files. The docker transport maps these onto container
// run/start/stop/rm. Validated at the unit (argv/script) level here; real-engine
// behavior is exercised by the gated integration tier.

var errNoGblDir = errors.New("transport: no global directory (.gld) configured")

// errRemoteAttachOnly: over SSH the YottaDB instance is provisioned and torn
// down on the host, out of band — the driver only attaches to it (like the IRIS
// remote transport). So provision/destroy are refused; up verifies reachability
// and down is a no-op.
var errRemoteAttachOnly = errors.New("transport: remote (SSH) is attach-only — provision/destroy the YottaDB instance on the host, not through the driver")

// ProvisionOpts carries provision inputs (driver-contract §5.1). For YottaDB
// local, Image/Namespace/License are not applicable; Image is the docker image.
type ProvisionOpts struct {
	Image     string
	Namespace string
	License   string
}

// datPath derives the database file beside the global directory: m.gld → m.dat
// (or, for an extensionless gld path, <path>.dat).
func datPath(gbldir string) string {
	return strings.TrimSuffix(gbldir, ".gld") + ".dat"
}

func filesExist(paths ...string) bool {
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			return false
		}
	}
	return true
}

// Provision creates the instance: GDE layout + mupip create (local), or a
// container (docker).
func (s *Session) Provision(ctx context.Context, opts ProvisionOpts) (mdriver.StateResult, error) {
	if s.isRemote() {
		return mdriver.StateResult{}, errRemoteAttachOnly
	}
	if s.isDocker() {
		args := []string{"run", "-d", "--name", s.cfg.Container}
		if opts.Image != "" {
			args = append(args, opts.Image)
		}
		if _, err := s.Docker(ctx, args...); err != nil {
			return mdriver.StateResult{}, err
		}
		return mdriver.StateResult{State: "provisioned", Endpoint: s.cfg.Container}, nil
	}
	if s.cfg.GblDir == "" {
		return mdriver.StateResult{}, errNoGblDir
	}
	// 1) GDE: map the DEFAULT segment to the .dat beside the .gld.
	script := fmt.Sprintf("change -segment DEFAULT -file=%s\nexit\n", datPath(s.cfg.GblDir))
	if _, err := s.Util(ctx, "gde", nil, script); err != nil {
		return mdriver.StateResult{}, fmt.Errorf("gde: %w", err)
	}
	// 2) mupip create: read the gld, create the .dat.
	if _, err := s.Util(ctx, "mupip", []string{"create"}, ""); err != nil {
		return mdriver.StateResult{}, fmt.Errorf("mupip create: %w", err)
	}
	return mdriver.StateResult{State: "provisioned"}, nil
}

// Up brings the instance into a usable state: start the container (docker), or
// (local) ensure the .gld/.dat exist and clear stale shared memory.
func (s *Session) Up(ctx context.Context) (mdriver.StateResult, error) {
	if s.isRemote() {
		h, err := s.Health(ctx)
		if err != nil {
			return mdriver.StateResult{}, err
		}
		if !h.Healthy {
			return mdriver.StateResult{State: "unreachable", Endpoint: s.cfg.Host}, nil
		}
		return mdriver.StateResult{State: "ready", Endpoint: s.cfg.Host}, nil
	}
	if s.isDocker() {
		if _, err := s.Docker(ctx, "start", s.cfg.Container); err != nil {
			return mdriver.StateResult{}, err
		}
		return mdriver.StateResult{State: "started", Endpoint: s.cfg.Container}, nil
	}
	if s.cfg.GblDir == "" {
		return mdriver.StateResult{}, errNoGblDir
	}
	if !filesExist(s.cfg.GblDir, datPath(s.cfg.GblDir)) {
		if _, err := s.Provision(ctx, ProvisionOpts{}); err != nil {
			return mdriver.StateResult{}, err
		}
	}
	// Release any stale shared memory from a crashed process (risk C1); a no-op
	// on a clean database.
	if _, err := s.Util(ctx, "mupip", []string{"rundown", "-region", "*"}, ""); err != nil {
		return mdriver.StateResult{}, fmt.Errorf("mupip rundown: %w", err)
	}
	return mdriver.StateResult{State: "ready"}, nil
}

// Down releases the instance: stop the container (docker) or mupip rundown (local).
func (s *Session) Down(ctx context.Context) (mdriver.StateResult, error) {
	if s.isRemote() {
		return mdriver.StateResult{State: "detached", Endpoint: s.cfg.Host}, nil
	}
	if s.isDocker() {
		if _, err := s.Docker(ctx, "stop", s.cfg.Container); err != nil {
			return mdriver.StateResult{}, err
		}
		return mdriver.StateResult{State: "stopped"}, nil
	}
	if _, err := s.Util(ctx, "mupip", []string{"rundown", "-region", "*"}, ""); err != nil {
		return mdriver.StateResult{}, fmt.Errorf("mupip rundown: %w", err)
	}
	return mdriver.StateResult{State: "stopped"}, nil
}

// Restart is Down then Up.
func (s *Session) Restart(ctx context.Context) (mdriver.StateResult, error) {
	if _, err := s.Down(ctx); err != nil {
		return mdriver.StateResult{}, err
	}
	return s.Up(ctx)
}

// Destroy removes the instance: rm the container (docker) or delete the database
// files (local).
func (s *Session) Destroy(ctx context.Context) (mdriver.StateResult, error) {
	if s.isRemote() {
		return mdriver.StateResult{}, errRemoteAttachOnly
	}
	if s.isDocker() {
		if _, err := s.Docker(ctx, "rm", "-f", s.cfg.Container); err != nil {
			return mdriver.StateResult{}, err
		}
		return mdriver.StateResult{State: "removed"}, nil
	}
	if s.cfg.GblDir == "" {
		return mdriver.StateResult{}, errNoGblDir
	}
	// Best-effort: clear shared memory first so the .dat is not in use.
	_, _ = s.Util(ctx, "mupip", []string{"rundown", "-region", "*"}, "")
	for _, p := range []string{datPath(s.cfg.GblDir), s.cfg.GblDir} {
		if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
			return mdriver.StateResult{}, fmt.Errorf("remove %s: %w", p, err)
		}
	}
	return mdriver.StateResult{State: "destroyed"}, nil
}
