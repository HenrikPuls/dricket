// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// setupBareCLI creates a seeded bare repository and points DK_STORE at it.
func setupBareCLI(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	bare := filepath.Join(base, "bare.git")
	for _, args := range [][]string{
		{"init", "--quiet", "--bare", "-b", "tickets", bare},
	} {
		cmd := exec.Command("git", args...) // #nosec G204 -- test scaffolding
		cmd.Dir = base
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	seed := filepath.Join(base, "seed")
	cmd := exec.Command("git", "clone", "--quiet", bare, seed) // #nosec G204 -- test scaffolding
	cmd.Dir = base
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("clone: %v: %s", err, out)
	}
	for _, args := range [][]string{
		{"-c", "user.name=seed", "-c", "user.email=seed@invalid", "commit", "--quiet", "--allow-empty", "-m", "seed"},
		{"push", "--quiet", "origin", "HEAD:refs/heads/tickets"},
	} {
		c := exec.Command("git", args...) // #nosec G204 -- test scaffolding
		c.Dir = seed
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	t.Setenv("DK_STORE", bare+"#tickets")
	t.Setenv("DK_OWNER", "cli-tester")
	t.Setenv("DK_DATA_DIR", t.TempDir())
	return bare
}

func TestCLIHappyPath(t *testing.T) {
	setupBareCLI(t)

	code, out, errMsg := runForTest(t, "add", "--prefix", "app", "-c", "Login error", "--unclaimed", "-l", "bug,auth")
	if code != 0 || strings.TrimSpace(out) != "app-1" {
		t.Fatalf("add = %d, %q, %q", code, out, errMsg)
	}

	code, out, errMsg = runForTest(t, "claim", "--size", "M", "app-1")
	if code != 0 || !strings.Contains(out, "claimed app-1") || !strings.Contains(out, "epoch 1") {
		t.Fatalf("claim = %d, %q, %q", code, out, errMsg)
	}

	code, out, errMsg = runForTest(t, "show", "app-1")
	if code != 0 || !strings.Contains(out, "id: app-1") || !strings.Contains(out, "owner: cli-tester") {
		t.Fatalf("show = %d, %q, %q", code, out, errMsg)
	}

	code, out, errMsg = runForTest(t, "comment", "app-1", "first comment")
	if code != 0 || !strings.Contains(out, "comments/0001.md") {
		t.Fatalf("comment = %d, %q, %q", code, out, errMsg)
	}

	code, out, errMsg = runForTest(t, "set", "app-1", "title=Fixed title")
	if code != 0 || !strings.Contains(out, "title = Fixed title") {
		t.Fatalf("set = %d, %q, %q", code, out, errMsg)
	}

	code, out, errMsg = runForTest(t, "release", "--result", "fix on dk/app-1", "app-1")
	if code != 0 || !strings.Contains(out, "released app-1 -> review") {
		t.Fatalf("release = %d, %q, %q", code, out, errMsg)
	}

	code, out, _ = runForTest(t, "ls")
	if code != 0 || !strings.Contains(out, "app-1") || !strings.Contains(out, "review") {
		t.Fatalf("ls = %d, %q", code, out)
	}

	code, _, _ = runForTest(t, "lint")
	if code != 0 {
		t.Fatalf("lint = %d", code)
	}
}

func TestCLIJSONEnvelopes(t *testing.T) {
	setupBareCLI(t)

	code, out, _ := runForTest(t, "--json", "add", "--prefix", "app", "-c", "json ticket", "--unclaimed")
	if code != 0 {
		t.Fatalf("add = %d", code)
	}
	var add struct {
		SchemaVersion int `json:"schema_version"`
		Data          struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &add); err != nil || add.Data.ID != "app-1" || add.SchemaVersion != 1 {
		t.Fatalf("add json = %q (%v)", out, err)
	}

	claimCode, claimOut, _ := runForTest(t, "--json", "claim", "app-1")
	if claimCode != 0 {
		t.Fatalf("claim = %d", claimCode)
	}
	var claim struct {
		Data struct {
			ID    string `json:"id"`
			Epoch int    `json:"epoch"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(claimOut), &claim); err != nil || claim.Data.Epoch != 1 {
		t.Fatalf("claim json = %q (%v)", claimOut, err)
	}
}

func TestCLIExitCodes(t *testing.T) {
	setupBareCLI(t)
	runForTest(t, "add", "--prefix", "app", "-c", "x", "--unclaimed")

	cases := []struct {
		args []string
		code int
	}{
		{[]string{"show", "app-99"}, 10},
		{[]string{"set", "app-1", "stage=done"}, 14},
		{[]string{"claim", "NOT-AN-ID"}, 14},
		{[]string{"add", "-c", "no prefix"}, 2},
		{[]string{"bogus"}, 2},
	}
	for _, c := range cases {
		code, _, _ := runForTest(t, c.args...)
		if code != c.code {
			t.Errorf("%v = exit %d, want %d", c.args, code, c.code)
		}
	}
}

func TestCLINoStoreConfigured(t *testing.T) {
	t.Setenv("DK_STORE", "")
	if os.Getenv("DK_STORE") != "" {
		t.Skip("cannot clear DK_STORE")
	}
	// --store= (empty) falls back to DK_STORE, which is empty here.
	code, _, errMsg := runForTest(t, "ls")
	if code != exitUsage || !strings.Contains(errMsg, "no ticket store") {
		t.Fatalf("ls without store = %d, %q", code, errMsg)
	}
}

func TestCLIHelpListsCommands(t *testing.T) {
	code, out, _ := runForTest(t, "--help")
	if code != 0 {
		t.Fatalf("help = %d", code)
	}
	for _, want := range []string{"add", "claim", "release", "comment", "set", "ls", "show", "lint", "version"} {
		if !strings.Contains(out, want) {
			t.Errorf("help misses %q", want)
		}
	}
}
