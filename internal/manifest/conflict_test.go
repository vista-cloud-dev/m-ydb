package manifest

import "testing"

func TestCheckConflict(t *testing.T) {
	m := New()
	m.Routines["FOO.m"] = Entry{SourceTS: "100", SHA256: "abc", Bytes: 10}

	tests := []struct {
		name     string
		routine  string
		instTS   string
		exists   bool
		wantKind ConflictKind
	}{
		{"unchanged: same ts → none", "FOO.m", "100", true, ConflictNone},
		{"instance edited: different ts → changed", "FOO.m", "200", true, ConflictChanged},
		{"instance deleted under us → deleted", "FOO.m", "", false, ConflictDeleted},
		{"new local, instance absent → none (clean create)", "NEW.m", "", false, ConflictNone},
		{"new local, instance already has it → exists", "NEW.m", "50", true, ConflictExists},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := CheckConflict(m, tt.routine, tt.instTS, tt.exists)
			if c.Kind != tt.wantKind {
				t.Errorf("kind = %v, want %v", c.Kind, tt.wantKind)
			}
			if tt.wantKind != ConflictNone && c.Message == "" {
				t.Error("expected a non-empty conflict message")
			}
		})
	}
}
