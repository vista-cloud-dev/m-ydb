package transport

import (
	"context"
	"strings"
	"testing"
)

// ContainerRoutines reads the engine's own $ZROUTINES under docker — the source
// axis's fallback when the host config does not pin --routines (a real VistA
// image like vehu keeps its routine source path in the container, not the host
// env). It must route through the engine and return the trimmed value verbatim
// (its `object*(src …)` form is what ParseRoutinesDirs consumes).
func TestContainerRoutines_DockerReadsZRoutines(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "/h/p/r2*(/h/p) /h/r\n", Code: 0}}
	s := NewSession(Config{Transport: "docker", Container: "vehu"}, rr.run)

	got, err := s.ContainerRoutines(context.Background())
	if err != nil {
		t.Fatalf("ContainerRoutines: %v", err)
	}
	if got != "/h/p/r2*(/h/p) /h/r" {
		t.Errorf("got %q, want the trimmed $ZROUTINES value", got)
	}
	cmd := strings.Join(rr.argv, " ")
	if rr.argv[0] != "docker" || !strings.Contains(cmd, "$ZROUTINES") {
		t.Errorf("argv = %v, want a docker-exec'd $ZROUTINES read", rr.argv)
	}
}

// Off the docker transport there is no container to ask — the source path comes
// from the host env/flags — so it is a no-op that runs nothing.
func TestContainerRoutines_NonDockerIsNoop(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "should-not-be-read", Code: 0}}
	s := NewSession(Config{Transport: "local"}, rr.run)

	got, err := s.ContainerRoutines(context.Background())
	if err != nil {
		t.Fatalf("ContainerRoutines: %v", err)
	}
	if got != "" {
		t.Errorf("got %q, want empty (no engine call off docker)", got)
	}
	if rr.argv != nil {
		t.Errorf("ran a command off the docker transport: %v", rr.argv)
	}
}
