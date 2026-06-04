package udiff

import (
	"strings"
	"testing"
)

func TestUnified_Change(t *testing.T) {
	a := []string{"line1", "line2", "line3"}
	b := []string{"line1", "EDITED", "line3"}
	got := Unified("instance/FOO.m", "mirror/FOO.m", a, b)

	for _, want := range []string{
		"--- instance/FOO.m",
		"+++ mirror/FOO.m",
		"@@ -1,3 +1,3 @@",
		" line1",
		"-line2",
		"+EDITED",
		" line3",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestUnified_Identical(t *testing.T) {
	a := []string{"x", "y"}
	if got := Unified("a", "b", a, a); got != "" {
		t.Errorf("identical inputs should diff empty, got:\n%s", got)
	}
}

func TestUnified_PureAddition(t *testing.T) {
	got := Unified("a", "b", []string{"one"}, []string{"one", "two"})
	if !strings.Contains(got, "+two") {
		t.Errorf("expected +two, got:\n%s", got)
	}
}

func TestSplitLines(t *testing.T) {
	if got := SplitLines("a\nb\n"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("SplitLines = %v, want [a b]", got)
	}
	if got := SplitLines("a\nb"); len(got) != 2 { // no trailing newline
		t.Errorf("SplitLines no-trailing = %v, want 2 lines", got)
	}
	if got := SplitLines(""); len(got) != 0 {
		t.Errorf("SplitLines empty = %v, want 0", got)
	}
}
