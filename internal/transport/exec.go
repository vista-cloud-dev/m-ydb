package transport

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// mnemonicRe matches a YottaDB error mnemonic piece — `%FAC-S-NAME`
// (`%YDB-E-LVUNDEF`, `%GTM-E-LABELMISSING`). It deliberately requires the
// `-S-` severity infix so a percent-routine in the location piece (e.g.
// `%XCMD+5^%XCMD`, which also starts with `%`) is not mistaken for it.
var mnemonicRe = regexp.MustCompile(`^%[A-Za-z0-9]+-[A-Za-z]-`)

// statusSentinel ($CHAR(1), SOH) delimits the $ZSTATUS the $ETRAP wrapper emits
// on a fault. It is a control byte that never appears in routine output, so the
// captured status block is unambiguous to strip and parse.
const statusSentinel = "\x01"

// ExecTrapped runs an entryref or M command under a $ETRAP that captures the
// structured $ZSTATUS on any fault (driver-contract §5.3/§7), rather than
// scraping the engine's free-form stderr. The work is xecute'd by `%XCMD` — a
// normal (non-direct) context where $ETRAP fires and stdout stays clean (direct
// mode prints `YDB>` prompts and handles errors at the prompt, bypassing the
// trap). On a fault the trap writes the sentinel-delimited $ZSTATUS to the
// principal device and ZHALTs non-zero. The returned ExecResult carries clean
// Stdout (sentinel block removed) plus a populated EngineError on a fault.
func (s *Session) ExecTrapped(ctx context.Context, req mdriver.ExecRequest) (mdriver.ExecResult, error) {
	cmd := s.buildTrapped(req)
	argv := s.wrap([]string{s.yottabin(), "-run", "%XCMD", cmd})
	out, err := s.run(ctx, argv, s.execEnv(), req.Stdin)
	if err != nil {
		return mdriver.ExecResult{}, err
	}
	stdout, ee := splitStatus(out.Stdout)
	return mdriver.ExecResult{Stdout: stdout, Status: out.Code, EngineError: ee}, nil
}

// Compile ZLINKs the given bare routine names (forcing a recompile so compile
// faults surface) and returns the §7 EngineError on the first failure, or nil
// when all compile clean. Unlike a runtime fault, a YottaDB *compile* error is
// written to stderr as a `%FAC-E-NAME, text` listing with exit 0 and does NOT
// invoke $ETRAP — so Compile parses the compiler listing rather than the
// $ZSTATUS trap. The configured routine path is layered on via $ZROUTINES so
// the routines resolve while %XCMD stays linked.
func (s *Session) Compile(ctx context.Context, names []string) (*mdriver.EngineError, error) {
	if len(names) == 0 {
		return nil, nil
	}
	var z strings.Builder
	if s.cfg.Routines != "" {
		fmt.Fprintf(&z, "SET $ZROUTINES=%s_$ZROUTINES ", mQuote(s.cfg.Routines+" "))
	}
	for i, n := range names {
		if i > 0 {
			z.WriteByte(' ')
		}
		fmt.Fprintf(&z, `ZLINK "%s"`, n)
	}
	argv := s.wrap([]string{s.yottabin(), "-run", "%XCMD", z.String()})
	out, err := s.run(ctx, argv, s.execEnv(), "")
	if err != nil {
		return nil, err
	}
	if ee := parseCompileError(out.Stderr); ee != nil {
		return ee, nil
	}
	// Belt-and-suspenders: a non-compile fault (e.g. ZLINKFILE) may still hit
	// the trap on stdout.
	if _, ee := splitStatus(out.Stdout); ee != nil {
		return ee, nil
	}
	return nil, nil
}

var (
	compileLineRe   = regexp.MustCompile(`line (\d+)`)
	sourceModuleRe  = regexp.MustCompile(`source module (\S+)`)
	errSeverityFrag = []string{"-E-", "-F-"} // error / fatal severities
)

