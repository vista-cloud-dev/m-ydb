// Package manifest models .m-ydb-manifest.json — the per-mirror record that
// makes the mirror an incremental cache (pull fetches only new/changed) and a
// verifiable artifact (verify re-hashes against it). For YottaDB the source is
// the .m file on the filesystem, so the change signal is the source file's
// modification time (SourceTS) and the integrity hash is its SHA-256.
//
// The shape mirrors m-iris's manifest deliberately so the two drivers feel
// identical to a human reading both trees, but the packages are independent
// (no shared source beyond the SDK — consistency-protocol).
package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// Schema is the manifest format version.
const Schema = 1

// Name is the per-mirror manifest filename.
const Name = ".m-ydb-manifest.json"

// Entry records one routine's last-pulled source state and content hash. The
// key in Manifest.Routines is the routine filename (e.g. "FOO.m").
type Entry struct {
	SourceTS string `json:"sourceTS"` // source file mod-time at pull (change signal)
	SHA256   string `json:"sha256"`   // content hash of the mirrored copy
	Bytes    int    `json:"bytes"`
}

// Manifest is the full per-mirror record. Routines is keyed by routine filename.
type Manifest struct {
	Schema   int              `json:"schema"`
	PulledAt string           `json:"pulledAt"`
	Routines map[string]Entry `json:"routines"`
}

// New returns an empty manifest.
func New() *Manifest {
	return &Manifest{Schema: Schema, Routines: map[string]Entry{}}
}

// Diff classifies a source listing against the manifest. Each field holds
// routine names, sorted.
type Diff struct {
	New       []string `json:"new"`
	Changed   []string `json:"changed"`
	Deleted   []string `json:"deleted"`
	Unchanged []string `json:"unchanged"`
}

// Drift reports whether the mirror is out of sync with the source (anything
// new, changed, or deleted). Unchanged-only is in sync.
func (d Diff) Drift() bool {
	return len(d.New)+len(d.Changed)+len(d.Deleted) > 0
}

// ToPull returns the names pull must fetch: new + changed, sorted.
func (d Diff) ToPull() []string {
	out := make([]string, 0, len(d.New)+len(d.Changed))
	out = append(out, d.New...)
	out = append(out, d.Changed...)
	sort.Strings(out)
	return out
}

// Compare classifies source names (name → source timestamp) against the
// manifest. A nil manifest is treated as empty (nothing pulled yet), so every
// source routine is New.
func Compare(source map[string]string, m *Manifest) Diff {
	recorded := map[string]Entry{}
	if m != nil {
		recorded = m.Routines
	}
	var d Diff
	for name, ts := range source {
		switch e, ok := recorded[name]; {
		case !ok:
			d.New = append(d.New, name)
		case e.SourceTS != ts:
			d.Changed = append(d.Changed, name)
		default:
			d.Unchanged = append(d.Unchanged, name)
		}
	}
	for name := range recorded {
		if _, ok := source[name]; !ok {
			d.Deleted = append(d.Deleted, name)
		}
	}
	sort.Strings(d.New)
	sort.Strings(d.Changed)
	sort.Strings(d.Deleted)
	sort.Strings(d.Unchanged)
	return d
}

// Load reads a manifest from path. A missing file is not an error: it returns
// (nil, nil) so callers can treat "never pulled" distinctly from a parse error.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("manifest: parse %s: %w", path, err)
	}
	if m.Routines == nil {
		m.Routines = map[string]Entry{}
	}
	return &m, nil
}

// Save writes the manifest to path atomically. Map keys marshal in sorted order,
// so the file is deterministic and diffs cleanly in git.
func Save(path string, m *Manifest) error {
	if m.Schema == 0 {
		m.Schema = Schema
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".m-ydb-manifest-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}
