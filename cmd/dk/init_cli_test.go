// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCLIInitInDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tickets")

	code, out, errMsg := runForTest(t, "init", dir, "app")
	if code != 0 || !strings.Contains(out, "initialized ticket store in") {
		t.Fatalf("init = %d, %q, %q", code, out, errMsg)
	}
	if out := cliGit(t, dir, "symbolic-ref", "--quiet", "--short", "HEAD"); strings.TrimSpace(out) != "tickets" {
		t.Fatalf("checked-out branch = %q; want tickets", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "dricket.toml")); !os.IsNotExist(err) {
		t.Fatalf("dricket.toml must stay on main; stat err = %v", err)
	}

	// The fresh store works directly from its ticket checkout and discovery.
	t.Chdir(dir)
	t.Setenv("DK_STORE", "")
	t.Setenv("DK_DATA_DIR", t.TempDir())
	t.Setenv("DK_OWNER", "first-user")
	code, out, _ = runForTest(t, "add", "-c", "first ticket", "--unclaimed")
	if code != 0 || !strings.Contains(out, "app-1") {
		t.Fatalf("add in fresh store = %d, %q", code, out)
	}
	ticketPath := filepath.Join(dir, "tickets", "app", "0000", "app-1", "ticket.md")
	if content, err := os.ReadFile(ticketPath); err != nil || !strings.Contains(string(content), "title: first ticket") { // #nosec G304 -- test-controlled temporary path
		t.Fatalf("visible ticket = %q, %v", content, err)
	}
	if code, out, _ := runForTest(t, "use"); code != 0 || !strings.Contains(out, "prefix: app (prefixes.toml (single registered prefix))") {
		t.Fatalf("use after init = %d, %q; want registered prefix app", code, out)
	}

	// A second init into the same directory is refused.
	code, _, errMsg = runForTest(t, "init", dir)
	if code != exitError || !strings.Contains(errMsg, "already a git repository") {
		t.Fatalf("second init = %d, %q; want refusal", code, errMsg)
	}
}

func TestCLIInitInWorkingDirectory(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	code, out, _ := runForTest(t, "init")
	if code != 0 || !strings.Contains(out, "initialized ticket store in") {
		t.Fatalf("init in cwd = %d, %q", code, out)
	}
	for _, f := range []string{"dricket.toml", "prefixes.toml", "README.md", "AGENTS.md", "CLAUDE.md"} {
		if _, err := os.Stat(filepath.Join(dir, f)); !os.IsNotExist(err) {
			t.Errorf("scaffold %s must remain on main; stat err = %v", f, err)
		}
	}
}

func TestCLIInitCustomBranchChecksOutTicketBranch(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "store")
	if code, _, errMsg := runForTest(t, "init", "--branch", "work", dir, "app"); code != 0 {
		t.Fatalf("init = %d, %q", code, errMsg)
	}
	out := cliGit(t, dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if strings.TrimSpace(out) != "work" {
		t.Fatalf("checked-out branch = %q; want work", out)
	}
	if out := cliGit(t, dir, "show", "main:dricket.toml"); !strings.Contains(out, `branch = "work"`) {
		t.Fatalf("main config = %q", out)
	}
	t.Chdir(dir)
	t.Setenv("DK_STORE", "")
	t.Setenv("DK_DATA_DIR", t.TempDir())
	t.Setenv("DK_OWNER", "custom-branch-user")
	if code, out, _ := runForTest(t, "add", "-c", "custom branch", "--unclaimed"); code != 0 || !strings.Contains(out, "app-1") {
		t.Fatalf("add on custom branch = %d, %q", code, out)
	}
}

func TestCLISetupAgent(t *testing.T) {
	setupBareCLI(t)
	dir := t.TempDir()
	t.Chdir(dir)

	code, out, _ := runForTest(t, "setup-agent")
	if code != 0 || !strings.Contains(out, "wrote the store pointer") {
		t.Fatalf("setup-agent = %d, %q", code, out)
	}
	content, err := os.ReadFile(filepath.Join(dir, "AGENTS.md")) // #nosec G304 -- test scaffolding in t.TempDir
	if err != nil || !strings.Contains(string(content), "dk guide") || !strings.Contains(string(content), "branch tickets") {
		t.Fatalf("AGENTS.md = %q, %v", content, err)
	}

	// Idempotent: a second run changes nothing.
	code, _, _ = runForTest(t, "setup-agent")
	if code != 0 {
		t.Fatalf("second setup-agent = %d", code)
	}
	content2, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md")) // #nosec G304 -- test scaffolding in t.TempDir
	if string(content) != string(content2) {
		t.Errorf("second setup-agent changed the file:\n%q\n%q", content, content2)
	}
}

func TestCLIProfileFromUserConfig(t *testing.T) {
	// DK_CONFIG pins the user config file (platform-independent).
	cfgPath := filepath.Join(t.TempDir(), "config.toml")
	t.Setenv("DK_CONFIG", cfgPath)
	cfg := `
[profile.demo]
owner = "demo-runner.sub-1"
git_name = "Demo Runner"
git_email = "demo@invalid"
`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	bare := setupBareCLISilent(t)
	_ = bare
	// DK_OWNER would override the profile's owner id; unset it so the
	// profile's owner is what lands in the ticket.
	t.Setenv("DK_OWNER", "")
	// The profile is selected before the command.
	code, _, errMsg := runForTest(t, "--profile", "demo", "add", "--prefix", "app", "-c", "by the demo profile")
	if code != 0 {
		t.Fatalf("add with profile = %d, %q", code, errMsg)
	}
	code, out, _ := runForTest(t, "--profile", "demo", "show", "app-1")
	if code != 0 || !strings.Contains(out, "demo-runner.sub-1") {
		t.Fatalf("show = %d, %q; want the profile's owner id", code, out)
	}

	// An unknown profile name must fail loudly, not fall back.
	code, _, errMsg = runForTest(t, "--profile", "nope", "add", "--prefix", "app", "-c", "x")
	if code != exitError || !strings.Contains(errMsg, "profile") {
		t.Fatalf("unknown profile = %d, %q; want a profile error", code, errMsg)
	}
}

func setupBareCLISilent(t *testing.T) string {
	t.Helper()
	t.Setenv("DK_OWNER", "cli-tester")
	base := t.TempDir()
	bare := filepath.Join(base, "bare.git")
	runGitCmd(t, base, "init", "--quiet", "--bare", "-b", "tickets", bare)
	seed := filepath.Join(base, "seed")
	runGitCmd(t, base, "clone", "--quiet", bare, seed)
	runGitCmd(t, seed, "-c", "user.name=s", "-c", "user.email=s@invalid",
		"commit", "--quiet", "--allow-empty", "-m", "seed")
	runGitCmd(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/tickets")
	t.Setenv("DK_STORE", bare+"#tickets")
	t.Setenv("DK_DATA_DIR", t.TempDir())
	return bare
}
