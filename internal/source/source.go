// Package source is m-ydb's view of the engine-side routine source: the .m
// files in a YottaDB instance's $ydb_routines source directories. For YottaDB
// the source IS the file on disk, so a Store lists/reads those files directly —
// over the host filesystem for the local transport (FileStore), or inside the
// managed container for the docker transport (ShellStore).
//
// The change signal for sync is the source file's modification time (epoch
// seconds, so it is identical whether read by stat locally or in the container);
// content integrity is the SHA-256 the mirror records. Routine names are the
// bare filenames (e.g. "FOO.m"); --filter matches the extension-stripped bare
// name (driver-contract §5.2).
package source

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Routine is one routine in the engine's source.
type Routine struct {
	Name string // filename, e.g. "FOO.m"
	TS   string // change signal: source mod-time, epoch seconds
}

// Store lists and reads .m source files from the engine's routine source
// directories, in search-path order (the first directory holding a given name
// wins, matching YottaDB's own resolution).
type Store interface {
	List(ctx context.Context) ([]Routine, error)
	Read(ctx context.Context, name string) ([]byte, error)
}

// nameRe restricts routine filenames to YottaDB's grammar, which also makes
// them safe to interpolate into the ShellStore scripts.
var nameRe = mustName()

func mustName() func(string) bool {
	// First char % or letter; then letters/digits; then ".m".
	return func(s string) bool {
		if !strings.HasSuffix(s, ".m") {
			return false
		}
		base := strings.TrimSuffix(s, ".m")
		if base == "" {
			return false
		}
		for i, r := range base {
			switch {
			case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
			case r == '%' && i == 0:
			case r >= '0' && r <= '9' && i > 0:
			default:
				return false
			}
		}
		return true
	}
}

// --- FileStore (local transport) ---------------------------------------------

// FileStore reads routine source from host-filesystem directories.
type FileStore struct {
	dirs []string
}

// NewFileStore builds a local-filesystem store over the given source dirs (in
// search-path order).
func NewFileStore(dirs []string) *FileStore { return &FileStore{dirs: dirs} }

func (s *FileStore) List(context.Context) ([]Routine, error) {
	seen := map[string]bool{}
	var out []Routine
	for _, dir := range s.dirs {
		entries, err := os.ReadDir(dir)
		if os.IsNotExist(err) {
			continue // a configured source dir that does not exist is just empty
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || filepath.Ext(name) != ".m" || seen[name] {
				continue
			}
			info, err := e.Info()
			if err != nil {
				return nil, err
			}
			seen[name] = true
			out = append(out, Routine{Name: name, TS: strconv.FormatInt(info.ModTime().Unix(), 10)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *FileStore) Read(_ context.Context, name string) ([]byte, error) {
	if !nameRe(name) {
		return nil, fmt.Errorf("source: invalid routine name %q", name)
	}
	for _, dir := range s.dirs {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err == nil {
			return b, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
	}
	return nil, fs.ErrNotExist
}

// --- ShellStore (docker transport) -------------------------------------------

// Sheller runs a /bin/sh -c script in the engine's filesystem context (inside
// the managed container for the docker transport) and returns its stdout and
// exit code. transport.Session implements it.
type Sheller interface {
	Sh(ctx context.Context, script string) (stdout string, code int, err error)
}

// ShellStore reads routine source from directories inside the engine container
// by running small shell scripts through a Sheller.
type ShellStore struct {
	sh   Sheller
	dirs []string
}

// NewShellStore builds a container store over the given source dirs.
func NewShellStore(sh Sheller, dirs []string) *ShellStore {
	return &ShellStore{sh: sh, dirs: dirs}
}

func (s *ShellStore) List(ctx context.Context) ([]Routine, error) {
	var b strings.Builder
	for _, dir := range s.dirs {
		// For each *.m in dir, print "<basename>\t<mtime-epoch>". Skip the glob
		// when nothing matches (the [ -e ] guard). stat -c is GNU coreutils.
		fmt.Fprintf(&b, `for f in '%s'/*.m; do [ -e "$f" ] || continue; printf '%%s\t%%s\n' "${f##*/}" "$(stat -c %%Y "$f" 2>/dev/null)"; done;`, dir)
	}
	out, _, err := s.sh.Sh(ctx, b.String())
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var rts []Routine
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		name, ts, ok := strings.Cut(line, "\t")
		if !ok || filepath.Ext(name) != ".m" || seen[name] {
			continue
		}
		seen[name] = true
		rts = append(rts, Routine{Name: name, TS: ts})
	}
	sort.Slice(rts, func(i, j int) bool { return rts[i].Name < rts[j].Name })
	return rts, nil
}

func (s *ShellStore) Read(ctx context.Context, name string) ([]byte, error) {
	if !nameRe(name) {
		return nil, fmt.Errorf("source: invalid routine name %q", name)
	}
	var b strings.Builder
	for _, dir := range s.dirs {
		fmt.Fprintf(&b, `if [ -f '%s/%s' ]; then cat '%s/%s'; exit 0; fi;`, dir, name, dir, name)
	}
	b.WriteString("exit 1")
	out, code, err := s.sh.Sh(ctx, b.String())
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fs.ErrNotExist
	}
	return []byte(out), nil
}

// --- helpers -----------------------------------------------------------------

// ParseRoutinesDirs extracts the source directories from a $ydb_routines value
// (best-effort). It keeps bare directories and the source dirs inside an
// `object(src1 src2)` group, strips a trailing `*` autorelink marker, and skips
// shared-object entries (.so/.o).
func ParseRoutinesDirs(ydbRoutines string) []string {
	var out []string
	add := func(tok string) {
		tok = strings.TrimSpace(tok)
		tok = strings.TrimSuffix(tok, ")") // stray close-paren from a group
		tok = strings.TrimSuffix(tok, "*") // autorelink marker
		if tok == "" || strings.HasSuffix(tok, ".so") || strings.HasSuffix(tok, ".o") {
			return
		}
		out = append(out, tok)
	}
	for _, tok := range strings.Fields(ydbRoutines) {
		if i := strings.IndexByte(tok, '('); i >= 0 {
			inner := tok[i+1:]
			inner = strings.TrimSuffix(inner, ")")
			for _, src := range strings.Fields(inner) {
				add(src)
			}
			continue
		}
		add(tok)
	}
	return out
}

// BareName strips a routine's extension: "FOO.m" → "FOO".
func BareName(name string) string {
	return strings.TrimSuffix(name, filepath.Ext(name))
}

// Match reports whether a routine name passes a bare-name glob (the extension
// is stripped before matching, per driver-contract §5.2). An empty glob matches
// everything.
func Match(name, glob string) (bool, error) {
	if glob == "" {
		return true, nil
	}
	ok, err := filepath.Match(glob, BareName(name))
	if err != nil {
		return false, fmt.Errorf("invalid --filter %q: %w", glob, err)
	}
	return ok, nil
}
