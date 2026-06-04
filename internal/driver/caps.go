// Package driver holds the m-ydb vendor logic: the YottaDB-specific realization
// of the neutral engine-driver contract (driver-contract.md v1.0). It depends on
// the contract shapes (internal/contract) but knows nothing of m-cli.
package driver

import "github.com/vista-cloud-dev/m-ydb/internal/contract"

// Caps returns the m-ydb capability document (contract §4).
//
// YottaDB is daemonless and file-based, so it supports only the local and docker
// transports — there is no network API, hence features.remote is false. The
// advertised axes/verbs are the driver's target surface; conformance (M8) gates
// that every advertised verb is actually implemented (caps honesty, contract §9).
func Caps() contract.Caps {
	return contract.Caps{
		Engine:     "ydb",
		Contract:   contract.Version,
		Transports: []string{contract.TransportLocal, contract.TransportDocker},
		Axes: contract.Axes{
			Lifecycle: []string{"up", "down", "restart", "status", "logs", "provision", "destroy", "wait"},
			Sync:      []string{"list", "pull", "status", "verify", "push", "deploy", "diff", "rm"},
			Exec:      []string{"load", "run", "eval", "abort"},
			Data:      []string{"get", "set", "kill", "query", "export", "import"},
			Cover:     []string{"trace"},
			Admin:     []string{"backup", "restore", "check", "journal"},
			Meta:      []string{"caps", "version", "info", "doctor", "selftest", "native"},
		},
		Features: contract.Features{
			Remote:          false,
			Prune:           true,
			EphemeralPrefix: true,
			Snapshot:        true,
		},
	}
}
