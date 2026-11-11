// SPDX-License-Identifier: GPL-3.0-or-later
package service

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lariscus/dricket/internal/frontmatter"
	"github.com/lariscus/dricket/internal/profile"
	"github.com/lariscus/dricket/internal/store"
	"github.com/lariscus/dricket/internal/ticket"
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

func open(t *testing.T, bare, owner string) *Service {
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
	return New(st, owner)
}

func bareShow(t *testing.T, bare, path string) string {
	t.Helper()
	return runGit(t, bare, "show", "tickets:"+path)
}

func mustTicket(t *testing.T, content string) ticket.Ticket {
	t.Helper()
	doc, err := frontmatter.Parse(content)
	if err != nil {
		t.Fatalf("parse ticket: %v", err)
	}
	tk, _, err := ticket.FromDoc(doc)
	if err != nil {
		t.Fatalf("ticket from doc: %v", err)
	}
	return tk
}

func TestLifecycleAddClaimRelease(t *testing.T) {
	bare := setupBare(t)
	s := open(t, bare, "alice")
	ctx := context.Background()

	id, err := s.Add(ctx, AddRequest{Prefix: "app", Title: "Login error", Unclaimed: true, Labels: []string{"bug", "auth"}})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if id != "app-1" {
		t.Fatalf("id = %q, want app-1", id)
	}

	res, err := s.Claim(ctx, id, ticket.SizeM, ticket.KindAgent)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if res.Epoch != 1 || res.LeaseUntil == "" {
		t.Fatalf("claim result = %+v", res)
	}

	content := bareShow(t, bare, "tickets/app/0000/app-1/ticket.md")
	tk := mustTicket(t, content)
	if tk.Owner != "alice" || tk.Stage != ticket.StageInProgress || tk.Rev != 1 || tk.Epoch != 1 {
		t.Errorf("ticket after claim = %+v", tk)
	}
	if !strings.Contains(tk.Body, "claim app-1") {
		t.Errorf("log line missing in body:\n%s", tk.Body)
	}

	// Idempotent claim: no commit, same epoch.
	before := runGit(t, bare, "rev-list", "--count", "tickets")
	again, err := s.Claim(ctx, id, ticket.SizeM, ticket.KindAgent)
	if err != nil || again.Epoch != 1 {
		t.Fatalf("idempotent claim = %+v, %v", again, err)
	}
	after := runGit(t, bare, "rev-list", "--count", "tickets")
	if strings.TrimSpace(before) != strings.TrimSpace(after) {
		t.Error("idempotent claim created a commit")
	}

	rel, err := s.Release(ctx, id, ticket.StageReview, "fix on dk/app-1")
	if err != nil {
		t.Fatalf("release: %v", err)
	}
	if rel.Stage != string(ticket.StageReview) {
		t.Fatalf("release = %+v", rel)
	}
	tk = mustTicket(t, bareShow(t, bare, "tickets/app/0000/app-1/ticket.md"))
	if tk.Owner != "" || tk.Stage != ticket.StageReview || tk.Rev != 2 || tk.Epoch != 1 {
		t.Errorf("ticket after release = %+v", tk)
	}
	if !strings.Contains(tk.Body, "## Result") || !strings.Contains(tk.Body, "fix on dk/app-1") {
		t.Errorf("result missing in body:\n%s", tk.Body)
	}

	// Idempotent release.
	if _, err := s.Release(ctx, id, ticket.StageReview, ""); err != nil {
		t.Fatalf("idempotent release: %v", err)
	}
}

func TestAddClaimsDirectlyAndNumbersGrow(t *testing.T) {
	bare := setupBare(t)
	s := open(t, bare, "alice")
	ctx := context.Background()
	for want := 1; want <= 3; want++ {
		id, err := s.Add(ctx, AddRequest{Prefix: "app", Title: fmt.Sprintf("ticket %d", want)})
		if err != nil {
			t.Fatalf("add %d: %v", want, err)
		}
		if id != fmt.Sprintf("app-%d", want) {
			t.Fatalf("id = %q, want app-%d", id, want)
		}
	}
	tk := mustTicket(t, bareShow(t, bare, "tickets/app/0000/app-1/ticket.md"))
	if tk.Owner != "alice" || tk.Stage != ticket.StageInProgress {
		t.Errorf("directly claimed ticket = %+v", tk)
	}
}

