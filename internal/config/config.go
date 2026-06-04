// Package config resolves the m-ydb connection (transport + YottaDB paths or a
// container) from flags and environment. m-cli passes the active [instance]
// profile as M_YDB_* env + non-secret flags (driver-contract §3); the blob is
// opaque to m-cli — only this driver interprets the YottaDB fields. As a
// convenience for humans, an unset path falls back to the standard ydb_*/gtm_*
// environment a YottaDB shell already exports.
package config

import (
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
	Transport string `env:"M_YDB_TRANSPORT" enum:"local,docker" default:"local" help:"Engine transport: local (native install) | docker (a container we manage). YottaDB has no network API, so there is no remote."`
	Dist      string `name:"ydb-dist" env:"M_YDB_DIST" help:"$ydb_dist — directory holding the yottadb/mupip/gde binaries (local; defaults to $ydb_dist/$gtm_dist)." placeholder:"DIR"`
	GblDir    string `name:"gbldir" env:"M_YDB_GBLDIR" help:"$ydb_gbldir — the .gld global directory (defaults to $ydb_gbldir/$gtmgbldir)." placeholder:"PATH"`
	Routines  string `name:"routines" env:"M_YDB_ROUTINES" help:"$ydb_routines — routine source/object search path (defaults to $ydb_routines/$gtmroutines)." placeholder:"PATH"`
	Container string `env:"M_YDB_CONTAINER" help:"docker transport: the container name to exec into." placeholder:"NAME"`

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
	dirs := c.SourceDirs()
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

const transportDocker = "docker"