// parseCompileError extracts the first error/fatal compile fault from a YottaDB
// compiler listing on stderr, e.g.:
//
//	bad zzznotacommand
//	    ^-----
//	At column 5, line 2, source module /stage/r/ZZTBAD.m
//	%YDB-E-INVCMD, Invalid command keyword encountered
//
// It returns the §7 EngineError {routine,line,mnemonic,text}, or nil when the
// listing carries no error-severity fault (warnings are ignored).
func parseCompileError(stderr string) *mdriver.EngineError {
	var ee *mdriver.EngineError
	for _, ln := range strings.Split(stderr, "\n") {
		ln = strings.TrimSpace(ln)
		if !mnemonicRe.MatchString(ln) || !anySubstr(ln, errSeverityFrag) {
			continue
		}
		mnem, text, _ := strings.Cut(ln, ",")
		ee = &mdriver.EngineError{Mnemonic: strings.TrimSpace(mnem), Text: strings.TrimSpace(text)}
		break
	}
	if ee == nil {
		return nil
	}
	if m := compileLineRe.FindStringSubmatch(stderr); m != nil {
		ee.Line, _ = strconv.Atoi(m[1])
	}
	if m := sourceModuleRe.FindStringSubmatch(stderr); m != nil {
		ee.Routine = strings.TrimSuffix(filepath.Base(m[1]), ".m")
	}
	return ee
}

