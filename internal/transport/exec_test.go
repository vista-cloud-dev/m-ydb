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

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return len(sub) == 0
}
