// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"strings"
	"testing"
)

func TestCLIPriority(t *testing.T) {
	setupBareCLI(t)

	// Default priority.
	runForTest(t, "add", "--prefix", "app", "-m", "plain task", "--unclaimed")
	// Explicit priority.
	code, _, errMsg := runForTest(t, "add", "--prefix", "app", "-m", "hot task", "--unclaimed", "--priority", "critical")
	if code != 0 {
		t.Fatalf("add critical = %d, %q", code, errMsg)
	}
	// Unknown value.
	code, _, errMsg = runForTest(t, "add", "--prefix", "app", "-m", "x", "--unclaimed", "--priority", "bogus")
	if code != exitInvalid || !strings.Contains(errMsg, "priority") {
		t.Fatalf("add bogus priority = %d, %q; want 14", code, errMsg)
	}

	code, out, _ := runForTest(t, "show", "app-2")
	if code != 0 || !strings.Contains(out, "priority: critical") {
		t.Fatalf("show = %d, %q", code, out)
	}

	code, out, _ = runForTest(t, "ls")
	if code != 0 || !strings.Contains(out, "critical") || !strings.Contains(out, "normal") {
		t.Fatalf("ls = %d, %q", code, out)
	}

	// set changes the priority.
	code, _, _ = runForTest(t, "set", "app-1", "priority=low")
	if code != 0 {
		t.Fatalf("set priority = %d", code)
	}
	code, out, _ = runForTest(t, "show", "app-1")
	if code != 0 || !strings.Contains(out, "priority: low") {
		t.Fatalf("show after set = %d, %q", code, out)
	}

	// next picks the critical ticket first now (priority order from
	// [priorities].values, then the lowest number).
	code, out, _ = runForTest(t, "next")
	if code != 0 || !strings.Contains(out, "critical") || !strings.Contains(out, "app-2") {
		t.Fatalf("next = %d, %q; want the critical app-2 first", code, out)
	}
}