func anySubstr(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// execEnv is the environment for an exec invocation. It deliberately omits
// ydb_routines: the engine's default search path must stay intact so the
// %XCMD utility (a percent-routine on the system path) still links, and the
// configured routine path is layered on at runtime via $ZROUTINES instead (see
// buildTrapped). For docker the container's own env applies, so it returns nil.
func (s *Session) execEnv() []string {
	if s.isDocker() {
		return nil
	}
	var env []string
	if s.cfg.Dist != "" {
		env = append(env, "ydb_dist="+s.cfg.Dist)
	}
	if s.cfg.GblDir != "" {
		env = append(env, "ydb_gbldir="+s.cfg.GblDir)
	}
	return env
}

// buildTrapped assembles the single-line M command line %XCMD xecutes: set the
// $ETRAP trap first, prepend the configured routine path onto $ZROUTINES (so
// staged routines resolve while the system path — and %XCMD — stays intact),
// then the work — an entryref (DO, with Args staged into $ZCMDLINE) or a single
// command (eval); a raw Script is passed verbatim. M commands are
// space-separated; the $ETRAP literal needs no quote doubling because the
// sentinel is $CHAR(1), not a quote.
func (s *Session) buildTrapped(req mdriver.ExecRequest) string {
	var b strings.Builder
	b.WriteString(trapClause())
	b.WriteByte(' ')
	if s.cfg.Routines != "" {
		fmt.Fprintf(&b, "SET $ZROUTINES=%s_$ZROUTINES ", mQuote(s.cfg.Routines+" "))
	}
	switch {
	case req.EntryRef != "":
		if len(req.Args) > 0 {
			fmt.Fprintf(&b, "SET $ZCMDLINE=%s ", mQuote(strings.Join(req.Args, " ")))
		}
		fmt.Fprintf(&b, "DO %s", req.EntryRef)
	case req.Script != "":
		b.WriteString(strings.ReplaceAll(strings.TrimRight(req.Script, "\n"), "\n", " "))
	default:
		b.WriteString(req.Command)
	}
	// An ephemeral-run prefix is embedded as a trailing comment so the running
	// process is identifiable in its command line (abort --prefix greps for it).
	if req.Prefix != "" {
		fmt.Fprintf(&b, " ;%s", req.Prefix)
	}
	return b.String()
}

// abortMarkerRe restricts an abort/run prefix to a safe token (it is grepped
// for and interpolated into a shell command).
var abortMarkerRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

// Abort stops YottaDB processes whose command line carries the ephemeral-run
// marker (the `;<prefix>` embedded by a run --prefix), via `pgrep -f` →
// `mupip stop`. YottaDB is daemonless and exec is synchronous, so this targets
// backgrounded/long-running prefixed jobs; with nothing matching it is a clean
// no-op. It returns the PIDs it stopped.
func (s *Session) Abort(ctx context.Context, prefix string) ([]string, error) {
	if !abortMarkerRe.MatchString(prefix) {
		return nil, fmt.Errorf("transport: invalid --prefix %q (want [A-Za-z][A-Za-z0-9]*)", prefix)
	}
	// Match the `;<prefix>` marker, then keep only real engine processes by
	// checking /proc/<pid>/comm — `pgrep -f` would otherwise also match the very
	// `sh -c` shell running this pgrep (its own command line carries the marker).
	script := fmt.Sprintf(
		`for p in $(pgrep -f ';%s' 2>/dev/null); do c=$(cat /proc/$p/comm 2>/dev/null); case "$c" in yottadb|mumps) echo $p;; esac; done`,
		prefix)
	out, _, err := s.Sh(ctx, script)
	if err != nil {
		return nil, err
	}
	var killed []string
	for _, pid := range strings.Fields(out) {
		if _, err := s.Util(ctx, "mupip", []string{"stop", pid}, ""); err != nil {
			return killed, err
		}
		killed = append(killed, pid)
	}
	return killed, nil
}

// trapClause is the $ETRAP that writes the sentinel-delimited $ZSTATUS to the
// principal device and halts non-zero.
func trapClause() string {
	return `SET $ETRAP="USE $PRINCIPAL WRITE $C(1),$ZSTATUS,$C(1) ZHALT 1"`
}

// mQuote renders s as an M string literal (doubling embedded quotes).
func mQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// splitStatus separates clean stdout from a trailing sentinel-delimited
// $ZSTATUS block, returning the parsed EngineError (nil when absent).
func splitStatus(raw string) (string, *mdriver.EngineError) {
	i := strings.Index(raw, statusSentinel)
	if i < 0 {
		return raw, nil
	}
	rest := raw[i+len(statusSentinel):]
	j := strings.Index(rest, statusSentinel)
	if j < 0 {
		return raw[:i], parseZStatus(strings.TrimSpace(rest))
	}
	return raw[:i], parseZStatus(rest[:j])
}

// parseZStatus turns a YottaDB $ZSTATUS string into the §7 EngineError. The
// format is `code,place,%MNEMONIC,text…`, where place is `label+offset^routine`
// (the location may be absent). Commas inside the text are preserved.
func parseZStatus(zs string) *mdriver.EngineError {
	zs = strings.TrimSpace(zs)
	if zs == "" {
		return nil
	}
	parts := strings.Split(zs, ",")
	ee := &mdriver.EngineError{}

	// Find the mnemonic piece (%…-…-… token); everything after it is the text,
	// the piece before it (if any, and not the leading numeric code) is the place.
	mIdx := -1
	for i, p := range parts {
		if mnemonicRe.MatchString(strings.TrimSpace(p)) {
			mIdx = i
			break
		}
	}
	if mIdx < 0 {
		// No recognizable mnemonic: surface the whole thing as text.
		ee.Text = zs
		return ee
	}
	ee.Mnemonic = strings.TrimSpace(parts[mIdx])
	ee.Text = strings.TrimSpace(strings.Join(parts[mIdx+1:], ","))
	// The place is the piece right before the mnemonic, unless that is the
	// leading numeric error code (i.e. there was no location piece).
	if mIdx >= 2 {
		ee.Routine, ee.Line = parsePlace(parts[mIdx-1])
	}
	return ee
}

// parsePlace splits a `label+offset^routine` location into routine + line
// offset. A missing offset is 0; a missing routine yields "".
func parsePlace(place string) (routine string, line int) {
	place = strings.TrimSpace(place)
	if i := strings.LastIndex(place, "^"); i >= 0 {
		routine = place[i+1:]
		place = place[:i]
	}
	if i := strings.LastIndex(place, "+"); i >= 0 {
		if n, err := strconv.Atoi(place[i+1:]); err == nil {
			line = n
		}
	}
	return routine, line
}