func TestClaimedByOtherGivesExit11(t *testing.T) {
	bare := setupBare(t)
	alice := open(t, bare, "alice")
	bob := open(t, bare, "bob")
	ctx := context.Background()

	id, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "x", Unclaimed: true})
	if _, err := alice.Claim(ctx, id, ticket.SizeM, ticket.KindAgent); err != nil {
		t.Fatalf("alice claim: %v", err)
	}
	_, err := bob.Claim(ctx, id, ticket.SizeM, ticket.KindAgent)
	if ExitCode(err) != 11 {
		t.Fatalf("bob claim err = %v, code %d; want exit 11", err, ExitCode(err))
	}
}

func TestClaimRaceOneWinner(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")
	id, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "race", Unclaimed: true})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	bob := open(t, bare, "bob")
	carol := open(t, bare, "carol")
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, s := range []*Service{bob, carol} {
		wg.Add(1)
		go func(s *Service) {
			defer wg.Done()
			_, err := s.Claim(ctx, id, ticket.SizeM, ticket.KindAgent)
			errs <- err
		}(s)
	}
	wg.Wait()
	close(errs)

	winners, losers := 0, 0
	for err := range errs {
		if err == nil {
			winners++
		} else if ExitCode(err) == 11 {
			losers++
		} else {
			t.Errorf("unexpected claim outcome: %v", err)
		}
	}
	if winners != 1 || losers != 1 {
		t.Fatalf("winners=%d losers=%d, want 1/1", winners, losers)
	}
	tk := mustTicket(t, bareShow(t, bare, "tickets/app/0000/app-1/ticket.md"))
	if tk.Owner != "bob" && tk.Owner != "carol" {
		t.Errorf("owner = %q", tk.Owner)
	}
}

func TestCommentNamingAndConcurrentComments(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")
	bob := open(t, bare, "bob")
	id, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "with comments", Unclaimed: true})

	f1, err := alice.Comment(ctx, id, "first look")
	if err != nil {
		t.Fatalf("comment 1: %v", err)
	}
	// v0.2 naming: UTC timestamp without colons plus the author, no
	// running number (ADR-0007).
	if !strings.Contains(f1, "/comments/") ||
		!strings.HasSuffix(f1, "-alice.md") || strings.Contains(filepath.Base(f1), ":") {
		t.Fatalf("file = %q; want timestamp-author naming", f1)
	}
	if content := bareShow(t, bare, f1); strings.Contains(content, "number:") {
		t.Errorf("comment front matter carries a number:\n%s", content)
	}

	// Concurrent comments: both land under distinct names.
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, s := range []*Service{alice, bob} {
		wg.Add(1)
		go func(s *Service) {
			defer wg.Done()
			_, err := s.Comment(ctx, id, "concurrent note")
			errs <- err
		}(s)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent comment: %v", err)
		}
	}
	files := runGit(t, bare, "ls-tree", "-r", "--name-only", "tickets")
	got := strings.Count(files, "/comments/")
	if got != 3 {
		t.Fatalf("comment files = %d, want 3:\n%s", got, files)
	}
}

func TestShowListLint(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")
	bob := open(t, bare, "bob")

	a1, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "one", Unclaimed: true})
	b1, _ := alice.Add(ctx, AddRequest{Prefix: "ops", Title: "two", Unclaimed: true})
	if _, err := alice.Claim(ctx, a1, ticket.SizeS, ticket.KindAgent); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := bob.Comment(ctx, b1, "needs triage"); err != nil {
		t.Fatalf("comment: %v", err)
	}

	content, err := alice.Show(ctx, a1)
	if err != nil || !strings.Contains(content, "id: app-1") {
		t.Fatalf("show = %q, %v", content, err)
	}
	if _, err := alice.Show(ctx, "app-99"); ExitCode(err) != 10 {
		t.Fatalf("show missing err = %v, code %d; want 10", err, ExitCode(err))
	}

	items, warns, err := alice.List(ctx, ListFilter{})
	if err != nil || len(items) != 2 || len(warns) != 0 {
		t.Fatalf("list = %+v, %v, %v", items, warns, err)
	}
	items, _, err = alice.List(ctx, ListFilter{Prefix: "app"})
	if err != nil || len(items) != 1 || items[0].ID != "app-1" {
		t.Fatalf("list by prefix = %+v, %v", items, err)
	}
	items, _, err = alice.List(ctx, ListFilter{Owner: "@me"})
	if err != nil || len(items) != 1 || items[0].Owner != "alice" {
		t.Fatalf("list @me = %+v, %v", items, err)
	}
	items, _, err = alice.List(ctx, ListFilter{Stage: "open"})
	if err != nil || len(items) != 1 || items[0].ID != "ops-1" {
		t.Fatalf("list by stage = %+v, %v", items, err)
	}

	results, err := alice.Lint(ctx, "")
	if err != nil || len(results) != 0 {
		t.Fatalf("lint all = %+v, %v", results, err)
	}
	results, err = alice.Lint(ctx, a1)
	if err != nil || len(results) != 0 {
		t.Fatalf("lint one = %+v, %v", results, err)
	}
}

