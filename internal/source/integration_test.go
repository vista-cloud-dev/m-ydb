package source

import (
	"context"
	"os"
	"sort"
	"testing"

	"github.com/vista-cloud-dev/m-ydb/internal/transport"
)

// TestRealShellStore exercises the docker-transport ShellStore against a real
// YottaDB container (the gated integration tier — plan §1). It stages two .m
// files in a scratch directory inside the container (via the session shell, the
// sanctioned engine path), then lists + reads them back through the ShellStore,
// and removes the scratch dir. It never touches the engine database or the
// shared container's routine paths. Run with:
//
//	M_YDB_IT=1 M_YDB_CONTAINER=m-test-engine go test ./internal/source/ -run RealShellStore
func TestRealShellStore(t *testing.T) {
	if os.Getenv("M_YDB_IT") != "1" {
		t.Skip("gated: set M_YDB_IT=1 (+ a running YottaDB container) to run the real-engine tier")
	}
	container := os.Getenv("M_YDB_CONTAINER")
	if container == "" {
		container = "m-test-engine"
	}
	sess := transport.NewSession(transport.Config{Transport: "docker", Container: container}, nil)
	ctx := context.Background()

	const dir = "/tmp/m-ydb-it-src"
	// Stage fixtures inside the container; clean up afterwards.
	setup := `set -e; rm -rf '` + dir + `'; mkdir -p '` + dir + `'; ` +
		`printf 'ZZTFOO ;it\n quit\n' > '` + dir + `/ZZTFOO.m'; ` +
		`printf 'ZZTBAR\n' > '` + dir + `/ZZTBAR.m'`
	if _, code, err := sess.Sh(ctx, setup); err != nil || code != 0 {
		t.Fatalf("stage fixtures: code=%d err=%v", code, err)
	}
	t.Cleanup(func() { _, _, _ = sess.Sh(ctx, `rm -rf '`+dir+`'`) })

	st := NewShellStore(sess, []string{dir})

	rts, err := st.List(ctx)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var names []string
	for _, r := range rts {
		names = append(names, r.Name)
		if r.TS == "" {
			t.Errorf("%s: empty TS from real stat", r.Name)
		}
	}
	sort.Strings(names)
	if len(names) != 2 || names[0] != "ZZTBAR.m" || names[1] != "ZZTFOO.m" {
		t.Fatalf("list = %v, want [ZZTBAR.m ZZTFOO.m]", names)
	}

	body, err := st.Read(ctx, "ZZTFOO.m")
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(body) != "ZZTFOO ;it\n quit\n" {
		t.Errorf("read ZZTFOO.m = %q", body)
	}

	// A name absent in the source resolves to fs.ErrNotExist.
	if _, err := st.Read(ctx, "ZZTNOPE.m"); !os.IsNotExist(err) {
		t.Errorf("read missing: err = %v, want IsNotExist", err)
	}
	t.Logf("real ShellStore listed %d routines, read-back OK", len(names))
}
