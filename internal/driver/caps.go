// Package driver holds the m-ydb vendor logic: the YottaDB-specific realization
// of the neutral engine-driver contract. It depends on the shared SDK
// (github.com/vista-cloud-dev/m-driver-sdk) for the contract shapes and the
// verb-level Transport, and knows nothing of m-cli.
package driver

import mdriver "github.com/vista-cloud-dev/m-driver-sdk"

// Caps returns the m-ydb capability document (driver-contract.md §4).
//
// It is HONEST by construction (conformance enforces advertised == implemented):
// it advertises only the axes/verbs wired in this build and grows milestone by
// milestone — M1 lifecycle, M2 sync, M3 exec, M4 data, M5 cover, M6 admin, M7
// native (info/doctor/selftest land with M1). Do not list a verb here before its
// command exists.
//
// YottaDB is daemonless and file-based, so only the local and docker transports
// are supported (no network API → features.remote = false). The transport seam
// exists, so both are advertised up-front.
func Caps() mdriver.Caps {
	return mdriver.Caps{
		Engine:     "ydb",
		Contract:   mdriver.ContractVersion,
		Transports: []string{mdriver.TransportLocal, mdriver.TransportDocker},
		Axes: mdriver.Axes{
			// Only the verbs actually wired; grows per milestone.
			// M0 meta + M1a info/doctor; M1a lifecycle health surface.
			Meta:      []string{"caps", "info", "version", "schema", "doctor"},
			Lifecycle: []string{"up", "down", "restart", "status", "wait", "provision", "destroy"},
			// M2 sync (full axis): read (list/pull/status/verify) + write
			// (diff/push/deploy/rm). Order matches driver-contract §4.
			Sync: []string{"list", "pull", "status", "verify", "diff", "push", "deploy", "rm"},
			// M3 exec (full axis): load/run/eval/abort. engineError comes from
			// $ZSTATUS (runtime, via $ETRAP) and the compiler listing (compile).
			Exec: []string{"load", "run", "eval", "abort"},
		},
		Features: mdriver.Features{
			Remote:          false, // YottaDB has no network API
			Prune:           true,  // sync deploy --prune true-sync (M2)
			EphemeralPrefix: true,  // exec --prefix zzt<runid> staging (M3)
			Snapshot:        false, // lifecycle snapshot/rollback — roadmap §10, not yet
		},
	}
}
