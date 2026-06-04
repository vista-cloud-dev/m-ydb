package clikit

import (
	"errors"
	"fmt"
)

// Exit codes — the m engine-driver contract ladder (driver-contract.md §2).
// Every m-iris verb returns one of these; m-cli branches on the code.
//
//	0 ok · 2 usage · 3 gate/tests-failed · 4 conflict/refusal ·
//	5 runtime · 6 engine-unreachable · 7 unsupported (verb/transport)
const (
	ExitOK          = 0 // success
	ExitUsage       = 2 // usage error (bad flags/args)
	ExitCheck       = 3 // gate: --check/lint findings, drift, or tests failed
	ExitRefused     = 4 // conflict / refusal (lock held, conflict-check, prune scope)
	ExitRuntime     = 5 // runtime error (IO / engine fault / parse)
	ExitUnreachable = 6 // engine unreachable (no connectivity / auth)
	ExitUnsupported = 7 // verb or transport not available on this engine — query caps first
)

// Error is the deterministic, machine-parseable error object. Commands return
// it (via Fail) so agents and CI branch on code+exit, not on prose (§5.5).
type Error struct {
	Code    string `json:"code"`
	Exit    int    `json:"exit"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`

	// Engine, when set, is surfaced at envelope.engineError (a sibling of
	// error, never nested) — the §7 structured engine fault behind a failed
	// exec/cover verb.
	Engine *EngineError `json:"-"`
}

func (e *Error) Error() string { return e.Message }

// Fail builds a deterministic Error for a command to return.
func Fail(exit int, code, message, hint string) *Error {
	return &Error{Code: code, Exit: exit, Message: message, Hint: hint}
}

// FailEngine is Fail plus a §7 engine fault, surfaced at envelope.engineError.
// Use it for exec/cover faults so the real cause (routine, line, mnemonic)
// reaches m-cli alongside the deterministic error code.
func FailEngine(exit int, code, message, hint string, eng *EngineError) *Error {
	return &Error{Code: code, Exit: exit, Message: message, Hint: hint, Engine: eng}
}

// exitOf maps any error to an exit code (clikit.Error keeps its own).
func exitOf(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Exit
	}
	return ExitRuntime
}

// RenderError prints an error: the JSON envelope in JSON mode, otherwise a
// styled "Error: …" + optional hint on stderr.
func RenderError(c *Context, err error) {
	var e *Error
	if !errors.As(err, &e) {
		e = &Error{Code: "RUNTIME", Exit: ExitRuntime, Message: err.Error()}
	}
	if c.JSON() {
		_ = writeJSON(c.Stderr, Envelope{SchemaVersion: SchemaVersion, Command: c.Command, OK: false, Exit: e.Exit, Error: e, EngineError: e.Engine})
		return
	}
	fmt.Fprintf(c.Stderr, "%s %s\n", c.th.err.render(c.Color, c.gl.Err+" Error:"), e.Message)
	if e.Hint != "" {
		fmt.Fprintln(c.Stderr, c.th.hint.render(c.Color, "  "+c.gl.Arrow+" hint: "+e.Hint))
	}
}
