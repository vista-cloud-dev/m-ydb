// Package transport is the verb-level seam between m-ydb's vendor logic and a
// YottaDB instance. It is deliberately NOT a low-level run(argv): the local and
// docker strategies pipe M into a `yottadb` session and capture stdout, while
// the IRIS driver's eventual Atelier-SQL strategy issues HTTP+SQL and reads
// results from a global. A verb-level interface (Exec/Load/Compile/ReadGlobal/
// Health) is the one shape that fits both (risk B1); a raw argv seam could not.
//
// This interface is the heart of the future m-driver-sdk. It is vendored here
// until the Phase-0 reconciliation checkpoint, when it is frozen against both
// the YottaDB session shape and the IRIS Atelier-SQL shape and extracted.
package transport

import (
	"context"

	"github.com/vista-cloud-dev/m-ydb/internal/contract"
)

// Transport is the verb-level engine seam. Vendor logic (internal/driver)
// depends only on this interface and is unit-tested against the Fake; the real
// engine appears only in the gated integration tier.
type Transport interface {
	// Exec runs an M command line, entryref, or direct-mode script in the engine
	// and returns its captured output and status. On a compile/runtime fault,
	// ExecResult.EngineError is populated (contract §7) rather than returning a
	// transport error — a fault is a successful round-trip with a bad result.
	Exec(ctx context.Context, req ExecRequest) (ExecResult, error)

	// Load stages routine source into the engine and compiles it
	// (contract exec.load: stage + compile).
	Load(ctx context.Context, req LoadRequest) (LoadResult, error)

	// Compile compiles already-staged routines (contract: implicit on YottaDB,
	// an explicit step on IRIS; kept separate so both fit).
	Compile(ctx context.Context, req CompileRequest) (CompileResult, error)

	// ReadGlobal reads a global (sub)tree out of the engine — for data.get/query
	// and for reading the STDHARN result global after a test run.
	ReadGlobal(ctx context.Context, req GlobalRef) (GlobalResult, error)

	// Health probes liveness/readiness for `status --probe` and `wait`
	// (contract §5.7, plan §3).
	Health(ctx context.Context) (HealthResult, error)
}

// ExecMode selects how a session transport runs an Exec request.
type ExecMode int

const (
	// ExecCommand runs an inline M command: `yottadb -run %XCMD "<cmd>"`.
	ExecCommand ExecMode = iota
	// ExecRoutine runs an entryref: `yottadb -run <entryref> args…` (args → $ZCMDLINE).
	ExecRoutine
	// ExecScript pipes a multi-line M script to `yottadb -direct` (ends with halt).
	ExecScript
)

// ExecRequest is one execution against the engine.
type ExecRequest struct {
	Mode     ExecMode
	Command  string   // ExecCommand: an M command line
	EntryRef string   // ExecRoutine: e.g. RUN^STDHARN
	Args     []string // ExecRoutine: positional args → $ZCMDLINE
	Script   string   // ExecScript: newline-separated M lines (halt appended)
	Stdin    string   // optional principal-device input
	Prefix   string   // ephemeral routine prefix (zzt<runid>), contract exec --prefix
}

// ExecResult is the captured outcome of an Exec.
type ExecResult struct {
	Stdout      string
	Status      int
	EngineError *contract.EngineError // populated on fault (contract §7)
}

// LoadRequest stages routine source (contract exec.load).
type LoadRequest struct {
	Paths  []string // explicit .m files
	Dir    string   // or a directory of source
	Prefix string   // ephemeral staging prefix
}

// LoadResult lists what was staged + compiled.
type LoadResult struct {
	Loaded      []string
	EngineError *contract.EngineError
}

// CompileRequest names routines to (re)compile.
type CompileRequest struct {
	Routines []string
	Prefix   string
}

// CompileResult lists what compiled.
type CompileResult struct {
	Compiled    []string
	EngineError *contract.EngineError
}

// GlobalRef addresses a global node/subtree for ReadGlobal (contract data axis).
type GlobalRef struct {
	Name       string   // e.g. "^ycov" (leading ^ optional)
	Subscripts []string // descend to this subscript path
	Order      string   // "forward" | "reverse" (query traversal)
	Depth      int      // 0 = unbounded subtree
}

// GlobalNode is one (subscripted) global node.
type GlobalNode struct {
	Ref   string `json:"ref"`
	Value string `json:"value"`
}

// GlobalResult is the read-back subtree.
type GlobalResult struct {
	Nodes []GlobalNode
}

// HealthResult is the readiness/liveness snapshot for status/wait.
type HealthResult struct {
	Running   bool   `json:"running"`
	Healthy   bool   `json:"healthy"`
	Version   string `json:"version,omitempty"`
	LatencyMs int    `json:"latencyMs"`
}
