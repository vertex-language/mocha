package testutils

import (
	"fmt"
	"strings"
	"testing"
)

// maxDiffCells bounds the LCS table. Method bodies are small; a pathological
// input falls back to reporting the first divergence rather than allocating
// gigabytes to describe a failure nobody will read anyway.
const maxDiffCells = 4 << 20

// Diff returns a unified-style description of the difference between two line
// slices, or "" when they are equal.
func Diff(want, got []string) string {
	if equal(want, got) {
		return ""
	}
	if len(want)*len(got) > maxDiffCells {
		return firstDivergence(want, got)
	}

	// Standard LCS table, walked backwards to emit the edit script.
	n, m := len(want), len(got)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if want[i] == got[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}

	var sb strings.Builder
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case want[i] == got[j]:
			fmt.Fprintf(&sb, "  %s\n", want[i])
			i, j = i+1, j+1
		case lcs[i+1][j] >= lcs[i][j+1]:
			fmt.Fprintf(&sb, "- %s\n", want[i])
			i++
		default:
			fmt.Fprintf(&sb, "+ %s\n", got[j])
			j++
		}
	}
	for ; i < n; i++ {
		fmt.Fprintf(&sb, "- %s\n", want[i])
	}
	for ; j < m; j++ {
		fmt.Fprintf(&sb, "+ %s\n", got[j])
	}
	return sb.String()
}

func firstDivergence(want, got []string) string {
	for i := 0; i < len(want) && i < len(got); i++ {
		if want[i] != got[i] {
			return fmt.Sprintf("first difference at line %d:\n- %s\n+ %s\n", i+1, want[i], got[i])
		}
	}
	return fmt.Sprintf("common prefix, but lengths differ: want %d lines, got %d\n", len(want), len(got))
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// DiffDisassembly compares two javap -c outputs member by member and fails
// with a readable report. "want" is javac's, "got" is mocha's.
func DiffDisassembly(t testing.TB, want, got string) {
	t.Helper()

	wantM, gotM := Members(want), Members(got)

	for _, name := range MemberNames(want) {
		body, ok := gotM[name]
		if !ok {
			t.Errorf("missing member %s\njavac emitted it; mocha did not.\nmocha has: %v",
				name, MemberNames(got))
			continue
		}
		if d := Diff(wantM[name], body); d != "" {
			t.Errorf("disassembly differs for %s\n(- javac, + mocha)\n%s", name, d)
		}
	}
	for _, name := range MemberNames(got) {
		if _, ok := wantM[name]; !ok {
			t.Errorf("unexpected member %s: mocha emitted it, javac did not", name)
		}
	}
}