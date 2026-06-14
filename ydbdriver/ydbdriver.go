// Package ydbdriver is m-ydb's public, importable surface: it constructs a
// YottaDB mdriver.Transport for in-process consumers (notably m-cli's
// VistaEngine) that speak only the neutral engine-driver contract. All vendor
// logic stays in internal/ — this package is the thin seam that lets another
// module hold a YottaDB Transport without reaching into m-ydb's internals.
//
// The same Transport covers all three reaches behind one contract: local
// (native install), docker (a container we manage), and remote (SSH to a host
// running YottaDB). Pick one via Config.Transport.
package ydbdriver

import (
	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
	"github.com/vista-cloud-dev/m-ydb/internal/transport"
)

// Config is the YottaDB connection. It re-exports the internal transport config
// so external callers configure the engine without importing internal/.
//
//	local:  Dist / GblDir / Routines locate the install + database.
//	docker: Container is the container to exec into.
//	remote: Host (+ Port/User/Identity) is the SSH target; EnvFile is sourced
//	        on the far side for the YottaDB environment.
type Config = transport.Config

// New builds a YottaDB Transport over the given connection, using the real OS
// runner. Callers hold the result as an mdriver.Transport and never see vendor
// detail; the T0.1 readiness gate is Health (W $ZV via Exec).
func New(cfg Config) mdriver.Transport {
	return transport.NewSession(cfg, nil)
}
