// Package udiff produces a unified diff of two line slices — enough for the
// `sync diff` verb (driver-contract §5.2: `{ unified }`). Routines are small,
// so an LCS DP over lines is fine; the output is a single hunk with up to three
// lines of surrounding context, which is valid unified-diff format.
package udiff

import (
	"fmt"
	"strings"
)

const context = 3

// SplitLines splits s into lines, dropping a single trailing newline so a
// normalized file ("a\nb\n") yields exactly its lines (["a","b"]).
func SplitLines(s string) []string {
	if s == "" {
		return nil
	}
	s = strings.TrimSuffix(s, "\n")
	return strings.Split(s, "\n")
}

type op struct {
	tag  byte // ' ' equal, '-' removed (in a), '+' added (in b)
	text string
}

// Unified returns the unified diff transforming a → b, or "" if they are equal.
func Unified(aName, bName string, a, b []string) string {
	ops := diffOps(a, b)

	firstChange, lastChange := -1, -1
	for k, o := range ops {
		if o.tag != ' ' {
			if firstChange < 0 {
				firstChange = k
			}
			lastChange = k
		}
	}
	if firstChange < 0 {
		return "" // identical
	}

	// Per-op source line numbers (1-based).
	aLineOf := make([]int, len(ops))
	bLineOf := make([]int, len(ops))
	a1, b1 := 1, 1
	for k, o := range ops {
		switch o.tag {
		case ' ':
			aLineOf[k], bLineOf[k] = a1, b1
			a1++
			b1++
		case '-':
			aLineOf[k] = a1
			a1++
		case '+':
			bLineOf[k] = b1
			b1++
		}
	}

	lo := firstChange - context
	if lo < 0 {
		lo = 0
	}
	hi := lastChange + context
	if hi > len(ops)-1 {
		hi = len(ops) - 1
	}

	aStart, aCount, bStart, bCount := 0, 0, 0, 0
	for k := lo; k <= hi; k++ {
		switch ops[k].tag {
		case ' ':
			if aCount == 0 {
				aStart = aLineOf[k]
			}
			if bCount == 0 {
				bStart = bLineOf[k]
			}
			aCount++
			bCount++
		case '-':
			if aCount == 0 {
				aStart = aLineOf[k]
			}
			aCount++
		case '+':
			if bCount == 0 {
				bStart = bLineOf[k]
			}
			bCount++
		}
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "--- %s\n", aName)
	fmt.Fprintf(&sb, "+++ %s\n", bName)
	fmt.Fprintf(&sb, "@@ -%s +%s @@\n", rangeSpec(aStart, aCount), rangeSpec(bStart, bCount))
	for k := lo; k <= hi; k++ {
		sb.WriteByte(ops[k].tag)
		sb.WriteString(ops[k].text)
		sb.WriteByte('\n')
	}
	return sb.String()
}

// rangeSpec renders the "start,count" half of a hunk header (count omitted when
// it is 1, per unified-diff convention; start 0 for an empty side).
func rangeSpec(start, count int) string {
	if count == 1 {
		return fmt.Sprint(start)
	}
	return fmt.Sprintf("%d,%d", start, count)
}

// diffOps computes an edit script (LCS-based) transforming a → b.
func diffOps(a, b []string) []op {
	n, m := len(a), len(b)
	dp := make([][]int, n+1)
	for i := range dp {
		dp[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var ops []op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{' ', a[i]})
			i++
			j++
		case dp[i+1][j] >= dp[i][j+1]:
			ops = append(ops, op{'-', a[i]})
			i++
		default:
			ops = append(ops, op{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, op{'+', b[j]})
	}
	return ops
}
