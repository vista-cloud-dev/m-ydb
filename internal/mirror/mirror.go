// Package mirror writes and reads the on-disk .m mirror that m-ydb sync
// materializes from a YottaDB instance's routine source. The mirror is the
// input to file-based M tooling (m-cli's FilesystemSourceProvider), so writes
// are atomic and line endings are normalized — the tree must stay git-stable
// and tree-sitter-parseable.
//
// For YottaDB the source already lives on a filesystem (the .m files in
// $ydb_routines), so the mirror is a near-identity copy keyed by routine
// filename. The layout is flat (no namespace segmentation — YottaDB has regions,
// not namespaces); the manifest, keyed by filename, is the source of truth.
package mirror

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"

	"github.com/vista-cloud-dev/m-ydb/internal/manifest"
)

// Layout resolves paths within a mirror root:
//
//	<root>/<ROUTINE>.m
//	<root>/.m-ydb-manifest.json
type Layout struct {
	Root string
}

// RoutinePath is the on-disk path for a routine filename (e.g. "FOO.m").
func (l Layout) RoutinePath(name string) string {
	return filepath.Join(l.Root, name)
}

// ManifestPath is the mirror manifest file path.
func (l Layout) ManifestPath() string {
	return filepath.Join(l.Root, manifest.Name)
}

// WriteResult reports what WriteRoutine persisted.
type WriteResult struct {
	SHA256 string
	Bytes  int
}

// WriteRoutine writes a routine's source to path atomically (temp file in the
// same directory + rename). Line endings are normalized to "\n" with a single
// trailing newline so the mirror is byte-stable across pulls.
func WriteRoutine(path string, content []byte) (WriteResult, error) {
	body := normalize(content)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return WriteResult{}, err
	}
	if err := atomicWrite(path, body); err != nil {
		return WriteResult{}, err
	}
	sum := sha256.Sum256(body)
	return WriteResult{SHA256: hex.EncodeToString(sum[:]), Bytes: len(body)}, nil
}

// HashFile returns the sha256 (hex) and byte length of an existing file.
func HashFile(path string) (sum string, n int, err error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	written, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), int(written), nil
}

// Normalize is the exported form of the mirror's line-ending normalization, so
// the push path can compute the canonical bytes it writes to both the mirror
// and the instance (keeping their SHA-256 identical).
func Normalize(content []byte) []byte { return normalize(content) }

// normalize rewrites content to LF line endings with exactly one trailing
// newline, stripping a trailing CR from each line.
func normalize(content []byte) []byte {
	lines := bytes.Split(content, []byte("\n"))
	// Drop a trailing empty element from a final newline so we don't double it.
	if n := len(lines); n > 0 && len(lines[n-1]) == 0 {
		lines = lines[:n-1]
	}
	var b bytes.Buffer
	for _, ln := range lines {
		b.Write(bytes.TrimRight(ln, "\r"))
		b.WriteByte('\n')
	}
	return b.Bytes()
}

// atomicWrite writes data to a temp file in path's directory, then renames it
// over path so a reader never sees a partial file.
func atomicWrite(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".m-ydb-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op once the rename succeeds

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
