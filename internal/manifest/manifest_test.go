package manifest

import (
	"path/filepath"
	"testing"
)

func TestCompare_ClassifiesAgainstManifest(t *testing.T) {
	m := New()
	m.Routines["FOO.m"] = Entry{SourceTS: "t1", SHA256: "a", Bytes: 3}
	m.Routines["BAR.m"] = Entry{SourceTS: "t1", SHA256: "b", Bytes: 3}
	// GONE.m recorded but absent on the source → deleted.
	m.Routines["GONE.m"] = Entry{SourceTS: "t1", SHA256: "c", Bytes: 3}

	source := map[string]string{
		"FOO.m": "t1", // unchanged
		"BAR.m": "t2", // changed (different ts)
		"NEW.m": "t1", // new
	}
	d := Compare(source, m)

	if got := d.New; len(got) != 1 || got[0] != "NEW.m" {
		t.Errorf("new = %v, want [NEW.m]", got)
	}
	if got := d.Changed; len(got) != 1 || got[0] != "BAR.m" {
		t.Errorf("changed = %v, want [BAR.m]", got)
	}
	if got := d.Deleted; len(got) != 1 || got[0] != "GONE.m" {
		t.Errorf("deleted = %v, want [GONE.m]", got)
	}
	if got := d.Unchanged; len(got) != 1 || got[0] != "FOO.m" {
		t.Errorf("unchanged = %v, want [FOO.m]", got)
	}
	if !d.Drift() {
		t.Error("Drift() = false, want true")
	}
	if want := []string{"BAR.m", "NEW.m"}; !eq(d.ToPull(), want) {
		t.Errorf("ToPull() = %v, want %v", d.ToPull(), want)
	}
}

func TestCompare_NilManifestIsAllNew(t *testing.T) {
	d := Compare(map[string]string{"A.m": "t", "B.m": "t"}, nil)
	if want := []string{"A.m", "B.m"}; !eq(d.New, want) {
		t.Errorf("new = %v, want %v", d.New, want)
	}
	if d.Drift() != true {
		t.Error("expected drift with a fresh listing")
	}
}

func TestCompare_InSyncHasNoDrift(t *testing.T) {
	m := New()
	m.Routines["A.m"] = Entry{SourceTS: "t", SHA256: "h", Bytes: 1}
	d := Compare(map[string]string{"A.m": "t"}, m)
	if d.Drift() {
		t.Errorf("drift = true, want false (%+v)", d)
	}
	if len(d.Unchanged) != 1 {
		t.Errorf("unchanged = %v, want 1", d.Unchanged)
	}
}

func TestLoadSave_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".m-ydb-manifest.json")

	// Missing file → (nil, nil), not an error.
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if got != nil {
		t.Fatalf("load missing = %+v, want nil", got)
	}

	m := New()
	m.Routines["FOO.m"] = Entry{SourceTS: "t1", SHA256: "abc", Bytes: 12}
	m.PulledAt = "2026-06-04T00:00:00Z"
	if err := Save(path, m); err != nil {
		t.Fatalf("save: %v", err)
	}

	back, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if back == nil || back.Routines["FOO.m"].SHA256 != "abc" {
		t.Fatalf("round-trip lost data: %+v", back)
	}
	if back.Schema != Schema {
		t.Errorf("schema = %d, want %d", back.Schema, Schema)
	}
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
