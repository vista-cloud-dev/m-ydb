package transport

import (
	"context"
	"os"
	"reflect"
	"testing"
)

func TestProvision_Local_GdeThenMupipCreate(t *testing.T) {
	rr := &recordingRunner{}
	s := NewSession(Config{Transport: "local", Dist: "/opt/yottadb", GblDir: "/data/m.gld"}, rr.run)

	res, err := s.Provision(context.Background(), ProvisionOpts{})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if res.State != "provisioned" {
		t.Errorf("state = %q, want provisioned", res.State)
	}
	if len(rr.calls) != 2 {
		t.Fatalf("want 2 calls (gde, mupip create), got %d: %+v", len(rr.calls), rr.calls)
	}
	// 1) GDE lays out DEFAULT segment → the .dat beside the .gld, via stdin.
	if rr.calls[0].argv[0] != "/opt/yottadb/gde" {
		t.Errorf("call0 = %v, want gde", rr.calls[0].argv)
	}
	if rr.calls[0].in != "change -segment DEFAULT -file=/data/m.dat\nexit\n" {
		t.Errorf("gde script = %q", rr.calls[0].in)
	}
	// 2) mupip create reads the gld and creates the .dat.
	if want := []string{"/opt/yottadb/mupip", "create"}; !reflect.DeepEqual(rr.calls[1].argv, want) {
		t.Errorf("call1 = %v, want %v", rr.calls[1].argv, want)
	}
}

func TestProvision_Docker_RunsContainer(t *testing.T) {
	rr := &recordingRunner{}
	s := NewSession(Config{Transport: "docker", Container: "m-test-engine"}, rr.run)

	res, err := s.Provision(context.Background(), ProvisionOpts{Image: "yottadb/yottadb-base"})
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if res.State != "provisioned" {
		t.Errorf("state = %q", res.State)
	}
	want := []string{"docker", "run", "-d", "--name", "m-test-engine", "yottadb/yottadb-base"}
	if !reflect.DeepEqual(rr.calls[0].argv, want) {
		t.Errorf("argv = %v, want %v", rr.calls[0].argv, want)
	}
}

func TestDown_Local_Rundown(t *testing.T) {
	rr := &recordingRunner{}
	s := NewSession(Config{Transport: "local", Dist: "/opt/yottadb", GblDir: "/data/m.gld"}, rr.run)

	res, err := s.Down(context.Background())
	if err != nil {
		t.Fatalf("down: %v", err)
	}
	if res.State != "stopped" {
		t.Errorf("state = %q, want stopped", res.State)
	}
	want := []string{"/opt/yottadb/mupip", "rundown", "-region", "*"}
	if !reflect.DeepEqual(rr.calls[0].argv, want) {
		t.Errorf("argv = %v, want %v", rr.calls[0].argv, want)
	}
}

func TestDown_Docker_StopsContainer(t *testing.T) {
	rr := &recordingRunner{}
	s := NewSession(Config{Transport: "docker", Container: "m-test-engine"}, rr.run)

	if _, err := s.Down(context.Background()); err != nil {
		t.Fatalf("down: %v", err)
	}
	want := []string{"docker", "stop", "m-test-engine"}
	if !reflect.DeepEqual(rr.calls[0].argv, want) {
		t.Errorf("argv = %v, want %v", rr.calls[0].argv, want)
	}
}

func TestUp_Docker_StartsContainer(t *testing.T) {
	rr := &recordingRunner{}
	s := NewSession(Config{Transport: "docker", Container: "m-test-engine"}, rr.run)

	res, err := s.Up(context.Background())
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if res.State != "started" {
		t.Errorf("state = %q, want started", res.State)
	}
	want := []string{"docker", "start", "m-test-engine"}
	if !reflect.DeepEqual(rr.calls[0].argv, want) {
		t.Errorf("argv = %v, want %v", rr.calls[0].argv, want)
	}
}

func TestDestroy_Docker_RemovesContainer(t *testing.T) {
	rr := &recordingRunner{}
	s := NewSession(Config{Transport: "docker", Container: "m-test-engine"}, rr.run)

	res, err := s.Destroy(context.Background())
	if err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if res.State != "removed" {
		t.Errorf("state = %q, want removed", res.State)
	}
	want := []string{"docker", "rm", "-f", "m-test-engine"}
	if !reflect.DeepEqual(rr.calls[0].argv, want) {
		t.Errorf("argv = %v, want %v", rr.calls[0].argv, want)
	}
}

func TestUp_Local_ExistingDB_ClearsStaleShmem(t *testing.T) {
	dir := t.TempDir()
	gld := dir + "/m.gld"
	mustWrite(t, gld)
	mustWrite(t, dir+"/m.dat")
	rr := &recordingRunner{}
	s := NewSession(Config{Transport: "local", Dist: "/opt/yottadb", GblDir: gld}, rr.run)

	res, err := s.Up(context.Background())
	if err != nil {
		t.Fatalf("up: %v", err)
	}
	if res.State != "ready" {
		t.Errorf("state = %q, want ready", res.State)
	}
	// DB already present → only the stale-shmem rundown runs (no provision).
	if len(rr.calls) != 1 {
		t.Fatalf("want 1 call (rundown), got %d: %+v", len(rr.calls), rr.calls)
	}
	if want := []string{"/opt/yottadb/mupip", "rundown", "-region", "*"}; !reflect.DeepEqual(rr.calls[0].argv, want) {
		t.Errorf("argv = %v, want %v", rr.calls[0].argv, want)
	}
}

func TestUp_Local_MissingDB_ProvisionsFirst(t *testing.T) {
	dir := t.TempDir()
	rr := &recordingRunner{}
	s := NewSession(Config{Transport: "local", Dist: "/opt/yottadb", GblDir: dir + "/m.gld"}, rr.run)

	if _, err := s.Up(context.Background()); err != nil {
		t.Fatalf("up: %v", err)
	}
	// Missing DB → provision (gde, mupip create) then rundown = 3 calls.
	if len(rr.calls) != 3 {
		t.Fatalf("want 3 calls (gde, mupip create, rundown), got %d: %+v", len(rr.calls), rr.calls)
	}
}

func TestDestroy_Local_RemovesFiles(t *testing.T) {
	dir := t.TempDir()
	gld := dir + "/m.gld"
	mustWrite(t, gld)
	mustWrite(t, dir+"/m.dat")
	rr := &recordingRunner{}
	s := NewSession(Config{Transport: "local", Dist: "/opt/yottadb", GblDir: gld}, rr.run)

	res, err := s.Destroy(context.Background())
	if err != nil {
		t.Fatalf("destroy: %v", err)
	}
	if res.State != "destroyed" {
		t.Errorf("state = %q, want destroyed", res.State)
	}
	if filesExist(gld) || filesExist(dir+"/m.dat") {
		t.Error("destroy should remove the .gld and .dat")
	}
}

func mustWrite(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDatPath(t *testing.T) {
	if got := datPath("/data/m.gld"); got != "/data/m.dat" {
		t.Errorf("datPath = %q, want /data/m.dat", got)
	}
	if got := datPath("/data/store"); got != "/data/store.dat" {
		t.Errorf("datPath = %q, want /data/store.dat", got)
	}
}
