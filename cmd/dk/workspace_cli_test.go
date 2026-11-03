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

func cliGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) // #nosec G204 -- test scaffolding
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func inCLIDir(t *testing.T, dir string) {
	t.Helper()
	old, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
}

func setupCLIWorkspace(t *testing.T) (bare, dir string) {
	t.Helper()
	bare = setupBareCLI(t)
	if code, out, errMsg := runForTest(t, "add", "--prefix", "app", "-c", "old title"); code != 0 {
		t.Fatalf("add = %d, %q, %q", code, out, errMsg)
	}
	dir = filepath.Join(t.TempDir(), "checkout")
	cliGit(t, t.TempDir(), "clone", "--quiet", bare, dir)
	inCLIDir(t, dir)
	return bare, dir
}

func editCLICommitTicket(t *testing.T, dir, old, replacement string) {
	t.Helper()
	path := filepath.Join(dir, "tickets", "app", "0000", "app-1", "ticket.md")
	content, err := os.ReadFile(path) // #nosec G304 -- test-controlled temporary path
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(content), old, replacement, 1)), 0o600); err != nil { // #nosec G703 -- test-controlled temporary path
		t.Fatal(err)
	}
}

func TestCommitCLIWorkflow(t *testing.T) {
	_, dir := setupCLIWorkspace(t)
	if code, out, errMsg := runForTest(t, "sync"); code != 0 {
		t.Fatalf("sync = %d, %q, %q", code, out, errMsg)
	}
	editCLICommitTicket(t, dir, "title: old title", "title: new title")
	code, out, errMsg := runForTest(t, "--json", "commit", "app-1")
	if code != 0 {
		t.Fatalf("commit = %d, %q, %q", code, out, errMsg)
	}
	var response struct {
		SchemaVersion int `json:"schema_version"`
		Data          struct {
			ID     string `json:"id"`
			Rev    int    `json:"rev"`
			Commit string `json:"commit"`
			Synced bool   `json:"synced"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &response); err != nil {
		t.Fatalf("json: %v in %q", err, out)
	}
	if response.SchemaVersion != 1 || response.Data.ID != "app-1" || response.Data.Rev != 2 || response.Data.Commit == "" || !response.Data.Synced {
		t.Fatalf("response = %+v", response)
	}
	if code, _, errMsg := runForTest(t, "commit"); code != exitUsage || !strings.Contains(errMsg, "exactly one") {
		t.Fatalf("commit usage = %d, %q", code, errMsg)
	}
}

func TestSyncRestoreAndCommitErrors(t *testing.T) {
	_, dir := setupCLIWorkspace(t)
	editCLICommitTicket(t, dir, "title: old title", "title")
	if code, _, _ := runForTest(t, "commit", "app-1"); code != 14 {
		t.Fatalf("malformed commit = %d, want 14", code)
	}
	if code, _, _ := runForTest(t, "restore", "tickets/app/0000/app-1/ticket.md"); code != 0 {
		t.Fatalf("restore = %d", code)
	}
	editCLICommitTicket(t, dir, "title: old title", "title: local")
	if code, _, _ := runForTest(t, "sync"); code != 1 {
		t.Fatalf("dirty sync = %d, want 1", code)
	}
	if code, _, _ := runForTest(t, "note", "app-1", "remote"); code != 0 {
		t.Fatalf("note = %d", code)
	}
	if code, _, _ := runForTest(t, "commit", "app-1"); code != 12 {
		t.Fatalf("stale commit = %d, want 12", code)
	}
}

func TestCommitCLIRejectsStoreMismatch(t *testing.T) {
	_, _ = setupCLIWorkspace(t)
	other := setupBareCLI(t)
	_ = other
	editCLICommitTicket(t, ".", "title: old title", "title: mismatch")
	if code, _, errMsg := runForTest(t, "commit", "app-1"); code != exitUsage || !strings.Contains(errMsg, "does not match") {
		t.Fatalf("mismatch = %d, %q", code, errMsg)
	}
}

func TestCLIHelpUsesWorkspaceCommands(t *testing.T) {
	code, out, _ := runForTest(t, "--help")
	if code != 0 {
		t.Fatal(code)
	}
	for _, want := range []string{"sync", "restore", "commit"} {
		if !strings.Contains(out, want) {
			t.Errorf("help misses %q", want)
		}
	}
	if strings.Contains(out, "\n  edit ") {
		t.Error("help contains removed edit command")
	}
}
