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

func TestSyncRm_RemovesFromInstanceAndManifest(t *testing.T) {
	conn := syncConn(t, map[string]string{"FOO.m": "FOO\n", "BAR.m": "BAR\n"})
	var buf bytes.Buffer
	if err := (&syncPullCmd{}).Run(jsonCtx(&buf, "sync pull"), conn); err != nil {
		t.Fatalf("pull: %v", err)
	}

	// rm accepts a bare name (".m" appended) and removes from instance + mirror.
	buf.Reset()
	if err := (&syncRmCmd{Name: "FOO"}).Run(jsonCtx(&buf, "sync rm"), conn); err != nil {
		t.Fatalf("rm: %v", err)
	}
	d := dataMap(t, &buf)
	if rm, _ := d["removed"].([]any); len(rm) != 1 || rm[0].(string) != "FOO.m" {
		t.Errorf("removed = %v, want [FOO.m]", d["removed"])
	}
	if _, err := os.Stat(filepath.Join(conn.Routines, "FOO.m")); !os.IsNotExist(err) {
		t.Error("FOO.m still in the instance source dir")
	}
	if _, err := os.Stat(filepath.Join(conn.Mirror, "FOO.m")); !os.IsNotExist(err) {
		t.Error("FOO.m still in the mirror")
	}

	// Removing an absent routine reports nothing removed (not an error).
	buf.Reset()
	if err := (&syncRmCmd{Name: "NOPE"}).Run(jsonCtx(&buf, "sync rm"), conn); err != nil {
		t.Fatalf("rm absent: %v", err)
	}
	if rm, _ := dataMap(t, &buf)["removed"].([]any); len(rm) != 0 {
		t.Errorf("removed = %v, want []", rm)
	}
}

func TestSyncRm_DryRunKeepsFile(t *testing.T) {
	conn := syncConn(t, map[string]string{"FOO.m": "FOO\n"})
	conn.DryRun = true
	var buf bytes.Buffer
	if err := (&syncRmCmd{Name: "FOO"}).Run(jsonCtx(&buf, "sync rm"), conn); err != nil {
		t.Fatalf("rm dry: %v", err)
	}
	if _, err := os.Stat(filepath.Join(conn.Routines, "FOO.m")); err != nil {
		t.Errorf("dry-run removed the file: %v", err)
	}
}

