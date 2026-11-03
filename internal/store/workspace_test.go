// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const workspaceTicket = "tickets/app/0000/app-1/ticket.md"

func setupWorkspace(t *testing.T) (bare, dir string) {
	t.Helper()
	bare = setupBare(t, true)
	dir = filepath.Join(t.TempDir(), "checkout")
	runGit(t, t.TempDir(), "clone", "--quiet", bare, dir)
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(filepath.FromSlash(workspaceTicket))), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(workspaceTicket)), []byte("base ticket\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "other.txt"), []byte("other base\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "--", workspaceTicket, "other.txt")
	runGit(t, dir, "-c", "user.name=alice", "-c", "user.email=alice@invalid", "commit", "--quiet", "-m", "ticket")
	runGit(t, dir, "push", "--quiet", "origin", "HEAD:refs/heads/tickets")
	return bare, dir
}

func openWorkspaceForTest(t *testing.T, dir string) *Workspace {
	t.Helper()
	ws, err := OpenWorkspace(context.Background(), dir)
	if err != nil {
		t.Fatalf("OpenWorkspace: %v", err)
	}
	return ws
}

func TestWorkspaceSyncFastForwardsCleanCheckout(t *testing.T) {
	bare, dir := setupWorkspace(t)
	remote := openStore(t, bare, "bob", nil)
	if err := remote.Write(context.Background(), writeFileSpec("tickets/remote.txt", "remote\n", nil)); err != nil {
		t.Fatal(err)
	}
	ws := openWorkspaceForTest(t, dir)
	if err := ws.Sync(context.Background()); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if got := runGit(t, dir, "show", "HEAD:tickets/remote.txt"); got != "remote\n" {
		t.Fatalf("remote Git content = %q", got)
	}
}

func TestWorkspaceSyncRefusesDirtyCheckout(t *testing.T) {
	_, dir := setupWorkspace(t)
	path := filepath.Join(dir, filepath.FromSlash(workspaceTicket))
	if err := os.WriteFile(path, []byte("unique local work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := openWorkspaceForTest(t, dir)
	if err := ws.Sync(context.Background()); err == nil {
		t.Fatal("Sync accepted a dirty checkout")
	}
	if got, _ := os.ReadFile(path); string(got) != "unique local work\n" { // #nosec G304 -- test-controlled temporary path
		t.Fatalf("local work changed to %q", got)
	}
}

func TestWorkspaceSyncRefusesNonFastForward(t *testing.T) {
	bare, dir := setupWorkspace(t)
	remote := openStore(t, bare, "bob", nil)
	if err := remote.Write(context.Background(), writeFileSpec("tickets/remote.txt", "remote\n", nil)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "local.txt"), []byte("local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "local.txt")
	runGit(t, dir, "-c", "user.name=alice", "-c", "user.email=alice@invalid", "commit", "--quiet", "-m", "diverge")
	before := runGit(t, dir, "rev-parse", "HEAD")
	ws := openWorkspaceForTest(t, dir)
	if err := ws.Sync(context.Background()); err == nil {
		t.Fatal("Sync accepted diverged history")
	}
	if after := runGit(t, dir, "rev-parse", "HEAD"); after != before {
		t.Fatalf("HEAD moved from %s to %s", before, after)
	}
}

func TestWorkspaceRestoreAndReadPair(t *testing.T) {
	bare, dir := setupWorkspace(t)
	ticketPath := filepath.Join(dir, filepath.FromSlash(workspaceTicket))
	otherPath := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(ticketPath, []byte("edited ticket\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(otherPath, []byte("edited other\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := openWorkspaceForTest(t, dir)
	base, edited, err := ws.ReadPair(context.Background(), workspaceTicket)
	if err != nil || base != "base ticket\n" || edited != "edited ticket\n" {
		t.Fatalf("ReadPair = %q, %q, %v", base, edited, err)
	}
	// Restore is local-only and therefore still works with an unreachable origin.
	runGit(t, dir, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing.git"))
	if err := ws.Restore(context.Background(), workspaceTicket); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if got, _ := os.ReadFile(ticketPath); strings.ReplaceAll(string(got), "\r\n", "\n") != "base ticket\n" { // #nosec G304 -- test-controlled temporary path
		t.Errorf("restored ticket = %q", got)
	}
	if got, _ := os.ReadFile(otherPath); string(got) != "edited other\n" { // #nosec G304 -- test-controlled temporary path
		t.Errorf("unrelated file = %q", got)
	}
	_ = bare
}

func TestWorkspaceAdoptProtectsOtherChanges(t *testing.T) {
	bare, dir := setupWorkspace(t)
	remote := openStore(t, bare, "bob", nil)
	if err := remote.Write(context.Background(), writeFileSpec("tickets/remote.txt", "remote\n", nil)); err != nil {
		t.Fatal(err)
	}
	commit := strings.TrimSpace(runGit(t, bare, "rev-parse", "tickets"))
	path := filepath.Join(dir, filepath.FromSlash(workspaceTicket))
	other := filepath.Join(dir, "other.txt")
	if err := os.WriteFile(path, []byte("published edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(other, []byte("unique other work\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := openWorkspaceForTest(t, dir)
	if err := ws.Adopt(context.Background(), commit, workspaceTicket, "published edit\n"); err == nil {
		t.Fatal("Adopt accepted an unrelated dirty file")
	}
	if got, _ := os.ReadFile(other); string(got) != "unique other work\n" { // #nosec G304 -- test-controlled temporary path
		t.Fatalf("other work changed to %q", got)
	}
	if err := ws.Restore(context.Background(), "other.txt"); err != nil {
		t.Fatal(err)
	}
	if err := ws.Adopt(context.Background(), commit, workspaceTicket, "published edit\n"); err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	if got := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD")); got != commit {
		t.Fatalf("HEAD = %s, want %s", got, commit)
	}
	if _, err := os.Stat(filepath.Join(dir, "tickets", "remote.txt")); err != nil {
		t.Fatalf("accepted remote content missing: %v", err)
	}
}

func TestWorkspaceAdoptRefusesUnpushedCommit(t *testing.T) {
	bare, dir := setupWorkspace(t)
	localFile := filepath.Join(dir, "unpushed.txt")
	if err := os.WriteFile(localFile, []byte("local commit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "unpushed.txt")
	runGit(t, dir, "-c", "user.name=alice", "-c", "user.email=alice@invalid", "commit", "--quiet", "-m", "unpushed")
	localHead := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))
	remote := openStore(t, bare, "bob", nil)
	if err := remote.Write(context.Background(), writeFileSpec("tickets/remote.txt", "remote\n", nil)); err != nil {
		t.Fatal(err)
	}
	accepted := strings.TrimSpace(runGit(t, bare, "rev-parse", "tickets"))
	if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(workspaceTicket)), []byte("published edit\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	ws := openWorkspaceForTest(t, dir)
	if err := ws.Adopt(context.Background(), accepted, workspaceTicket, "published edit\n"); err == nil {
		t.Fatal("Adopt accepted divergent local history")
	}
	if got := strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD")); got != localHead {
		t.Fatalf("HEAD = %s, want preserved %s", got, localHead)
	}
	if got, _ := os.ReadFile(localFile); string(got) != "local commit\n" { // #nosec G304 -- test-controlled temporary path
		t.Fatalf("unpushed file = %q", got)
	}
}
