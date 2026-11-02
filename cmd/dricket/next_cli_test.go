// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"strings"
	"testing"
)

func TestCLINextSearchValidate(t *testing.T) {
	setupBareCLI(t)

	runForTest(t, "add", "--prefix", "app", "-c", "Login error on empty password", "--unclaimed")
	runForTest(t, "add", "--prefix", "app", "-c", "Rotate the login certificates", "--unclaimed")

	code, out, errMsg := runForTest(t, "next")
	if code != 0 || !strings.Contains(out, "app-1") || !strings.Contains(out, "open") {
		t.Fatalf("next = %d, %q, %q", code, out, errMsg)
	}

	code, out, errMsg = runForTest(t, "next", "--claim", "--size", "S")
	if code != 0 || !strings.Contains(out, "claimed app-1 (epoch 1") {
		t.Fatalf("next --claim = %d, %q, %q", code, out, errMsg)
	}

	// The claimed ticket is no longer next.
	code, out, _ = runForTest(t, "next")
	if code != 0 || !strings.Contains(out, "app-2") {
		t.Fatalf("next after claim = %d, %q", code, out)
	}

	code, out, _ = runForTest(t, "search", "login")
	if code != 0 || !strings.Contains(out, "app-1") || !strings.Contains(out, "app-2") {
		t.Fatalf("search = %d, %q", code, out)
	}

	code, out, _ = runForTest(t, "search", "nothing-matches-this")
	if code != exitError || !strings.Contains(out, "") {
		t.Fatalf("search miss = %d, %q; want exit 1", code, out)
	}

	code, _, _ = runForTest(t, "validate")
	if code != 0 {
		t.Fatalf("validate clean = %d; want 0", code)
	}

	// A dangling dependency is exactly what validate catches and lint
	// cannot see.
	runForTest(t, "set", "app-2", "depends=app-99")
	code, _, errMsg = runForTest(t, "validate")
	if code != exitInvalid || !strings.Contains(errMsg, "validate found 1 error") {
		t.Fatalf("validate dangling = %d, %q; want 14", code, errMsg)
	}
}
