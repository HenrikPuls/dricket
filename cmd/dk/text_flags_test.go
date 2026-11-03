// SPDX-License-Identifier: GPL-3.0-or-later
package main

import "testing"

func TestTextFlagsUseMessage(t *testing.T) {
	setupBareCLI(t)
	if code, out, errMsg := runForTest(t, "add", "--prefix", "app", "-m", "title", "--unclaimed"); code != 0 {
		t.Fatalf("add -m = %d, %q, %q", code, out, errMsg)
	}
	if code, _, _ := runForTest(t, "claim", "app-1"); code != 0 {
		t.Fatalf("claim = %d", code)
	}
	cases := []struct {
		name string
		args []string
	}{
		{"note after id", []string{"note", "app-1", "-m", "progress"}},
		{"comment before id", []string{"comment", "--message", "comment", "app-1"}},
		{"extend after id", []string{"extend", "app-1", "--message", "more time"}},
		{"release after id", []string{"release", "app-1", "-m", "done"}},
	}
	for _, tc := range cases {
		if code, out, errMsg := runForTest(t, tc.args...); code != 0 {
			t.Errorf("%s = %d, %q, %q", tc.name, code, out, errMsg)
		}
	}
}

func TestRemovedCLIFormsFail(t *testing.T) {
	setupBareCLI(t)
	cases := [][]string{
		{"add", "--prefix", "app", "-c", "old"},
		{"update", "app-1", "--note", "old"},
		{"note", "app-1", "positional"},
		{"note", "app-1", "--note", "old"},
		{"comment", "app-1", "positional"},
		{"extend", "app-1", "--reason", "old"},
		{"release", "app-1", "--result", "old"},
		{"force-release", "app-1", "--reason", "old"},
		{"-q", "ls"},
		{"--no-color", "ls"},
		{"-y", "ls"},
		{"-n", "ls"},
	}
	for _, args := range cases {
		if code, _, _ := runForTest(t, args...); code != exitUsage {
			t.Errorf("%v = exit %d; want 2", args, code)
		}
	}
}
