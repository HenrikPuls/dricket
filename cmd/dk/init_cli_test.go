// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lariscus/dricket/internal/config"
	agentskills "github.com/lariscus/dricket/skills"
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
	code, out, _ = runForTest(t, "add", "-m", "first ticket", "--unclaimed")
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
	if code, out, _ := runForTest(t, "add", "-m", "custom branch", "--unclaimed"); code != 0 || !strings.Contains(out, "app-1") {
		t.Fatalf("add on custom branch = %d, %q", code, out)
	}
}

func TestCLISetupAgent(t *testing.T) {
	setupBareCLI(t)
	dir := t.TempDir()
	t.Chdir(dir)

	code, out, _ := runForTest(t, "setup-agent")
	if code != 0 || !strings.Contains(out, "wrote the store pointer") || !strings.Contains(out, "dricket skill") {
		t.Fatalf("setup-agent = %d, %q", code, out)
	}
	content, err := os.ReadFile(filepath.Join(dir, "AGENTS.md")) // #nosec G304 -- test scaffolding in t.TempDir
	if err != nil || !strings.Contains(string(content), "dk guide") || !strings.Contains(string(content), "branch tickets") {
		t.Fatalf("AGENTS.md = %q, %v", content, err)
	}
	skillPath := filepath.Join(dir, ".agents", "skills", "dricket", "SKILL.md")
	skill, err := os.ReadFile(skillPath) // #nosec G304 -- test scaffolding in t.TempDir
	if err != nil || string(skill) != agentskills.Dricket {
		t.Fatalf("installed skill does not match embedded asset: %v", err)
	}

	// Idempotent: a second run changes nothing and reports both files.
	code, out, _ = runForTest(t, "--json", "setup-agent")
	if code != 0 {
		t.Fatalf("second setup-agent = %d", code)
	}
	var response struct {
		Data struct {
			File         string `json:"file"`
			Written      bool   `json:"written"`
			SkillFile    string `json:"skill_file"`
			SkillWritten bool   `json:"skill_written"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil || response.Data.Written || response.Data.SkillWritten || response.Data.SkillFile != skillPath {
		t.Fatalf("second setup-agent JSON = %q, %v", out, err)
	}
	content2, _ := os.ReadFile(filepath.Join(dir, "AGENTS.md")) // #nosec G304 -- test scaffolding in t.TempDir
	skill2, _ := os.ReadFile(skillPath)                         // #nosec G304 -- test scaffolding in t.TempDir
	if string(content) != string(content2) || string(skill) != string(skill2) {
		t.Error("idempotent setup-agent changed an existing file")
	}
}

func TestCLISetupAgentRefusesModifiedSkill(t *testing.T) {
	setupBareCLI(t)
	dir := t.TempDir()
	t.Chdir(dir)
	skillPath := filepath.Join(dir, ".agents", "skills", "dricket", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillPath, []byte("user-owned skill\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, errMsg := runForTest(t, "setup-agent")
	if code != exitError || !strings.Contains(errMsg, skillPath) {
		t.Fatalf("modified skill setup = %d, %q; want refusal", code, errMsg)
	}
	content, _ := os.ReadFile(skillPath) // #nosec G304 -- test scaffolding in t.TempDir
	if string(content) != "user-owned skill\n" {
		t.Fatalf("modified skill was overwritten: %q", content)
	}
	if _, err := os.Stat(filepath.Join(dir, "AGENTS.md")); !os.IsNotExist(err) {
		t.Fatalf("AGENTS.md changed despite skill preflight failure: %v", err)
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
	code, _, errMsg := runForTest(t, "--profile", "demo", "add", "--prefix", "app", "-m", "by the demo profile")
	if code != 0 {
		t.Fatalf("add with profile = %d, %q", code, errMsg)
	}
	code, out, _ := runForTest(t, "--profile", "demo", "show", "app-1")
	if code != 0 || !strings.Contains(out, "demo-runner.sub-1") {
		t.Fatalf("show = %d, %q; want the profile's owner id", code, out)
	}

	// An unknown profile name must fail loudly, not fall back.
	code, _, errMsg = runForTest(t, "--profile", "nope", "add", "--prefix", "app", "-m", "x")
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

// R-27: the generated dricket.toml must stay valid when a [stages] table is
// added, and must still configure the built-in stage set without one.
func TestScaffoldConfigAcceptsCustomStages(t *testing.T) {
	toml := scaffoldFiles("tickets", "app")["dricket.toml"]
	if _, err := config.Parse([]byte(toml)); err != nil {
		t.Fatalf("default scaffold does not parse: %v", err)
	}
	custom := toml + `
[stages]
values = ["inbox", "prioritized", "build", "commit", "done"]
initial = "inbox"
terminal = ["done"]
claim = ["build", "commit"]
cancelled = "cancelled"
`
	cfg, err := config.Parse([]byte(custom))
	if err != nil {
		t.Fatalf("scaffold plus custom [stages] is invalid: %v", err)
	}
	if got := cfg.Stages.Initial; got != "inbox" {
		t.Errorf("initial stage = %q; want inbox", got)
	}
}
