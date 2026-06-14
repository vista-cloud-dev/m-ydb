package transport

import (
	"context"
	"reflect"
	"testing"

	mdriver "github.com/vista-cloud-dev/m-driver-sdk"
)

func TestParseZStatus(t *testing.T) {
	tests := []struct {
		name string
		zs   string
		want *mdriver.EngineError
	}{
		{
			name: "runtime UNDEF with place",
			zs:   "150373850,xeq+1^FOO,%YDB-E-UNDEF,Undefined local variable: x",
			want: &mdriver.EngineError{Routine: "FOO", Line: 1, Mnemonic: "%YDB-E-UNDEF", Text: "Undefined local variable: x"},
		},
		{
			name: "GTM mnemonic skew, label only (offset 0)",
			zs:   "150373850,lbl^XLFISO,%GTM-E-LABELMISSING,Label referenced but not defined",
			want: &mdriver.EngineError{Routine: "XLFISO", Line: 0, Mnemonic: "%GTM-E-LABELMISSING", Text: "Label referenced but not defined"},
		},
		{
			name: "text containing commas is preserved",
			zs:   "150373594,zerr+2^A,%YDB-E-ZGBLDIRACC,Cannot access global directory, file a.gld",
			want: &mdriver.EngineError{Routine: "A", Line: 2, Mnemonic: "%YDB-E-ZGBLDIRACC", Text: "Cannot access global directory, file a.gld"},
		},
		{
			name: "no place location",
			zs:   "150372970,%YDB-E-CTRLC,CTRL_C encountered",
			want: &mdriver.EngineError{Mnemonic: "%YDB-E-CTRLC", Text: "CTRL_C encountered"},
		},
		{name: "empty", zs: "", want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseZStatus(tt.zs)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("parseZStatus(%q)\n got = %+v\nwant = %+v", tt.zs, got, tt.want)
			}
		})
	}
}

func TestExecTrapped_Eval_Success(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "hello\n", Code: 0}}
	s := NewSession(Config{Transport: "local"}, rr.run)

	res, err := s.ExecTrapped(context.Background(), mdriver.ExecRequest{Command: "write \"hello\",!"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.Stdout != "hello\n" || res.Status != 0 || res.EngineError != nil {
		t.Errorf("res = %+v, want clean hello", res)
	}
	// Routed through `-run %XCMD "<trap> <work>"` (clean, non-direct context).
	if len(rr.argv) != 4 || rr.argv[1] != "-run" || rr.argv[2] != "%XCMD" {
		t.Fatalf("argv = %v, want a -run %%XCMD invocation", rr.argv)
	}
	wrapped := rr.argv[3]
	if !containsStr(wrapped, "$ETRAP") || !containsStr(wrapped, `write "hello",!`) {
		t.Errorf("%%XCMD command missing wrapper or work: %q", wrapped)
	}
}

func TestExecTrapped_Eval_EngineError(t *testing.T) {
	// The ETRAP fires: stdout carries the sentinel-delimited $ZSTATUS, exit 1.
	const sentinel = "\x01"
	out := "partial\n" + sentinel + "150373850,xeq+1^FOO,%YDB-E-UNDEF,Undefined local variable: x" + sentinel
	rr := &recordingRunner{out: CmdOutput{Stdout: out, Code: 1}}
	s := NewSession(Config{Transport: "local"}, rr.run)

	res, err := s.ExecTrapped(context.Background(), mdriver.ExecRequest{Command: "write x"})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	if res.EngineError == nil {
		t.Fatal("expected engineError, got nil")
	}
	if res.EngineError.Mnemonic != "%YDB-E-UNDEF" || res.EngineError.Routine != "FOO" {
		t.Errorf("engineError = %+v", res.EngineError)
	}
	// The sentinel block is stripped from stdout.
	if res.Stdout != "partial\n" {
		t.Errorf("stdout = %q, want %q (sentinel stripped)", res.Stdout, "partial\n")
	}
	if res.Status == 0 {
		t.Errorf("status = 0, want non-zero on fault")
	}
}

func TestExecTrapped_Run_SetsCmdline(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "", Code: 0}}
	s := NewSession(Config{Transport: "local"}, rr.run)

	_, err := s.ExecTrapped(context.Background(), mdriver.ExecRequest{EntryRef: "RUN^STDHARN", Args: []string{"zzt42", "VERBOSE"}})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	wrapped := rr.argv[len(rr.argv)-1]
	if !containsStr(wrapped, `$ZCMDLINE="zzt42 VERBOSE"`) {
		t.Errorf("command missing $ZCMDLINE set: %q", wrapped)
	}
	if !containsStr(wrapped, "DO RUN^STDHARN") {
		t.Errorf("command missing entryref DO: %q", wrapped)
	}
}

