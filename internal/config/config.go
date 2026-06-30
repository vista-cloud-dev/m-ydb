// Package config resolves the m-ydb connection (transport + YottaDB paths or a
// container) from flags and environment. m-cli passes the active [instance]
// profile as M_YDB_* env + non-secret flags (driver-contract §3); the blob is
// opaque to m-cli — only this driver interprets the YottaDB fields. As a
// convenience for humans, an unset path falls back to the standard ydb_*/gtm_*
// environment a YottaDB shell already exports.
package config

import (
	"context"
	"fmt"
	"os"

	"github.com/vista-cloud-dev/m-ydb/internal/mirror"
	"github.com/vista-cloud-dev/m-ydb/internal/source"
	"github.com/vista-cloud-dev/m-ydb/internal/transport"
)

// Conn is the connection + behavior flag set, embedded in the root CLI and bound
// so every command receives a *config.Conn. Flags win over M_YDB_* env (Kong),
// which in turn is filled from the standard ydb_*/gtm_* env by Resolve.
type Conn struct {
	Transport string `env:"M_YDB_TRANSPORT" enum:"local,docker,remote" default:"local" help:"Engine transport: local (native install) | docker (a container we manage) | remote (SSH to a host running YottaDB)."`
	Dist      string `name:"ydb-dist" env:"M_YDB_DIST" help:"$ydb_dist — directory holding the yottadb/mupip/gde binaries (local + remote; defaults to $ydb_dist/$gtm_dist)." placeholder:"DIR"`
	GblDir    string `name:"gbldir" env:"M_YDB_GBLDIR" help:"$ydb_gbldir — the .gld global directory (defaults to $ydb_gbldir/$gtmgbldir)." placeholder:"PATH"`
	Routines  string `name:"routines" env:"M_YDB_ROUTINES" help:"$ydb_routines — routine source/object search path (defaults to $ydb_routines/$gtmroutines)." placeholder:"PATH"`
	Container string `env:"M_YDB_CONTAINER" help:"docker transport: the container name to exec into." placeholder:"NAME"`

	// remote (SSH) transport — reach a filesystem YottaDB on another host (e.g. a
	// FOIA `vehu` server). The same yottadb invocation is wrapped in `ssh`; the
	// engine env is sourced from EnvFile on the far side. SSH is a host-shell
	// transport, not a YottaDB network engine API.
	Host     string `env:"M_YDB_HOST" help:"remote (SSH) transport: target host." placeholder:"HOST"`
	Port     int    `env:"M_YDB_PORT" help:"remote (SSH) transport: ssh port (default 22)." placeholder:"PORT"`
	User     string `env:"M_YDB_USER" help:"remote (SSH) transport: ssh user." placeholder:"USER"`
	Identity string `name:"identity" env:"M_YDB_IDENTITY" help:"remote (SSH) transport: ssh identity file (-i)." placeholder:"FILE"`
	EnvFile  string `name:"env-file" env:"M_YDB_ENVFILE" help:"remote (SSH) transport: remote file to source for the YottaDB env (e.g. /home/vehu/etc/env)." placeholder:"PATH"`

	// sync axis (M2) behavior flags.
	Mirror string `env:"M_YDB_MIRROR" default:".m-cache" help:"Mirror root directory for the sync axis (routine source ↔ instance)."`
	Filter string `help:"Glob over the bare routine name (extension-insensitive), e.g. 'DG*'." placeholder:"GLOB"`
	DryRun bool   `name:"dry-run" help:"Plan only; never write to the mirror or the instance."`
}

// Resolve fills unset path fields from the standard YottaDB (ydb_*) environment,
// falling back to the GT.M (gtm_*) names. Explicit flags / M_YDB_* env always
// win. getenv is injected for testability (pass os.Getenv in production).
func (c *Conn) Resolve(getenv func(string) string) {
	fill := func(dst *string, keys ...string) {
		if *dst != "" {
			return
		}
		for _, k := range keys {
			if v := getenv(k); v != "" {
				*dst = v
				return
			}
		}
	}
	fill(&c.Dist, "ydb_dist", "gtm_dist")
	fill(&c.GblDir, "ydb_gbldir", "gtmgbldir")
	fill(&c.Routines, "ydb_routines", "gtmroutines")
}

// ResolveEnv fills unset paths from the real process environment. Commands that
// touch the engine call it before reading paths or building a session.
func (c *Conn) ResolveEnv() { c.Resolve(os.Getenv) }

// Validate checks the connection is usable for engine-bound work.
func (c *Conn) Validate() error {
	if c.Transport == "docker" && c.Container == "" {
		return fmt.Errorf("docker transport needs a container: pass --container or M_YDB_CONTAINER")
	}
	if c.Transport == transportRemote && c.Host == "" {
		return fmt.Errorf("remote transport needs a host: pass --host or M_YDB_HOST")
	}
	return nil
}

// TransportConfig maps the connection onto the transport layer's config.
func (c *Conn) TransportConfig() transport.Config {
	return transport.Config{
		Transport: c.Transport,
		Dist:      c.Dist,
		GblDir:    c.GblDir,
		Routines:  c.Routines,
		Container: c.Container,
		Host:      c.Host,
		Port:      c.Port,
		User:      c.User,
		Identity:  c.Identity,
		EnvFile:   c.EnvFile,
	}
}

// NewSession builds the YottaDB session transport for this connection,
// resolving paths from the environment first.
func (c *Conn) NewSession() *transport.Session {
	c.ResolveEnv()
	return transport.NewSession(c.TransportConfig(), nil)
}

// Layout is the mirror layout for the sync axis (the --mirror root).
func (c *Conn) Layout() mirror.Layout {
	return mirror.Layout{Root: c.Mirror}
}

// SourceDirs resolves the routine source directories from $ydb_routines (after
// filling unset paths from the environment).
func (c *Conn) SourceDirs() []string {
	c.ResolveEnv()
	return source.ParseRoutinesDirs(c.Routines)
}

// SourceStore builds the engine-side routine source store for the sync axis:
// a host-filesystem store for local, or a container store (over the session) for
// docker. It fails (usable for an exit-2) when no source directory is resolvable
// or the docker container is missing.
func (c *Conn) SourceStore() (source.Store, error) {
	if c.Transport == transportRemote {
		return nil, fmt.Errorf("sync over the remote (SSH) transport is not yet supported")
	}
	dirs := c.SourceDirs()
	if len(dirs) == 0 && c.Transport == transportDocker && c.Container != "" {
		// The host pinned no routine source path; a real VistA image keeps it in
		// the container's own engine profile (vehu), so ask the engine for its
		// $ZROUTINES rather than failing. Best-effort: a probe error leaves dirs
		// empty and falls through to the clear "set --routines" message below.
		if cr, err := c.NewSession().ContainerRoutines(context.Background()); err == nil {
			dirs = source.ParseRoutinesDirs(cr)
		}
	}
	if len(dirs) == 0 {
		return nil, fmt.Errorf("no routine source directory: set --routines or $ydb_routines")
	}
	if c.Transport == transportDocker {
		if c.Container == "" {
			return nil, fmt.Errorf("docker transport needs a container: pass --container or M_YDB_CONTAINER")
		}
		return source.NewShellStore(c.NewSession(), dirs), nil
	}
	return source.NewFileStore(dirs), nil
}

const (
	transportDocker = "docker"
	transportRemote = "remote"
)
