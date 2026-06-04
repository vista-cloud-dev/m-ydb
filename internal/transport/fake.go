package transport

import "context"

// Fake is the in-memory Transport for unit tests: it records every call and
// returns canned results in order (the last result repeats once exhausted).
// Vendor logic (internal/driver) is tested entirely against this — no engine.
type Fake struct {
	// Canned results, consumed in order per verb.
	ExecResults    []ExecResult
	LoadResults    []LoadResult
	CompileResults []CompileResult
	GlobalResults  []GlobalResult
	HealthResult   HealthResult

	// Forced transport-level errors (a launch failure, not an engine fault).
	ExecErr    error
	LoadErr    error
	CompileErr error
	GlobalErr  error
	HealthErr  error

	// Recorded calls, in order.
	ExecCalls    []ExecRequest
	LoadCalls    []LoadRequest
	CompileCalls []CompileRequest
	GlobalCalls  []GlobalRef
	HealthCalls  int

	execN, loadN, compileN, globalN int
}

func (f *Fake) Exec(_ context.Context, req ExecRequest) (ExecResult, error) {
	f.ExecCalls = append(f.ExecCalls, req)
	if f.ExecErr != nil {
		return ExecResult{}, f.ExecErr
	}
	res := pick(f.ExecResults, &f.execN)
	return res, nil
}

func (f *Fake) Load(_ context.Context, req LoadRequest) (LoadResult, error) {
	f.LoadCalls = append(f.LoadCalls, req)
	if f.LoadErr != nil {
		return LoadResult{}, f.LoadErr
	}
	return pick(f.LoadResults, &f.loadN), nil
}

func (f *Fake) Compile(_ context.Context, req CompileRequest) (CompileResult, error) {
	f.CompileCalls = append(f.CompileCalls, req)
	if f.CompileErr != nil {
		return CompileResult{}, f.CompileErr
	}
	return pick(f.CompileResults, &f.compileN), nil
}

func (f *Fake) ReadGlobal(_ context.Context, req GlobalRef) (GlobalResult, error) {
	f.GlobalCalls = append(f.GlobalCalls, req)
	if f.GlobalErr != nil {
		return GlobalResult{}, f.GlobalErr
	}
	return pick(f.GlobalResults, &f.globalN), nil
}

func (f *Fake) Health(context.Context) (HealthResult, error) {
	f.HealthCalls++
	if f.HealthErr != nil {
		return HealthResult{}, f.HealthErr
	}
	return f.HealthResult, nil
}

// pick returns results[*n] then advances n, clamping to the last element so an
// over-call repeats the final canned result rather than panicking.
func pick[T any](results []T, n *int) T {
	var zero T
	if len(results) == 0 {
		return zero
	}
	i := *n
	if i >= len(results) {
		i = len(results) - 1
	} else {
		*n++
	}
	return results[i]
}
