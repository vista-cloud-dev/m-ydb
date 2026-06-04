package source

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFileStore_ListAndRead(t *testing.T) {
	d1 := filepath.Join(t.TempDir(), "src1")
	d2 := filepath.Join(t.TempDir(), "src2")
	writeFile(t, d1, "FOO.m", "FOO ;one\n quit\n")
	writeFile(t, d1, "BAR.m", "BAR\n")
	writeFile(t, d1, "notes.txt", "ignored\n") // non-.m ignored
	writeFile(t, d2, "BAZ.m", "BAZ\n")
	writeFile(t, d2, "FOO.m", "FOO ;shadowed\n") // earlier dir wins

	st := NewFileStore([]string{d1, d2})
	rts, err := st.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	names := map[string]bool{}
	for _, r := range rts {
		names[r.Name] = true
		if r.TS == "" {
			t.Errorf("%s has empty TS", r.Name)
		}
	}
	for _, want := range []string{"FOO.m", "BAR.m", "BAZ.m"} {
		if !names[want] {
			t.Errorf("missing %s in %v", want, names)
		}
	}
	if names["notes.txt"] {
		t.Error("non-.m file listed")
	}
	if len(rts) != 3 {
		t.Errorf("count = %d, want 3 (first-dir-wins on FOO.m)", len(rts))
	}

	// Read resolves through the search path, earlier dir winning.
	b, err := st.Read(context.Background(), "FOO.m")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(b) != "FOO ;one\n quit\n" {
		t.Errorf("read FOO.m = %q, want the src1 copy", b)
	}
	if _, err := st.Read(context.Background(), "NOPE.m"); !os.IsNotExist(err) {
		t.Errorf("read missing: err = %v, want IsNotExist", err)
	}
}

