// SPDX-License-Identifier: GPL-3.0-or-later
package validate

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/HenrikPuls/dricket/internal/frontmatter"
	"github.com/HenrikPuls/dricket/internal/profile"
	"github.com/HenrikPuls/dricket/internal/service"
	"github.com/HenrikPuls/dricket/internal/store"
	"github.com/HenrikPuls/dricket/internal/ticket"
)

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) // #nosec G204 -- test scaffolding
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
	return string(out)
}

func setupBare(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	bare := filepath.Join(base, "bare.git")
	runGit(t, base, "init", "--quiet", "--bare", "-b", "tickets", bare)
	seed := filepath.Join(base, "seed")
	runGit(t, base, "clone", "--quiet", bare, seed)
	runGit(t, seed, "-c", "user.name=seed", "-c", "user.email=seed@invalid",
		"commit", "--quiet", "--allow-empty", "-m", "seed")
	runGit(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/tickets")
	return bare
}

func openSvc(t *testing.T, bare, owner string) *service.Service {
	t.Helper()
	st, err := store.Open(context.Background(), store.Options{
		Remote:  bare,
		Branch:  "tickets",
		DataDir: t.TempDir(),
		Profile: profile.Profile{Name: owner, OwnerID: owner, GitName: "n-" + owner, GitEmail: owner + "@invalid"},
	})
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return service.New(st, owner)
}

// rewrite lets a "hacker" write around the tool: change the parsed ticket
// with a function and push it with the plain store.
func rewrite(t *testing.T, bare, id string, change func(tk *ticket.Ticket)) {
	t.Helper()
	svc := openSvc(t, bare, "hacker")
	path, err := service.PathFor(id)
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	spec := store.WriteSpec{
		Message: "hack: rewrite " + id,
		Mutate: func(w store.Worktree) error {
			content, err := w.ReadFile(path)
			if err != nil {
				return err
			}
			doc, err := frontmatter.Parse(content)
			if err != nil {
				return err
			}
			tk, _, err := ticket.FromDoc(doc)
			if err != nil {
				return err
			}
			change(&tk)
			return w.WriteFile(path, tk.ToDoc().Render())
		},
	}
	if err := svc.Store.Write(context.Background(), spec); err != nil {
		t.Fatalf("rewrite %s: %v", id, err)
	}
}

// rewriteFile pushes new content for an arbitrary path, bypassing the
// service rules.
func rewriteFile(t *testing.T, bare, path, content string) {
	t.Helper()
	svc := openSvc(t, bare, "hacker")
	spec := store.WriteSpec{
		Message: "hack: rewrite " + path,
		Mutate: func(w store.Worktree) error {
			return w.WriteFile(path, content)
		},
	}
	if err := svc.Store.Write(context.Background(), spec); err != nil {
		t.Fatalf("rewrite %s: %v", path, err)
	}
}

// kindsOf collects the finding kinds of a ticket.
func kindsOf(findings []Finding, id string) map[string]bool {
	out := map[string]bool{}
	for _, f := range findings {
		if id == "" || f.Ticket == id {
			out[f.Kind] = true
		}
	}
	return out
}

// buildLegalHistory creates a busy but legal ticket history: add, claim,
// note, comment, extend, release with result, a second ticket, a takeover
// after lease expiry, and a re-claim. It returns the path of bob's
// comment file.
func buildLegalHistory(t *testing.T, bare string) string {
	t.Helper()
	ctx := context.Background()
	alice := openSvc(t, bare, "alice")
	bob := openSvc(t, bare, "bob")

	id1, err := alice.Add(ctx, service.AddRequest{Prefix: "app", Title: "Login error", Unclaimed: true, Labels: []string{"bug"}})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := alice.Claim(ctx, id1, ticket.SizeM, ticket.KindAgent); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := alice.Note(ctx, id1, "root cause found"); err != nil {
		t.Fatalf("note: %v", err)
	}
	if _, err := alice.Extend(ctx, id1, "tests take longer"); err != nil {
		t.Fatalf("extend: %v", err)
	}
	commentPath, err := bob.Comment(ctx, id1, "cannot reproduce on macOS")
	if err != nil {
		t.Fatalf("comment: %v", err)
	}
	if _, err := alice.Release(ctx, id1, ticket.StageReview, "fix on dk/app-1"); err != nil {
		t.Fatalf("release: %v", err)
	}

	id2, err := bob.Add(ctx, service.AddRequest{Prefix: "app", Title: "Second ticket", Parent: id1, Unclaimed: true})
	if err != nil {
		t.Fatalf("add 2: %v", err)
	}

	// Takeover: alice's fresh claim expires long ago, bob takes over.
	if _, err := alice.Claim(ctx, id2, ticket.SizeS, ticket.KindAgent); err != nil {
		t.Fatalf("claim 2: %v", err)
	}
	rewrite(t, bare, id2, func(tk *ticket.Ticket) {
		tk.LeaseUntil = time.Now().Add(-2 * time.Hour)
		tk.Rev++
	})
	res, err := bob.Claim(ctx, id2, ticket.SizeM, ticket.KindAgent)
	if err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if !res.Takeover {
		t.Fatalf("expected a takeover, got %+v", res)
	}
	return commentPath
}

func TestValidateCleanHistory(t *testing.T) {
	bare := setupBare(t)
	buildLegalHistory(t, bare)

	svc := openSvc(t, bare, "auditor")
	findings, err := Store(context.Background(), svc.Store)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, f := range findings {
		t.Errorf("unexpected finding: %s", f)
	}
}

func TestValidateDetectsManipulations(t *testing.T) {
	bare := setupBare(t)
	buildLegalHistory(t, bare)

	// Identity rewrite.
	rewrite(t, bare, "app-1", func(tk *ticket.Ticket) { tk.CreatedBy = "mallory" })
	// Rev jump.
	rewrite(t, bare, "app-1", func(tk *ticket.Ticket) { tk.Rev += 2 })
	// Illegal stage transition by an agent: review -> done.
	rewrite(t, bare, "app-1", func(tk *ticket.Ticket) { tk.Stage = ticket.StageDone })
	// Dangling depends on the other ticket.
	rewrite(t, bare, "app-2", func(tk *ticket.Ticket) { tk.Depends = []string{"app-99"} })
	// Comment rewritten: same front matter, tampered body — exactly the
	// edit that v0.2 naming must catch as an M on an add-only file.
	comment := runGit(t, bare, "ls-tree", "-r", "--name-only", "tickets")
	var commentPath string
	for _, l := range strings.Split(comment, "\n") {
		if strings.Contains(l, "/comments/") && strings.HasSuffix(l, "-bob.md") {
			commentPath = strings.TrimSpace(l)
		}
	}
	if commentPath == "" {
		t.Fatal("bob's comment file not found")
	}
	content := runGit(t, bare, "show", "tickets:"+commentPath)
	rewriteFile(t, bare, commentPath, strings.Replace(content, "cannot reproduce", "REWRITTEN by mallory", 1))

	svc := openSvc(t, bare, "auditor")
	findings, err := Store(context.Background(), svc.Store)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	k1 := kindsOf(findings, "app-1")
	for _, want := range []string{"identity-changed", "rev-not-incremented", "illegal-transition", "comment-modified"} {
		if !k1[want] {
			t.Errorf("app-1 missing finding %q; got %v", want, k1)
		}
	}
	k2 := kindsOf(findings, "app-2")
	if !k2["dangling-depends"] {
		t.Errorf("app-2 missing dangling-depends; got %v", k2)
	}
}

func TestValidateDetectsCycle(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := openSvc(t, bare, "alice")
	id1, _ := alice.Add(ctx, service.AddRequest{Prefix: "app", Title: "one", Unclaimed: true})
	id2, _ := alice.Add(ctx, service.AddRequest{Prefix: "app", Title: "two", Unclaimed: true})
	if _, err := alice.Set(ctx, id1, "depends", id2, 0); err != nil {
		t.Fatalf("set 1: %v", err)
	}
	if _, err := alice.Set(ctx, id2, "depends", id1, 0); err != nil {
		t.Fatalf("set 2: %v", err)
	}

	svc := openSvc(t, bare, "auditor")
	findings, err := Store(context.Background(), svc.Store)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	found := false
	for _, f := range findings {
		if f.Kind == "dependency-cycle" {
			found = true
		}
	}
	if !found {
		for _, f := range findings {
			t.Logf("finding: %s", f)
		}
		t.Fatal("no dependency-cycle finding")
	}
}

func TestValidateDetectsDeletionAroundTheTool(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := openSvc(t, bare, "alice")
	id, _ := alice.Add(ctx, service.AddRequest{Prefix: "app", Title: "with comment", Unclaimed: true})
	commentPath, err := alice.Comment(ctx, id, "please keep me")
	if err != nil {
		t.Fatalf("comment: %v", err)
	}

	// Delete the comment with plain git, exactly like someone writing
	// around the tool would.
	clone := filepath.Join(t.TempDir(), "clone")
	runGit(t, filepath.Dir(clone), "clone", "--quiet", bare, clone)
	runGit(t, clone, "rm", "-q", commentPath)
	runGit(t, clone, "-c", "user.name=deleter", "-c", "user.email=d@invalid",
		"commit", "--quiet", "-m", "remove comment")
	runGit(t, clone, "push", "--quiet", "origin", "HEAD:refs/heads/tickets")

	svc := openSvc(t, bare, "auditor")
	findings, err := Store(context.Background(), svc.Store)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !kindsOf(findings, "app-1")["comment-deleted"] {
		for _, f := range findings {
			t.Logf("finding: %s", f)
		}
		t.Fatal("no comment-deleted finding")
	}
}
