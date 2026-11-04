// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
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

	code, out, errMsg := runForTest(t, "add", "--prefix", "app", "-m", "Login error", "--unclaimed", "-l", "bug,auth")
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

	code, out, errMsg = runForTest(t, "comment", "app-1", "-m", "first comment")
	if code != 0 || !strings.Contains(out, "/comments/") || !strings.HasSuffix(strings.TrimSpace(out), "-cli-tester.md") {
		t.Fatalf("comment = %d, %q, %q", code, out, errMsg)
	}

	code, out, errMsg = putCLI(t, "app-1", -1, func(content string) string {
		return strings.Replace(content, "title: Login error", "title: Fixed title", 1)
	})
	if code != 0 || dataOf(t, out)["rev"] != float64(2) {
		t.Fatalf("put = %d, %q, %q", code, out, errMsg)
	}

	code, out, errMsg = runForTest(t, "release", "--message", "fix on dk/app-1", "app-1")
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

	code, out, _ := runForTest(t, "--json", "add", "--prefix", "app", "-m", "json ticket", "--unclaimed")
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
	runForTest(t, "add", "--prefix", "app", "-m", "x", "--unclaimed")

	cases := []struct {
		args []string
		code int
	}{
		{[]string{"show", "app-99"}, 10},
		{[]string{"set", "app-1", "x=1"}, 2}, // removed command (ADR-0013)
		{[]string{"claim", "NOT-AN-ID"}, 14},
		{[]string{"add", "-m", "no prefix"}, 2},
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
	for _, want := range []string{"add", "claim", "release", "comment", "put", "ls", "show", "lint", "version"} {
		if !strings.Contains(out, want) {
			t.Errorf("help misses %q", want)
		}
	}
}

func dataOf(t *testing.T, out string) map[string]any {
	t.Helper()
	var env struct {
		SchemaVersion int            `json:"schema_version"`
		Data          map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &env); err != nil || env.SchemaVersion != 1 {
		t.Fatalf("not an envelope: %q (%v)", out, err)
	}
	return env.Data
}

// putCLI writes ticket id back through `dk put --json`, applying edit to
// the current content. ifVersion >= 0 adds --claim and --if-version (the
// unclaimed path, ADR-0013); -1 writes as the claim holder.
func putCLI(t *testing.T, id string, ifVersion int, edit func(string) string) (int, string, string) {
	t.Helper()
	_, shown, _ := runForTest(t, "--json", "show", id)
	content, _ := dataOf(t, shown)["content"].(string)
	f := filepath.Join(t.TempDir(), "ticket.md")
	if err := os.WriteFile(f, []byte(edit(content)), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{"--json", "put", id, "-F", f}
	if ifVersion >= 0 {
		args = append(args, "--claim", "--if-version", strconv.Itoa(ifVersion))
	}
	return runForTest(t, args...)
}

func TestCLIExtensionFieldsAndPut(t *testing.T) {
	setupBareCLI(t)

	code, out, errMsg := runForTest(t, "--json", "add", "--prefix", "app", "-m", "Drey ticket", "--unclaimed",
		"--set", "x-persona=ben", "--set", "x-rank=10")
	if code != 0 {
		t.Fatalf("add = %d, %q, %q", code, out, errMsg)
	}

	// Extension fields change through put of the whole document: set,
	// change and remove keys in one commit (ADR-0013).
	code, out, errMsg = putCLI(t, "app-1", 0, func(content string) string {
		content = strings.Replace(content, "x-persona: ben", "x-persona: paul", 1)
		content = strings.Replace(content, "x-rank: 10\n", "", 1)
		return strings.Replace(content, "labels: ", "x-chain: a,b\nlabels: ", 1)
	})
	if code != 0 {
		t.Fatalf("put = %d, %q, %q", code, out, errMsg)
	}
	data := dataOf(t, out)
	if data["rev"] != float64(1) || data["short_claim"] != true {
		t.Fatalf("put json = %v", data)
	}
	code, out, _ = runForTest(t, "show", "app-1")
	if code != 0 || !strings.Contains(out, "x-persona: paul") || !strings.Contains(out, "x-chain: a,b") || strings.Contains(out, "x-rank:") {
		t.Fatalf("show = %d, %q", code, out)
	}

	// put needs -F <file> or '-'.
	if code, _, _ = runForTest(t, "put", "app-1"); code != 2 {
		t.Errorf("put without input = %d, want 2", code)
	}

	// An unclaimed ticket writes only under --claim, with --if-version.
	file := filepath.Join(t.TempDir(), "ticket.md")
	_, shown, _ := runForTest(t, "--json", "show", "app-1")
	content, _ := dataOf(t, shown)["content"].(string)
	if err := os.WriteFile(file, []byte(strings.Replace(content, "(no description)", "Written by put.", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _, _ = runForTest(t, "put", "app-1", "-F", file, "--if-version", "1"); code != 17 {
		t.Errorf("claim-free put on an unclaimed ticket = %d, want 17", code)
	}
	if code, _, _ = runForTest(t, "put", "app-1", "-F", file, "--claim"); code != 14 {
		t.Errorf("put --claim without --if-version = %d, want 14", code)
	}
	if code, _, _ = runForTest(t, "put", "app-1", "-F", file, "--claim", "--if-version", "5"); code != 12 {
		t.Errorf("put with a stale version = %d, want 12", code)
	}
	code, out, errMsg = runForTest(t, "--json", "put", "app-1", "-F", file, "--claim", "--if-version", "1")
	if code != 0 {
		t.Fatalf("put = %d, %q, %q", code, out, errMsg)
	}
	data = dataOf(t, out)
	if data["id"] != "app-1" || data["rev"] != float64(2) || data["commit"] == "" || data["commit"] == nil {
		t.Fatalf("put json = %v", data)
	}
	if got, _ := data["changed_fields"].([]any); len(got) != 1 || got[0] != "body" {
		t.Fatalf("changed_fields = %v", data["changed_fields"])
	}

	// A stage change is a put of the edited header line.
	code, out, errMsg = putCLI(t, "app-1", 2, func(content string) string {
		return strings.Replace(content, "stage: open", "stage: review", 1)
	})
	if code != 0 {
		t.Fatalf("stage put = %d, %q, %q", code, out, errMsg)
	}
	data = dataOf(t, out)
	if got, _ := data["changed_fields"].([]any); data["rev"] != float64(3) || len(got) != 1 || got[0] != "stage" {
		t.Fatalf("stage put json = %v", data)
	}
	if code, _, _ = putCLI(t, "app-1", 3, func(content string) string {
		return strings.Replace(content, "stage: review", "stage: nowhere", 1)
	}); code != 14 {
		t.Errorf("unknown stage = %d, want 14", code)
	}
}
