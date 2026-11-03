// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// cliPushMain writes a config file onto main of the CLI test store, like
// an admin would.
func cliPushMain(t *testing.T, name, content string) {
	t.Helper()
	bare := os.Getenv("DK_STORE")
	// DK_STORE is remote#branch; the clone wants the remote only.
	if i := strings.IndexByte(bare, '#'); i >= 0 {
		bare = bare[:i]
	}
	base := t.TempDir()
	clone := filepath.Join(base, "clone")
	runGitCmd(t, base, "clone", "--quiet", bare, clone)
	runGitCmd(t, clone, "checkout", "--orphan", "main")
	runGitCmd(t, clone, "rm", "-rf", "--ignore-unmatch", "-q", ".")
	if err := os.WriteFile(filepath.Join(clone, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	runGitCmd(t, clone, "add", name)
	runGitCmd(t, clone, "-c", "user.name=admin", "-c", "user.email=admin@invalid",
		"commit", "--quiet", "-m", "config")
	runGitCmd(t, clone, "push", "--quiet", "origin", "main:refs/heads/main")
}

func runGitCmd(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) // #nosec G702,G204 -- test scaffolding with fixed git invocations
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
}

func TestCLIForceReleaseAndGuide(t *testing.T) {
	setupBareCLI(t)
	cliPushMain(t, "dricket.toml", "[security]\nadmins = [\"cli-tester\"]\n")

	runForTest(t, "add", "--prefix", "app", "-c", "claimed work")
	code, _, errMsg := runForTest(t, "extend", "--reason", "still working", "app-1")
	if code != 0 {
		t.Fatalf("extend = %d, %q", code, errMsg)
	}

	// The same owner is the admin here: force-release works.
	code, out, errMsg := runForTest(t, "force-release", "--reason", "runner died", "app-1")
	if code != 0 || !strings.Contains(out, "force-released app-1 -> open") {
		t.Fatalf("force-release = %d, %q, %q", code, out, errMsg)
	}

	// Idempotent on an already free ticket.
	code, out, _ = runForTest(t, "force-release", "--reason", "again", "app-1")
	if code != 0 || !strings.Contains(out, "already free") {
		t.Fatalf("idempotent force-release = %d, %q", code, out)
	}

	// Missing reason is a usage error.
	code, _, _ = runForTest(t, "force-release", "app-1")
	if code != exitUsage {
		t.Fatalf("force-release without reason = %d; want 2", code)
	}

	// The guide is data on stdout.
	code, out, _ = runForTest(t, "guide")
	if code != 0 || !strings.Contains(out, "dricket agent guide") || !strings.Contains(out, "Exit 13") {
		t.Fatalf("guide = %d, %q", code, out)
	}
}

func TestCLIQuotaAndSecret(t *testing.T) {
	bare := setupBareCLI(t)
	cliPushMain(t, "dricket.toml", "[security]\nquota_tickets_per_day = 2\nsecret_scan = true\n")
	_ = bare

	runForTest(t, "add", "--prefix", "app", "-c", "one", "--unclaimed")
	runForTest(t, "add", "--prefix", "app", "-c", "two", "--unclaimed")

	code, _, errMsg := runForTest(t, "add", "--prefix", "app", "-c", "three", "--unclaimed")
	if code != exitQuota || !strings.Contains(errMsg, "quota reached") {
		t.Fatalf("add beyond quota = %d, %q; want 18", code, errMsg)
	}

	code, _, errMsg = runForTest(t, "add", "--prefix", "app", "-c", "use AKIAIOSFODNN7EXAMPLE here")
	if code != exitSecret || !strings.Contains(errMsg, "secret scan") {
		t.Fatalf("add with secret = %d, %q; want 19", code, errMsg)
	}

	// Nothing was created by the refused adds.
	code, out, _ := runForTest(t, "ls")
	if code != 0 || strings.Contains(out, "app-3") {
		t.Fatalf("ls after refused adds = %d, %q", code, out)
	}
}

func TestCLIForceReleaseDeniedWithoutAdmins(t *testing.T) {
	setupBareCLI(t) // no dricket.toml: nobody is an admin
	runForTest(t, "add", "--prefix", "app", "-c", "x")
	code, _, errMsg := runForTest(t, "force-release", "--reason", "no admins", "app-1")
	if code != exitForbidden || !strings.Contains(errMsg, "security.admins") {
		t.Fatalf("force-release without admins = %d, %q; want 17", code, errMsg)
	}
}
