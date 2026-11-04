// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"strings"
	"testing"

	"github.com/HenrikPuls/dricket/internal/ticket"
)

// commit --release writes the edited ticket and frees the claim in one
// commit, with the same exact-base, owner and epoch checks as commit
// (ADR-0013).
func TestCommitReleaseWritesAndFreesInOneCommit(t *testing.T) {
	svc, ws, bare, id, path := setupCommit(t)
	before := commitCount(t, bare)
	editCommitTicket(t, ws, path, "title: old title", "title: done title")
	result, err := svc.Commit(context.Background(), ws, id, CommitOptions{Release: true, Note: "fixed"})
	if err != nil {
		t.Fatalf("Commit --release: %v", err)
	}
	if result.Rev != 2 || result.Stage != "review" || !result.Synced {
		t.Fatalf("result = %+v", result)
	}
	if commitCount(t, bare) != before+1 {
		t.Error("write and release must be one commit")
	}
	tk := mustTicket(t, bareShow(t, bare, path))
	if tk.Owner != "" || tk.Epoch != 1 || tk.Stage != ticket.StageReview ||
		tk.Title != "done title" || !tk.LeaseUntil.IsZero() || tk.Extensions != 0 {
		t.Errorf("after commit --release: %+v", tk)
	}
	if !strings.Contains(tk.Body, "commit-release app-1 -> review: fixed") {
		t.Errorf("log line missing:\n%s", tk.Body)
	}
	if !strings.Contains(tk.Body, "## Result\n\nfixed") {
		t.Errorf("result note missing:\n%s", tk.Body)
	}
	if msg := runGit(t, bare, "log", "-1", "--format=%s", "tickets"); !strings.Contains(msg, "release -> review") {
		t.Errorf("commit message = %q", msg)
	}

	// The transition policy of the claim's kind applies, like release.
	svc2, ws2, _, id2, path2 := setupCommit(t)
	editCommitTicket(t, ws2, path2, "title: old title", "title: x")
	if _, err := svc2.Commit(context.Background(), ws2, id2, CommitOptions{Release: true, Stage: ticket.StageDone}); ExitCode(err) != 14 {
		t.Errorf("agent release to done = %v, want 14", err)
	}
	// An unknown stage is refused before any write.
	if _, err := svc2.Commit(context.Background(), ws2, id2, CommitOptions{Release: true, Stage: "nowhere"}); ExitCode(err) != 14 {
		t.Errorf("unknown stage = %v, want 14", err)
	}
}
