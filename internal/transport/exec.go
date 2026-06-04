package transport

import (
	"context"
	"fmt"
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
	out, err := s.run(ctx, argv, s.env(), req.Stdin)
	if err != nil {
		return mdriver.ExecResult{}, err
	}
	stdout, ee := splitStatus(out.Stdout)
	return mdriver.ExecResult{Stdout: stdout, Status: out.Code, EngineError: ee}, nil
}

// buildTrapped assembles the single-line M command line %XCMD xecutes: set the
// $ETRAP trap first, then the work — an entryref (DO, with Args staged into
// $ZCMDLINE) or a single command (eval); a raw Script is passed verbatim after
// the trap. M commands are space-separated; the $ETRAP literal needs no quote
// doubling because the sentinel is $CHAR(1), not a quote.
func (s *Session) buildTrapped(req mdriver.ExecRequest) string {
	var b strings.Builder
	b.WriteString(trapClause())
	b.WriteByte(' ')
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
	return b.String()
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
