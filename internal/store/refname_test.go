// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"os/exec"
	"testing"
)

// TestCheckBranchNameAgainstGit proves the in-process validator never
// accepts a name git rejects (strictness is allowed, laxness is not) and
// agrees on every ordinary name.
func TestCheckBranchNameAgainstGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	names := []string{
		"tickets", "work", "a", "a/b", "feature/x-1", "v1.2", "ünï", "tickets-2026",
		"", "HEAD", "@", "-x", "--x", "/x", "x/", "x.", ".x", "x/.y", "x.lock", "x.lock/y", "x/y.lock",
		"a..b", "a b", "a~b", "a^b", "a:b", "a?b", "a*b", "a[b", "a\\b", "a@{b", "a//b", "a\tb", "a\x7fb",
		"x@y", "a.b", "refs/heads/x", "...",
	}
	for _, n := range names {
		ours := checkBranchName(n) == nil
		gitOK := exec.Command("git", "check-ref-format", "--branch", n).Run() == nil // #nosec G204 -- fixed git call, test table input
		if ours && !gitOK {
			t.Errorf("%q: validator accepts a name git rejects", n)
		}
		if !ours && gitOK && n != "@" { // "@" alone is refused on purpose (rule 9 of git-check-ref-format)
			t.Errorf("%q: validator rejects a name git accepts", n)
		}
	}
}