func TestSyncPush_FromMirror_ConflictChecked(t *testing.T) {
	conn := syncConn(t, map[string]string{"FOO.m": "FOO ;v1\n", "BAR.m": "BAR\n"})
	conn.Filter = "FOO"
	var buf bytes.Buffer
	if err := (&syncPullCmd{}).Run(jsonCtx(&buf, "sync pull"), conn); err != nil {
		t.Fatalf("pull: %v", err)
	}

	// Edit the mirror copy → push sends it back to the instance.
	if err := os.WriteFile(filepath.Join(conn.Mirror, "FOO.m"), []byte("FOO ;v2-edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := (&syncPushCmd{}).Run(jsonCtx(&buf, "sync push"), conn); err != nil {
		t.Fatalf("push: %v", err)
	}
	if pushed, _ := dataMap(t, &buf)["pushed"].([]any); len(pushed) != 1 {
		t.Errorf("pushed = %v, want [FOO.m]", dataMap(t, &buf)["pushed"])
	}
	got, _ := os.ReadFile(filepath.Join(conn.Routines, "FOO.m"))
	if string(got) != "FOO ;v2-edited\n" {
		t.Errorf("instance FOO.m = %q, want the pushed edit", got)
	}
}

func TestSyncPush_RefusesOnInstanceConflict(t *testing.T) {
	conn := syncConn(t, map[string]string{"FOO.m": "FOO ;v1\n"})
	conn.Filter = "FOO"
	var buf bytes.Buffer
	if err := (&syncPullCmd{}).Run(jsonCtx(&buf, "sync pull"), conn); err != nil {
		t.Fatalf("pull: %v", err)
	}
	if err := os.WriteFile(filepath.Join(conn.Mirror, "FOO.m"), []byte("FOO ;mine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Someone edits the instance out-of-band (newer mtime) after our pull.
	fp := filepath.Join(conn.Routines, "FOO.m")
	if err := os.WriteFile(fp, []byte("FOO ;theirs\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(2 * time.Hour)
	if err := os.Chtimes(fp, future, future); err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	err := (&syncPushCmd{}).Run(jsonCtx(&buf, "sync push"), conn)
	if exitCode(t, err) != clikit.ExitRefused {
		t.Fatalf("push exit = %d, want %d (conflict)", exitCode(t, err), clikit.ExitRefused)
	}
	if got, _ := os.ReadFile(fp); string(got) != "FOO ;theirs\n" {
		t.Errorf("instance clobbered despite conflict: %q", got)
	}

	// --force overrides the guard.
	buf.Reset()
	pc := &syncPushCmd{}
	pc.Force = true
	if err := pc.Run(jsonCtx(&buf, "sync push"), conn); err != nil {
		t.Fatalf("push --force: %v", err)
	}
	if got, _ := os.ReadFile(fp); string(got) != "FOO ;mine\n" {
		t.Errorf("force push didn't win: %q", got)
	}
}

func TestSyncPush_FromDir(t *testing.T) {
	conn := syncConn(t, nil) // empty instance
	from := t.TempDir()
	if err := os.WriteFile(filepath.Join(from, "NEW.m"), []byte("NEW ;x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	pc := &syncPushCmd{}
	pc.From = from
	if err := pc.Run(jsonCtx(&buf, "sync push"), conn); err != nil {
		t.Fatalf("push --from: %v", err)
	}
	if got, _ := os.ReadFile(filepath.Join(conn.Routines, "NEW.m")); string(got) != "NEW ;x\n" {
		t.Errorf("instance NEW.m = %q", got)
	}
	// push --from also lands the routine in the mirror (so verify stays coherent).
	if _, err := os.Stat(filepath.Join(conn.Mirror, "NEW.m")); err != nil {
		t.Errorf("mirror NEW.m missing after push --from: %v", err)
	}
}

func TestSyncDeploy_InstallAndPrune(t *testing.T) {
	conn := syncConn(t, map[string]string{"STDOLD.m": "old\n", "XUSER.m": "x\n"})
	lib := t.TempDir()
	for _, n := range []string{"STDA.m", "STDB.m"} {
		if err := os.WriteFile(filepath.Join(lib, n), []byte(n+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	dc := &syncDeployCmd{Dir: lib}
	dc.Prune = true
	if err := dc.Run(jsonCtx(&buf, "sync deploy"), conn); err != nil {
		t.Fatalf("deploy --prune: %v", err)
	}
	d := dataMap(t, &buf)
	if inst, _ := d["installed"].([]any); len(inst) != 2 {
		t.Errorf("installed = %v, want 2", d["installed"])
	}
	if pr, _ := d["pruned"].([]any); len(pr) != 1 || pr[0].(string) != "STDOLD.m" {
		t.Errorf("pruned = %v, want [STDOLD.m]", d["pruned"])
	}
	// STD-prefixed resident removed; unrelated XUSER untouched; library installed.
	for name, wantExist := range map[string]bool{
		"STDA.m": true, "STDB.m": true, "STDOLD.m": false, "XUSER.m": true,
	} {
		_, err := os.Stat(filepath.Join(conn.Routines, name))
		if wantExist && err != nil {
			t.Errorf("%s should exist: %v", name, err)
		}
		if !wantExist && !os.IsNotExist(err) {
			t.Errorf("%s should be pruned", name)
		}
	}
}

func TestSyncDeploy_PruneRefusesWithoutCommonPrefix(t *testing.T) {
	conn := syncConn(t, map[string]string{"RES.m": "r\n"})
	lib := t.TempDir()
	for _, n := range []string{"STDA.m", "DGREG.m"} { // no common bare-name prefix
		if err := os.WriteFile(filepath.Join(lib, n), []byte(n+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	dc := &syncDeployCmd{Dir: lib}
	dc.Prune = true
	err := dc.Run(jsonCtx(&buf, "sync deploy"), conn)
	if exitCode(t, err) != clikit.ExitRefused {
		t.Fatalf("deploy --prune exit = %d, want %d (no safe prune scope)", exitCode(t, err), clikit.ExitRefused)
	}
}

func TestSyncDiff_InstanceVsMirror(t *testing.T) {
	conn := syncConn(t, map[string]string{"FOO.m": "line1\nline2\nline3\n"})
	var buf bytes.Buffer
	if err := (&syncPullCmd{}).Run(jsonCtx(&buf, "sync pull"), conn); err != nil {
		t.Fatalf("pull: %v", err)
	}
	// Edit the mirror; diff shows what push would change on the instance.
	if err := os.WriteFile(filepath.Join(conn.Mirror, "FOO.m"), []byte("line1\nEDITED\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := (&syncDiffCmd{Name: "FOO"}).Run(jsonCtx(&buf, "sync diff"), conn); err != nil {
		t.Fatalf("diff: %v", err)
	}
	u, _ := dataMap(t, &buf)["unified"].(string)
	if !contains2(u, "-line2") || !contains2(u, "+EDITED") {
		t.Errorf("unified diff missing expected changes:\n%s", u)
	}

	// Identical → empty diff.
	if err := os.WriteFile(filepath.Join(conn.Mirror, "FOO.m"), []byte("line1\nline2\nline3\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	buf.Reset()
	if err := (&syncDiffCmd{Name: "FOO"}).Run(jsonCtx(&buf, "sync diff"), conn); err != nil {
		t.Fatalf("diff2: %v", err)
	}
	if u, _ := dataMap(t, &buf)["unified"].(string); u != "" {
		t.Errorf("expected empty diff, got:\n%s", u)
	}
}

func contains2(s, sub string) bool {
	return bytes.Contains([]byte(s), []byte(sub))
}

func TestSyncVerify_NoManifestIsRuntimeError(t *testing.T) {
	conn := syncConn(t, map[string]string{"FOO.m": "FOO\n"})
	var buf bytes.Buffer
	err := (syncVerifyCmd{}).Run(jsonCtx(&buf, "sync verify"), conn)
	if exitCode(t, err) != clikit.ExitRuntime {
		t.Fatalf("verify without manifest exit = %d, want %d", exitCode(t, err), clikit.ExitRuntime)
	}
}
