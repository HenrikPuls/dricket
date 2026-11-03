// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HenrikPuls/dricket/internal/store"
	"github.com/HenrikPuls/dricket/internal/ticket"
)

func setupCommit(t *testing.T) (*Service, *store.Workspace, string, string, string) {
	t.Helper()
	bare := setupBare(t)
	svc := open(t, bare, "alice")
	id, err := svc.Add(context.Background(), AddRequest{Prefix: "app", Title: "old title"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "checkout")
	runGit(t, t.TempDir(), "clone", "--quiet", bare, dir)
	ws, err := store.OpenWorkspace(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	path, err := PathFor(id)
	if err != nil {
		t.Fatal(err)
	}
	return svc, ws, bare, id, path
}

func editCommitTicket(t *testing.T, ws *store.Workspace, path, old, new string) {
	t.Helper()
	full := filepath.Join(ws.Dir(), filepath.FromSlash(path))
	content, err := os.ReadFile(full)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), old) {
		t.Fatalf("%q not found in ticket", old)
	}
	if err := os.WriteFile(full, []byte(strings.Replace(string(content), old, new, 1)), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCommitWholeTicketEdit(t *testing.T) {
	svc, ws, bare, id, path := setupCommit(t)
	editCommitTicket(t, ws, path, "title: old title", "title: new title")
	result, err := svc.Commit(context.Background(), ws, id)
	if err != nil {
		t.Fatalf("Commit: %v", err)
	}
	if result.ID != id || result.Rev != 2 || result.Commit == "" || !result.Synced {
		t.Fatalf("result = %+v", result)
	}
	tk := mustTicket(t, bareShow(t, bare, path))
	if tk.Title != "new title" || tk.Rev != 2 || tk.Owner != "alice" || tk.Epoch != 1 {
		t.Fatalf("remote ticket = %+v", tk)
	}
	local := runGit(t, ws.Dir(), "show", "HEAD:"+path)
	if !strings.Contains(local, "title: new title") || !strings.Contains(local, "rev: 2") {
		t.Fatalf("local checkout not adopted:\n%s", local)
	}
}

func TestCommitRejectsMalformedAndProtocolEdits(t *testing.T) {
	for _, tc := range []struct {
		name string
		old  string
		new  string
	}{
		{"malformed", "title: old title", "title"},
		{"identity", "created_by: alice", "created_by: mallory"},
		{"claim", "epoch: 1", "epoch: 2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc, ws, bare, id, path := setupCommit(t)
			editCommitTicket(t, ws, path, tc.old, tc.new)
			before := bareShow(t, bare, path)
			_, err := svc.Commit(context.Background(), ws, id)
			if ExitCode(err) != 14 {
				t.Fatalf("err = %v, code %d; want 14", err, ExitCode(err))
			}
			if after := bareShow(t, bare, path); after != before {
				t.Fatal("remote ticket changed")
			}
			full := filepath.Join(ws.Dir(), filepath.FromSlash(path))
			content, _ := os.ReadFile(full)
			if !strings.Contains(string(content), tc.new) {
				t.Fatalf("local edit was not preserved: %q", content)
			}
		})
	}
}

func TestCommitRejectsStaleTicketAndPreservesEdit(t *testing.T) {
	svc, ws, bare, id, path := setupCommit(t)
	editCommitTicket(t, ws, path, "title: old title", "title: local edit")
	if _, err := svc.Note(context.Background(), id, "remote progress"); err != nil {
		t.Fatal(err)
	}
	_, err := svc.Commit(context.Background(), ws, id)
	if ExitCode(err) != 12 {
		t.Fatalf("err = %v, code %d; want 12", err, ExitCode(err))
	}
	full := filepath.Join(ws.Dir(), filepath.FromSlash(path))
	content, _ := os.ReadFile(full)
	if !strings.Contains(string(content), "title: local edit") {
		t.Fatalf("local edit lost: %q", content)
	}
	if got := bareShow(t, bare, path); !strings.Contains(got, "title: old title") || !strings.Contains(got, "remote progress") {
		t.Fatalf("remote state overwritten: %s", got)
	}
}

func TestCommitAllowsUnrelatedRemoteWrite(t *testing.T) {
	svc, ws, _, id, path := setupCommit(t)
	bob := open(t, wsRemote(t, ws), "bob")
	if _, err := bob.Add(context.Background(), AddRequest{Prefix: "app", Title: "unrelated", Unclaimed: true}); err != nil {
		t.Fatal(err)
	}
	editCommitTicket(t, ws, path, "title: old title", "title: after retry")
	result, err := svc.Commit(context.Background(), ws, id)
	if err != nil || !result.Synced {
		t.Fatalf("Commit = %+v, %v", result, err)
	}
}

func TestCommitAcceptedPushWithAdoptFailure(t *testing.T) {
	svc, ws, bare, id, path := setupCommit(t)
	editCommitTicket(t, ws, path, "title: old title", "title: remotely accepted")
	if err := os.WriteFile(filepath.Join(ws.Dir(), "other-local.txt"), []byte("keep me\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := svc.Commit(context.Background(), ws, id)
	if err != nil {
		t.Fatalf("accepted push returned error: %v", err)
	}
	if result.Synced || result.Commit == "" {
		t.Fatalf("result = %+v", result)
	}
	if got := bareShow(t, bare, path); !strings.Contains(got, "title: remotely accepted") {
		t.Fatalf("remote edit missing: %s", got)
	}
	content, _ := os.ReadFile(filepath.Join(ws.Dir(), "other-local.txt"))
	if string(content) != "keep me\n" {
		t.Fatalf("unrelated local file = %q", content)
	}
}

func wsRemote(t *testing.T, ws *store.Workspace) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, ws.Dir(), "remote", "get-url", "origin"))
}

func TestCommitRejectsReleasedTicket(t *testing.T) {
	svc, ws, _, id, path := setupCommit(t)
	if _, err := svc.Release(context.Background(), id, ticket.StageReview, ""); err != nil {
		t.Fatal(err)
	}
	editCommitTicket(t, ws, path, "title: old title", "title: stale claim")
	_, err := svc.Commit(context.Background(), ws, id)
	if ExitCode(err) != 13 && ExitCode(err) != 11 {
		t.Fatalf("err = %v, code %d; want lost or claimed", err, ExitCode(err))
	}
}
