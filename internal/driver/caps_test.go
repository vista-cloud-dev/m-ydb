package driver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/vista-cloud-dev/m-ydb/internal/contract"
)

// TestCaps_GoldenJSON pins the m-ydb capability document (contract §4): the
// exact axes/verbs, the two supported transports, and the feature flags. The
// golden file is the byte-for-byte contract m-cli reads via `caps`.
func TestCaps_GoldenJSON(t *testing.T) {
	got, err := json.MarshalIndent(Caps(), "", "  ")
	if err != nil {
		t.Fatalf("marshal caps: %v", err)
	}
	got = append(got, '\n')

	golden := filepath.Join("testdata", "caps.json")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(golden, got, 0o644); err != nil {
			t.Fatalf("update golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("caps JSON mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// TestCaps_Invariants asserts the contract-level invariants independently of
// the golden bytes, so a wrong golden can't mask a real regression.
func TestCaps_Invariants(t *testing.T) {
	c := Caps()
	if c.Engine != "ydb" {
		t.Errorf("engine = %q, want ydb", c.Engine)
	}
	if c.Contract != contract.Version {
		t.Errorf("contract = %q, want %q", c.Contract, contract.Version)
	}
	if want := []string{"local", "docker"}; !equal(c.Transports, want) {
		t.Errorf("transports = %v, want %v", c.Transports, want)
	}
	// YottaDB has no network API — remote must never be advertised (risk: caps honesty).
	if c.Features.Remote {
		t.Error("features.remote must be false for YottaDB")
	}
	for _, tr := range c.Transports {
		if tr == contract.TransportRemote {
			t.Errorf("transports advertise %q, which YottaDB does not support", tr)
		}
	}
	// Every advertised axis must be non-empty (caps honesty: no empty axis).
	axes := map[string][]string{
		"lifecycle": c.Axes.Lifecycle, "sync": c.Axes.Sync, "exec": c.Axes.Exec,
		"data": c.Axes.Data, "cover": c.Axes.Cover, "admin": c.Axes.Admin, "meta": c.Axes.Meta,
	}
	for name, verbs := range axes {
		if len(verbs) == 0 {
			t.Errorf("axis %q has no verbs", name)
		}
	}
}

func equal(a, b []string) bool {
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