func TestExecTrapped_DockerPrependsZRoutines(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "", Code: 0}}
	s := NewSession(Config{Transport: "docker", Container: "m-test-engine", Routines: "/stage/r"}, rr.run)

	if _, err := s.ExecTrapped(context.Background(), mdriver.ExecRequest{EntryRef: "hi^ZZT"}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	// Plain `docker exec` (no -e): the container's default path keeps %XCMD
	// linked; the staged dir is layered on via $ZROUTINES inside the command.
	if len(rr.argv) < 5 || rr.argv[0] != "docker" || rr.argv[3] != "m-test-engine" {
		t.Fatalf("argv = %v, want a plain docker exec", rr.argv)
	}
	wrapped := rr.argv[len(rr.argv)-1]
	if !containsStr(wrapped, `SET $ZROUTINES="/stage/r "_$ZROUTINES`) {
		t.Errorf("command did not prepend the staged dir to $ZROUTINES: %q", wrapped)
	}
}

func TestExecTrapped_DockerSetsZGblDir(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "", Code: 0}}
	s := NewSession(Config{Transport: "docker", Container: "vehu", GblDir: "/home/vehu/g/vehu.gld", Routines: "/home/vehu/r"}, rr.run)

	if _, err := s.ExecTrapped(context.Background(), mdriver.ExecRequest{Command: "write $data(^XPD(9.7,0))"}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	// docker exec carries no -e, and the container's default env has no
	// ydb_gbldir — so the global directory must be established at runtime inside
	// the command (mirroring the $ZROUTINES layering); otherwise any global
	// access faults %YDB-E-ZGBLDIRUNDEF.
	wrapped := rr.argv[len(rr.argv)-1]
	if !containsStr(wrapped, `SET $ZGBLDIR="/home/vehu/g/vehu.gld"`) {
		t.Errorf("command did not set $ZGBLDIR for docker: %q", wrapped)
	}
}

func TestExecTrapped_LocalCarriesEnv(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "", Code: 0}}
	s := NewSession(Config{Transport: "local", Dist: "/opt/yottadb", Routines: "/src"}, rr.run)
	if _, err := s.ExecTrapped(context.Background(), mdriver.ExecRequest{Command: "write 1"}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if rr.argv[0] != "/opt/yottadb/yottadb" {
		t.Errorf("argv0 = %q, want the local dist binary", rr.argv[0])
	}
	// ydb_routines is layered via $ZROUTINES (not the env), so %XCMD stays
	// linked; dist still rides the env.
	assertEnv(t, rr.env, "ydb_dist=/opt/yottadb")
	if !containsStr(rr.argv[len(rr.argv)-1], `SET $ZROUTINES="/src "_$ZROUTINES`) {
		t.Errorf("command did not prepend /src to $ZROUTINES: %q", rr.argv[len(rr.argv)-1])
	}
}

func TestExecTrapped_PrefixMarker(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "", Code: 0}}
	s := NewSession(Config{Transport: "local"}, rr.run)
	if _, err := s.ExecTrapped(context.Background(), mdriver.ExecRequest{EntryRef: "RUN^X", Prefix: "zzt42"}); err != nil {
		t.Fatalf("exec: %v", err)
	}
	if !containsStr(rr.argv[len(rr.argv)-1], ";zzt42") {
		t.Errorf("command missing ephemeral marker: %q", rr.argv[len(rr.argv)-1])
	}
}

func TestAbort_StopsMatchingPids(t *testing.T) {
	rr := &recordingRunner{outs: []CmdOutput{
		{Stdout: "111\n222\n", Code: 0}, // pgrep
		{Stdout: "", Code: 0},           // mupip stop 111
		{Stdout: "", Code: 0},           // mupip stop 222
	}}
	s := NewSession(Config{Transport: "local", Dist: "/opt/yottadb"}, rr.run)
	killed, err := s.Abort(context.Background(), "zzt42")
	if err != nil {
		t.Fatalf("abort: %v", err)
	}
	if len(killed) != 2 || killed[0] != "111" || killed[1] != "222" {
		t.Errorf("killed = %v, want [111 222]", killed)
	}
	// First call greps for the marker; later calls mupip stop each pid.
	if !containsStr(rr.calls[0].argv[len(rr.calls[0].argv)-1], "pgrep -f ';zzt42'") {
		t.Errorf("first call not a pgrep for the marker: %v", rr.calls[0].argv)
	}
	last := rr.calls[len(rr.calls)-1].argv
	if last[len(last)-2] != "stop" || last[len(last)-1] != "222" {
		t.Errorf("last call not `mupip stop 222`: %v", last)
	}
}

func TestAbort_NoMatchIsCleanNoop(t *testing.T) {
	rr := &recordingRunner{out: CmdOutput{Stdout: "", Code: 1}} // pgrep: no match
	s := NewSession(Config{Transport: "local"}, rr.run)
	killed, err := s.Abort(context.Background(), "zzt99")
	if err != nil {
		t.Fatalf("abort: %v", err)
	}
	if len(killed) != 0 {
		t.Errorf("killed = %v, want empty", killed)
	}
}

func TestAbort_RejectsBadPrefix(t *testing.T) {
	s := NewSession(Config{Transport: "local"}, (&recordingRunner{}).run)
	if _, err := s.Abort(context.Background(), "evil; rm -rf /"); err == nil {
		t.Error("expected an error for an unsafe prefix")
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return len(sub) == 0
}
