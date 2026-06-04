package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vista-cloud-dev/m-ydb/clikit"
	"github.com/vista-cloud-dev/m-ydb/internal/config"
)

// syncConn builds a local-transport connection over a temp source dir + temp
// mirror, and writes the given routines into the source dir.
func syncConn(t *testing.T, routines map[string]string) *config.Conn {
	t.Helper()
	src := t.TempDir()
	mir := t.TempDir()
	for name, body := range routines {
		if err := os.WriteFile(filepath.Join(src, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &config.Conn{Transport: "local", Routines: src, Mirror: mir}
}

func exitCode(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var e *clikit.Error
	if !errors.As(err, &e) {
		t.Fatalf("expected *clikit.Error, got %T: %v", err, err)
	}
	return e.Exit
}

func dataMap(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()
	env := decodeEnvelope(t, buf.Bytes())
	m, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("data not an object: %#v", env.Data)
	}
	return m
}

func TestSyncList_Envelope(t *testing.T) {
	conn := syncConn(t, map[string]string{
		"FOO.m": "FOO\n", "BAR.m": "BAR\n", "XUSER.m": "XUSER\n",
	})
	var buf bytes.Buffer
	if err := (syncListCmd{}).Run(jsonCtx(&buf, "sync list"), conn); err != nil {
		t.Fatalf("list: %v", err)
	}
	d := dataMap(t, &buf)
	if d["count"].(float64) != 3 {
		t.Errorf("count = %v, want 3", d["count"])
	}
}

func TestSyncList_BareNameFilter(t *testing.T) {
	conn := syncConn(t, map[string]string{"FOO.m": "FOO\n", "BAR.m": "BAR\n", "FIZZ.m": "FIZZ\n"})
	conn.Filter = "F*"
	var buf bytes.Buffer
	if err := (syncListCmd{}).Run(jsonCtx(&buf, "sync list"), conn); err != nil {
		t.Fatalf("list: %v", err)
	}
	d := dataMap(t, &buf)
	if d["count"].(float64) != 2 { // FOO, FIZZ — not BAR
		t.Errorf("filtered count = %v, want 2", d["count"])
	}
}

func TestSyncPull_MaterializesAndIsIncremental(t *testing.T) {
	conn := syncConn(t, map[string]string{"FOO.m": "FOO ;x\r\n quit\r\n", "BAR.m": "BAR\n"})

	var buf bytes.Buffer
	if err := (&syncPullCmd{}).Run(jsonCtx(&buf, "sync pull"), conn); err != nil {
		t.Fatalf("pull: %v", err)
	}
	d := dataMap(t, &buf)
	if d["pulled"].(float64) != 2 {
		t.Fatalf("pulled = %v, want 2", d["pulled"])
	}
	// Mirror content is normalized to LF + trailing newline.
	got, err := os.ReadFile(filepath.Join(conn.Mirror, "FOO.m"))
	if err != nil {
		t.Fatalf("read mirror: %v", err)
	}
	if string(got) != "FOO ;x\n quit\n" {
		t.Errorf("mirror FOO.m = %q", got)
	}

	// Second pull: nothing changed.
	buf.Reset()
	if err := (&syncPullCmd{}).Run(jsonCtx(&buf, "sync pull"), conn); err != nil {
		t.Fatalf("pull2: %v", err)
	}
	d = dataMap(t, &buf)
	if d["pulled"].(float64) != 0 || d["unchanged"].(float64) != 2 {
		t.Errorf("pull2 pulled=%v unchanged=%v, want 0/2", d["pulled"], d["unchanged"])
	}

	// Delete a source routine → pull prunes it from the mirror.
	if err := os.Remove(filepath.Join(conn.Routines, "BAR.m")); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := (&syncPullCmd{}).Run(jsonCtx(&buf, "sync pull"), conn); err != nil {
		t.Fatalf("pull3: %v", err)
	}
	d = dataMap(t, &buf)
	if d["deleted"].(float64) != 1 {
		t.Errorf("deleted = %v, want 1", d["deleted"])
	}
	if _, err := os.Stat(filepath.Join(conn.Mirror, "BAR.m")); !os.IsNotExist(err) {
		t.Error("BAR.m still in mirror after source deletion")
	}
}

func TestSyncPull_DryRunWritesNothing(t *testing.T) {
	conn := syncConn(t, map[string]string{"FOO.m": "FOO\n"})
	conn.DryRun = true
	var buf bytes.Buffer
	if err := (&syncPullCmd{}).Run(jsonCtx(&buf, "sync pull"), conn); err != nil {
		t.Fatalf("pull dry: %v", err)
	}
	if _, err := os.Stat(filepath.Join(conn.Mirror, "FOO.m")); !os.IsNotExist(err) {
		t.Error("dry-run wrote a mirror file")
	}
	if _, err := os.Stat(conn.Layout().ManifestPath()); !os.IsNotExist(err) {
		t.Error("dry-run wrote a manifest")
	}
}

func TestSyncStatus_DriftExit3(t *testing.T) {
	conn := syncConn(t, map[string]string{"FOO.m": "FOO\n"})
	var buf bytes.Buffer
	if err := (&syncPullCmd{}).Run(jsonCtx(&buf, "sync pull"), conn); err != nil {
		t.Fatalf("pull: %v", err)
	}

	// In sync → exit 0, drift false.
	buf.Reset()
	err := (syncStatusCmd{}).Run(jsonCtx(&buf, "sync status"), conn)
	if exitCode(t, err) != 0 {
		t.Fatalf("status exit = %d, want 0", exitCode(t, err))
	}
	if dataMap(t, &buf)["drift"].(bool) {
		t.Error("drift = true on a clean mirror")
	}

	// Change the source routine (content + a clearly newer mtime).
	fp := filepath.Join(conn.Routines, "FOO.m")
	if err := os.WriteFile(fp, []byte("FOO ;edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(fp, future, future); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	err = (syncStatusCmd{}).Run(jsonCtx(&buf, "sync status"), conn)
	if exitCode(t, err) != clikit.ExitCheck {
		t.Fatalf("status exit = %d, want %d (drift)", exitCode(t, err), clikit.ExitCheck)
	}
	if !dataMap(t, &buf)["drift"].(bool) {
		t.Error("drift = false after a source edit")
	}
}

func TestSyncVerify_MismatchExit3(t *testing.T) {
	conn := syncConn(t, map[string]string{"FOO.m": "FOO\n"})
	var buf bytes.Buffer
	if err := (&syncPullCmd{}).Run(jsonCtx(&buf, "sync pull"), conn); err != nil {
		t.Fatalf("pull: %v", err)
	}

	buf.Reset()
	if exitCode(t, (syncVerifyCmd{}).Run(jsonCtx(&buf, "sync verify"), conn)) != 0 {
		t.Fatal("verify of a fresh mirror should be clean")
	}

	// Corrupt the mirror copy → verify must flag a mismatch (exit 3).
	if err := os.WriteFile(filepath.Join(conn.Mirror, "FOO.m"), []byte("tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	err := (syncVerifyCmd{}).Run(jsonCtx(&buf, "sync verify"), conn)
	if exitCode(t, err) != clikit.ExitCheck {
		t.Fatalf("verify exit = %d, want %d", exitCode(t, err), clikit.ExitCheck)
	}
	d := dataMap(t, &buf)
	if mm, _ := d["mismatches"].([]any); len(mm) != 1 {
		t.Errorf("mismatches = %v, want 1", d["mismatches"])
	}
}

func TestSyncVerify_NoManifestIsRuntimeError(t *testing.T) {
	conn := syncConn(t, map[string]string{"FOO.m": "FOO\n"})
	var buf bytes.Buffer
	err := (syncVerifyCmd{}).Run(jsonCtx(&buf, "sync verify"), conn)
	if exitCode(t, err) != clikit.ExitRuntime {
		t.Fatalf("verify without manifest exit = %d, want %d", exitCode(t, err), clikit.ExitRuntime)
	}
}
