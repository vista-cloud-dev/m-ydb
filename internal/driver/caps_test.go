package driver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// TestCaps_GoldenJSON pins the m-ydb capability document (driver-contract.md §4):
// the wired axes/verbs, the two supported transports, and the feature flags. The
// golden file is the byte-for-byte contract m-cli reads via `meta caps`.
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

// TestCaps_Invariants asserts contract-level invariants independently of the
// golden bytes, so a wrong golden can't mask a real regression.
func TestCaps_Invariants(t *testing.T) {
	c := Caps()
	if c.Engine != "ydb" {
		t.Errorf("engine = %q, want ydb", c.Engine)
	}
	if c.Contract != mdriver.ContractVersion {
		t.Errorf("contract = %q, want %q", c.Contract, mdriver.ContractVersion)
	}
	if want := []string{"local", "docker", "remote"}; !equal(c.Transports, want) {
		t.Errorf("transports = %v, want %v", c.Transports, want)
	}
	// remote is the SSH host-shell transport (not a YottaDB network engine API):
	// it reaches a filesystem YottaDB on another host. It IS advertised.
	if !c.Features.Remote {
		t.Error("features.remote must be true (SSH host-shell transport)")
	}
	if !contains(c.Transports, mdriver.TransportRemote) {
		t.Error("transports must advertise remote (SSH)")
	}
	// Honest caps: every advertised (non-nil) axis must be non-empty — never
	// advertise an axis with no verbs.
	for name, verbs := range map[string][]string{
		"lifecycle": c.Axes.Lifecycle, "sync": c.Axes.Sync, "exec": c.Axes.Exec,
		"data": c.Axes.Data, "cover": c.Axes.Cover, "admin": c.Axes.Admin, "meta": c.Axes.Meta,
	} {
		if verbs != nil && len(verbs) == 0 {
			t.Errorf("axis %q is advertised but empty", name)
		}
	}
	// meta is always wired: caps must list itself and version.
	if !contains(c.Axes.Meta, "caps") || !contains(c.Axes.Meta, "version") {
		t.Errorf("meta axis = %v, must include caps and version", c.Axes.Meta)
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

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
