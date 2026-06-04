// Package contract holds a thin, local copy of the vendor-neutral engine-driver
// contract types (driver-contract.md v1.0): the capability document, the axis
// shapes, and the engine-error envelope every m-<engine> driver speaks.
//
// These are vendored locally so m-ydb is never blocked on the shared
// m-driver-sdk module. At the Phase-0 reconciliation checkpoint these types are
// extracted into m-driver-sdk and this package becomes a re-export shim. Keep it
// vendor-neutral: NO YottaDB specifics belong here (those live in internal/driver).
package contract

// Version is the driver-contract version this driver implements. m-cli reads it
// from caps.contract and refuses a driver whose major it does not understand
// (contract §8).
const Version = "1.0"

// Transport selectors (contract §3). YottaDB supports only local and docker;
// remote is meaningful for IRIS (Atelier REST) and listed here for completeness.
const (
	TransportLocal  = "local"
	TransportDocker = "docker"
	TransportRemote = "remote"
)

// Caps is the capability document emitted by `caps` (contract §4). m-cli probes
// it before optional verbs and adapts to what is advertised; an unadvertised
// verb yields exit 7.
type Caps struct {
	Engine     string   `json:"engine"`
	Contract   string   `json:"contract"`
	Transports []string `json:"transports"`
	Axes       Axes     `json:"axes"`
	Features   Features `json:"features"`
}

// Axes lists the advertised verbs per contract axis (contract §5). Field order
// is the contract's logical order; a struct (not a map) keeps the JSON stable.
type Axes struct {
	Lifecycle []string `json:"lifecycle"`
	Sync      []string `json:"sync"`
	Exec      []string `json:"exec"`
	Data      []string `json:"data"`
	Cover     []string `json:"cover"`
	Admin     []string `json:"admin"`
	Meta      []string `json:"meta"`
}

// Features advertises optional capabilities m-cli negotiates for graceful
// degradation (contract §4, §10).
type Features struct {
	Remote          bool `json:"remote"`
	Prune           bool `json:"prune"`
	EphemeralPrefix bool `json:"ephemeralPrefix"`
	Snapshot        bool `json:"snapshot"`
}

// EngineError is the engine-error envelope (contract §7), populated alongside
// ok=false by exec/cover verbs on any compile/runtime fault. For YottaDB it is
// driven from $ZSTATUS, not stderr scraping; mnemonic carries %YDB-E-/%GTM-E-
// codes (and IRIS <…> codes for that driver).
type EngineError struct {
	Routine  string `json:"routine,omitempty"`
	Line     int    `json:"line"`
	Mnemonic string `json:"mnemonic,omitempty"`
	Text     string `json:"text,omitempty"`
}