func TestFileStore_WriteAndRemove(t *testing.T) {
	d0 := filepath.Join(t.TempDir(), "primary")
	d1 := filepath.Join(t.TempDir(), "secondary")
	writeFile(t, d1, "OLD.m", "OLD\n")
	st := NewFileStore([]string{d0, d1})
	ctx := context.Background()

	// Write lands in the primary (first) dir and returns a non-empty TS.
	rt, err := st.Write(ctx, "NEW.m", []byte("NEW ;body\n quit\n"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if rt.Name != "NEW.m" || rt.TS == "" {
		t.Errorf("write result = %+v", rt)
	}
	got, err := os.ReadFile(filepath.Join(d0, "NEW.m"))
	if err != nil || string(got) != "NEW ;body\n quit\n" {
		t.Fatalf("primary write = %q err=%v", got, err)
	}

	// Read resolves the freshly written routine.
	if b, err := st.Read(ctx, "NEW.m"); err != nil || string(b) != "NEW ;body\n quit\n" {
		t.Fatalf("read back = %q err=%v", b, err)
	}

	// Remove deletes from the primary dir.
	writeFile(t, d0, "GONE.m", "GONE\n")
	if err := st.Remove(ctx, "GONE.m"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d0, "GONE.m")); !os.IsNotExist(err) {
		t.Error("GONE.m still present after remove")
	}
	// Removing a name absent from the primary dir is a no-op (not an error).
	if err := st.Remove(ctx, "OLD.m"); err != nil {
		t.Errorf("remove of non-primary routine = %v, want nil (no-op)", err)
	}
}

// fakeSh records the scripts it runs and replays canned stdout keyed by a
// substring match, so ShellStore can be unit-tested without a container.
type fakeSh struct {
	responses []struct{ contains, stdout string }
	capture   *string // if set, records the last script run
}

func (f *fakeSh) Sh(_ context.Context, script string) (string, int, error) {
	if f.capture != nil {
		*f.capture = script
	}
	for _, r := range f.responses {
		if contains(script, r.contains) {
			return r.stdout, 0, nil
		}
	}
	return "", 0, nil
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func TestShellStore_ListParsesTabbedOutput(t *testing.T) {
	sh := &fakeSh{responses: []struct{ contains, stdout string }{
		{"stat", "FOO.m\t1700000000\nBAR.m\t1700000005\nFOO.m\t1699999999\n"},
	}}
	st := NewShellStore(sh, []string{"/data/r"})
	rts, err := st.List(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rts) != 2 {
		t.Fatalf("count = %d, want 2 (first FOO.m wins)", len(rts))
	}
	byName := map[string]string{}
	for _, r := range rts {
		byName[r.Name] = r.TS
	}
	if byName["FOO.m"] != "1700000000" {
		t.Errorf("FOO.m TS = %q, want first occurrence 1700000000", byName["FOO.m"])
	}
	if byName["BAR.m"] != "1700000005" {
		t.Errorf("BAR.m TS = %q", byName["BAR.m"])
	}
}

func TestShellStore_Read(t *testing.T) {
	sh := &fakeSh{responses: []struct{ contains, stdout string }{
		{"cat", "FOO ;body\n quit\n"},
	}}
	st := NewShellStore(sh, []string{"/data/r"})
	b, err := st.Read(context.Background(), "FOO.m")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(b) != "FOO ;body\n quit\n" {
		t.Errorf("read = %q", b)
	}
}

func TestShellStore_WriteEncodesAndReturnsTS(t *testing.T) {
	var gotScript string
	sh := &fakeSh{responses: []struct{ contains, stdout string }{
		{"base64 -d", "1700000123\n"}, // write script ends by stat-ing the new file
	}}
	sh.capture = &gotScript
	st := NewShellStore(sh, []string{"/data/r"})
	rt, err := st.Write(context.Background(), "FOO.m", []byte("FOO\n quit\n"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if rt.Name != "FOO.m" || rt.TS != "1700000123" {
		t.Errorf("write result = %+v, want {FOO.m 1700000123}", rt)
	}
	// Content must be base64-encoded into the script (no raw newlines / quoting hazards).
	if !contains(gotScript, "base64 -d") || contains(gotScript, "FOO\n quit") {
		t.Errorf("script did not base64-encode content: %q", gotScript)
	}
}

func TestShellStore_Remove(t *testing.T) {
	var gotScript string
	sh := &fakeSh{}
	sh.capture = &gotScript
	st := NewShellStore(sh, []string{"/data/r"})
	if err := st.Remove(context.Background(), "FOO.m"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if !contains(gotScript, "rm -f") || !contains(gotScript, "/data/r/FOO.m") {
		t.Errorf("remove script = %q", gotScript)
	}
}

func TestParseRoutinesDirs(t *testing.T) {
	tests := []struct {
		in   string
		want []string
	}{
		{"/data/r", []string{"/data/r"}},
		{"/data/r /more/src", []string{"/data/r", "/more/src"}},
		// object(source-list): only the source dirs in parens count.
		{"/obj(/src1 /src2)", []string{"/src1", "/src2"}},
		// autorelink star and shared objects are stripped/skipped.
		{"/data/r* /opt/ydb/libyottadbutil.so", []string{"/data/r"}},
		{"", nil},
	}
	for _, tt := range tests {
		got := ParseRoutinesDirs(tt.in)
		if fmt.Sprint(got) != fmt.Sprint(tt.want) {
			t.Errorf("ParseRoutinesDirs(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestMatch_BareNameExtensionInsensitive(t *testing.T) {
	tests := []struct {
		name, glob string
		want       bool
	}{
		{"DGREG.m", "", true},      // empty glob matches all
		{"DGREG.m", "DG*", true},   // prefix glob on bare name
		{"DGREG.m", "DGREG", true}, // exact bare name
		{"XUSER.m", "DG*", false},
		{"DGREG.m", "*.m", false}, // glob is bare-name, so ".m" never matches
	}
	for _, tt := range tests {
		got, err := Match(tt.name, tt.glob)
		if err != nil {
			t.Fatalf("Match(%q,%q): %v", tt.name, tt.glob, err)
		}
		if got != tt.want {
			t.Errorf("Match(%q,%q) = %v, want %v", tt.name, tt.glob, got, tt.want)
		}
	}
}
