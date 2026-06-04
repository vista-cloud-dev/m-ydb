package mirror

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLayout_Paths(t *testing.T) {
	l := Layout{Root: "/tmp/cache"}
	if got, want := l.RoutinePath("FOO.m"), filepath.Join("/tmp/cache", "FOO.m"); got != want {
		t.Errorf("RoutinePath = %q, want %q", got, want)
	}
	if got, want := l.ManifestPath(), filepath.Join("/tmp/cache", ".m-ydb-manifest.json"); got != want {
		t.Errorf("ManifestPath = %q, want %q", got, want)
	}
}

func TestWriteRoutine_NormalizesAndHashes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "FOO.m")

	// CRLF + missing trailing newline → normalized to LF + one trailing \n.
	wr, err := WriteRoutine(path, []byte("FOO ;test\r\n quit\r\n"))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if want := "FOO ;test\n quit\n"; string(got) != want {
		t.Errorf("content = %q, want %q", got, want)
	}
	if wr.Bytes != len(got) {
		t.Errorf("bytes = %d, want %d", wr.Bytes, len(got))
	}

	// HashFile must agree with the WriteResult hash/size.
	sum, n, err := HashFile(path)
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if sum != wr.SHA256 || n != wr.Bytes {
		t.Errorf("hash/size mismatch: file=(%s,%d) write=(%s,%d)", sum, n, wr.SHA256, wr.Bytes)
	}
}

func TestWriteRoutine_CreatesParentDirs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nested", "deep", "BAR.m")
	if _, err := WriteRoutine(path, []byte("BAR\n")); err != nil {
		t.Fatalf("write into missing dirs: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

func TestHashFile_MissingIsNotExist(t *testing.T) {
	_, _, err := HashFile(filepath.Join(t.TempDir(), "nope.m"))
	if !os.IsNotExist(err) {
		t.Errorf("err = %v, want IsNotExist", err)
	}
}
