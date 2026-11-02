// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"strings"
	"testing"
)

func TestCLIPriority(t *testing.T) {
	setupBareCLI(t)

	// Default priority.
	runForTest(t, "add", "--prefix", "app", "-c", "plain task", "--unclaimed")
	// Explicit priority.
	code, _, errMsg := runForTest(t, "add", "--prefix", "app", "-c", "hot task", "--unclaimed", "--priority", "critical")
	if code != 0 {
		t.Fatalf("add critical = %d, %q", code, errMsg)
	}
	// Unknown value.
	code, _, errMsg = runForTest(t, "add", "--prefix", "app", "-c", "x", "--unclaimed", "--priority", "bogus")
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

	// next reports the priority of the chosen ticket (ordering stays
	// lowest number first; priority-based ordering is a separate
	// decision).
	code, out, _ = runForTest(t, "next")
	if code != 0 || !strings.Contains(out, "low") {
		t.Fatalf("next = %d, %q; want the priority column", code, out)
	}
}
