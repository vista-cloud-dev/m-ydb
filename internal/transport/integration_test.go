package transport

import (
	"context"
	"os"
	"testing"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

// TestDockerEngine_RealHealthAndVersion exercises the docker transport against a
// real YottaDB container (the gated integration tier — plan §1). It is READ-ONLY
// (Health + Version + a trivial Exec); it never stops/destroys the shared
// container. Run with:
//
//	M_YDB_IT=1 M_YDB_CONTAINER=m-test-engine go test ./internal/transport/ -run RealHealth
func TestDockerEngine_RealHealthAndVersion(t *testing.T) {
	if os.Getenv("M_YDB_IT") != "1" {
		t.Skip("gated: set M_YDB_IT=1 (+ a running YottaDB container) to run the real-engine tier")
	}
	container := os.Getenv("M_YDB_CONTAINER")
	if container == "" {
		container = "m-test-engine"
	}
	s := NewSession(Config{Transport: "docker", Container: container}, nil)
	ctx := context.Background()

	h, err := s.Health(ctx)
	if err != nil {
		t.Fatalf("health: %v", err)
	}
	if !h.Running || !h.Healthy {
		t.Fatalf("engine not healthy: %+v", h)
	}

	v, err := s.Version(ctx)
	if err != nil {
		t.Fatalf("version: %v", err)
	}
	if v == "" {
		t.Error("version probe returned empty")
	}
	t.Logf("real YottaDB healthy, version %s", v)

	res, err := s.Exec(ctx, mdriver.ExecRequest{Command: "write 1"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.Stdout == "" {
		t.Error("exec produced no output")
	}
}
