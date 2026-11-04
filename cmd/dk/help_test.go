// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestHelpGroupsEveryCommand(t *testing.T) {
	code, out, errMsg := runForTest(t, "--help")
	if code != 0 {
		t.Fatalf("help = %d, %q", code, errMsg)
	}
	if !strings.Contains(out, "Usage: dk") || strings.Contains(out, "roadmap") {
		t.Errorf("root help = %q", out)
	}
	for _, group := range []string{"Setup", "Work", "Inspect", "Check"} {
		if !strings.Contains(out, group+":") {
			t.Errorf("root help misses group %q", group)
		}
	}
	for _, cmd := range registry {
		if strings.Count(out, "\n  "+cmd.name+" ") != 1 {
			t.Errorf("command %q is not listed exactly once", cmd.name)
		}
	}
}

func TestHelpForEveryCommandWithoutStore(t *testing.T) {
	t.Setenv("DK_STORE", filepath.Join(t.TempDir(), "missing"))
	flags := map[string]string{
		"add": "--message", "claim": "--size", "release": "--message",
		"comment": "--message", "extend": "--message", "note": "--message",
		"put": "--if-version", "ls": "-s <value>", "next": "--claim",
		"leases": "--check", "init": "--branch", "force-release": "--message",
		"validate": "--trailers",
	}
	for _, cmd := range registry {
		for _, args := range [][]string{{cmd.name, "--help"}, {"help", cmd.name}} {
			code, out, errMsg := runForTest(t, args...)
			if code != 0 {
				t.Errorf("%v = %d, %q", args, code, errMsg)
				continue
			}
			if !strings.Contains(out, "Usage: dk ") || !strings.Contains(out, cmd.synopsis) {
				t.Errorf("%v help = %q", args, out)
			}
			if flag := flags[cmd.name]; flag != "" && !strings.Contains(out, flag) {
				t.Errorf("%v help misses %q: %q", args, flag, out)
			}
		}
	}
}

func TestHelpUnknownTopic(t *testing.T) {
	if code, _, _ := runForTest(t, "help", "unknown"); code != exitUsage {
		t.Fatalf("help unknown = %d; want 2", code)
	}
}