func TestReleaseByNonOwnerGivesExit11(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")
	bob := open(t, bare, "bob")
	id, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "mine"})
	if _, err := bob.Release(ctx, id, ticket.StageReview, ""); ExitCode(err) != 11 {
		t.Fatalf("foreign release err = %v, code %d; want 11", err, ExitCode(err))
	}
}

func TestPathForShards(t *testing.T) {
	cases := map[string]string{
		"app-1":    "tickets/app/0000/app-1/ticket.md",
		"app-999":  "tickets/app/0000/app-999/ticket.md",
		"app-1000": "tickets/app/0001/app-1000/ticket.md",
		"app-6842": "tickets/app/0006/app-6842/ticket.md",
	}
	for id, want := range cases {
		got, err := PathFor(id)
		if err != nil || got != want {
			t.Errorf("PathFor(%q) = %q, %v; want %q", id, got, err, want)
		}
	}
	for _, bad := range []string{"", "app", "app-", "-1", "APP-1"} {
		if _, err := PathFor(bad); err == nil {
			t.Errorf("PathFor(%q) accepted", bad)
		}
	}
}

func TestExitCodeMapping(t *testing.T) {
	cases := map[string]int{
		KindNotFound: 10, KindClaimed: 11, KindConflict: 12, KindClaimLost: 13,
		KindInvalid: 14, KindNetwork: 15, KindTooOld: 16, KindError: 1,
	}
	for kind, want := range cases {
		if got := ExitCode(&Error{Kind: kind}); got != want {
			t.Errorf("ExitCode(%s) = %d, want %d", kind, got, want)
		}
	}
	// Store outcomes: push failure and timeout are network (15).
	for _, outcome := range []store.Outcome{store.PushFailed, store.Timeout} {
		err := &store.WriteError{Outcome: outcome}
		if got := ExitCode(err); got != 15 {
			t.Errorf("ExitCode(%v) = %d, want 15", outcome, got)
		}
	}
}

func TestTakeoverAfterLeaseExpiry(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")
	bob := open(t, bare, "bob")
	id, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "dead claim"})

	// Shrink the lease to the past as if the runner died long ago.
	path, _ := PathFor(id)
	spec := store.WriteSpec{
		Message: "test: expire lease",
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
			tk.LeaseUntil = time.Now().Add(-2 * time.Hour)
			return w.WriteFile(path, tk.ToDoc().Render())
		},
	}
	if err := alice.Store.Write(ctx, spec); err != nil {
		t.Fatalf("expire lease: %v", err)
	}

	res, err := bob.Claim(ctx, id, ticket.SizeM, ticket.KindAgent)
	if err != nil {
		t.Fatalf("takeover: %v", err)
	}
	if !res.Takeover || res.Epoch != 2 {
		t.Fatalf("takeover result = %+v", res)
	}
	tk := mustTicket(t, bareShow(t, bare, path))
	if tk.Owner != "bob" || tk.Epoch != 2 {
		t.Errorf("after takeover = %+v", tk)
	}
	if !strings.Contains(tk.Body, "takeover app-1") {
		t.Errorf("takeover log missing:\n%s", tk.Body)
	}

	// The fenced previous owner: alice's update must now fail with 13.
	_, err = alice.Release(ctx, id, ticket.StageReview, "")
	if ExitCode(err) != 11 {
		t.Fatalf("fenced release err = %v, code %d", err, ExitCode(err))
	}
}

var _ = os.Getenv
