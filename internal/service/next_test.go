// SPDX-License-Identifier: GPL-3.0-or-later
package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/HenrikPuls/dricket/internal/store"
	"github.com/HenrikPuls/dricket/internal/ticket"
)

// doneStage marks a ticket done by writing around the tool (a human's
// release to done is stage v2; the stage value is what next reads).
func doneStage(t *testing.T, s *Service, id string) {
	t.Helper()
	path, err := PathFor(id)
	if err != nil {
		t.Fatalf("path: %v", err)
	}
	spec := store.WriteSpec{
		Message: "test: mark done",
		Mutate: func(w store.Worktree) error {
			tk, err := readWorktreeTicket(w, path)
			if err != nil {
				return err
			}
			tk.Stage = ticket.StageDone
			tk.Owner = ""
			tk.ClaimedAt = time.Time{}
			tk.LeaseUntil = time.Time{}
			tk.Rev++
			return w.WriteFile(path, tk.ToDoc().Render())
		},
	}
	if err := s.Store.Write(context.Background(), spec); err != nil {
		t.Fatalf("done stage %s: %v", id, err)
	}
}

func TestNextEligibility(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	base := time.Now().Truncate(time.Second)

	alice := open(t, bare, "alice")
	alice.Now = at(base)

	// app-1: open, unclaimed -> eligible.
	id1, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "first", Unclaimed: true})
	// app-2: claimed -> not eligible.
	_, _ = alice.Add(ctx, AddRequest{Prefix: "app", Title: "second"})
	// app-3: open but depends on app-2 (in-progress) -> not eligible.
	id3, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "blocked by second", Unclaimed: true})
	if _, err := alice.Set(ctx, id3, "depends", "app-2", 0); err != nil {
		t.Fatalf("set depends: %v", err)
	}
	// ops-1: other prefix, eligible but app sorts first.
	_, _ = alice.Add(ctx, AddRequest{Prefix: "ops", Title: "opsy", Unclaimed: true})

	res, err := alice.Next(ctx, NextOptions{})
	if err != nil {
		t.Fatalf("next: %v", err)
	}
	if res.ID != id1 {
		t.Fatalf("next = %s, want %s", res.ID, id1)
	}

	// Prefix filter reaches the ops ticket.
	res, err = alice.Next(ctx, NextOptions{Prefix: "ops"})
	if err != nil || res.ID != "ops-1" {
		t.Fatalf("next ops = %+v, %v", res, err)
	}

	// Once app-1 is claimed, app-3 stays blocked and ops-1 wins.
	if _, err := alice.Claim(ctx, id1, ticket.SizeM, ticket.KindAgent); err != nil {
		t.Fatalf("claim: %v", err)
	}
	res, err = alice.Next(ctx, NextOptions{})
	if err != nil || res.ID != "ops-1" {
		t.Fatalf("next after claim = %+v, %v", res, err)
	}

	// With the dependency done, app-3 becomes eligible and wins by number.
	doneStage(t, alice, "app-2")
	res, err = alice.Next(ctx, NextOptions{})
	if err != nil || res.ID != id3 {
		t.Fatalf("next with done dep = %+v, %v", res, err)
	}

	// An expired lease is a takeover candidate.
	viewer := open(t, bare, "viewer")
	viewer.Now = at(base.Add(10 * time.Hour)) // S lease of app-2 is long gone
	res, err = viewer.Next(ctx, NextOptions{})
	if err != nil {
		t.Fatalf("next expired: %v", err)
	}
	if res.ID != "app-1" && res.ID != "app-2" {
		t.Fatalf("next expired = %+v, want a takeover candidate", res)
	}

	// Nothing suitable in a prefix without tickets.
	if _, err := viewer.Next(ctx, NextOptions{Prefix: "nope"}); ExitCode(err) != 10 {
		t.Fatalf("next unknown prefix err = %v, code %d; want 10", err, ExitCode(err))
	}
}

func TestNextParentDoesNotBlockButDependencyDoes(t *testing.T) {
	stages := map[string]ticket.Stage{"app-1": ticket.StageOpen, "app-2": ticket.StageOpen, "app-3": ticket.StageOpen}
	child := ticket.Ticket{ID: "app-2", Stage: ticket.StageOpen, Parent: "app-1"}
	if !eligible(child, stages, ticket.DefaultStageSet(), time.Now()) {
		t.Fatal("open parent blocked its child")
	}
	dependent := ticket.Ticket{ID: "app-3", Stage: ticket.StageOpen, Parent: "app-1", Depends: []string{"app-1"}}
	if eligible(dependent, stages, ticket.DefaultStageSet(), time.Now()) {
		t.Fatal("unfinished dependency did not block")
	}
}

func TestNextClaim(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")

	id, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "mine now", Unclaimed: true})
	res, err := alice.Next(ctx, NextOptions{Claim: true, Size: ticket.SizeS})
	if err != nil {
		t.Fatalf("next --claim: %v", err)
	}
	if res.Claim == nil || res.Claim.Epoch != 1 || res.ID != id {
		t.Fatalf("next claim = %+v", res)
	}
	// The next call must skip the now-claimed ticket.
	if _, err := alice.Next(ctx, NextOptions{Claim: true}); ExitCode(err) != 10 {
		t.Fatalf("next after claim err = %v, code %d; want 10", err, ExitCode(err))
	}
}

func TestSearch(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")

	id1, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "Login error on empty password", Unclaimed: true})
	id2, _ := alice.Add(ctx, AddRequest{Prefix: "ops", Title: "Rotate the login certificates", Unclaimed: true})
	if _, err := alice.Comment(ctx, id1, "the SSO redirect loops endlessly"); err != nil {
		t.Fatalf("comment: %v", err)
	}

	hits, err := alice.Search(ctx, "login", "")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	ids := map[string]bool{}
	for _, h := range hits {
		ids[h.ID] = true
	}
	if !ids[id1] || !ids[id2] {
		t.Fatalf("search login hits = %+v", hits)
	}

	hits, err = alice.Search(ctx, "endlessly", "")
	if err != nil || len(hits) != 1 || hits[0].ID != id1 {
		t.Fatalf("search comment text = %+v, %v", hits, err)
	}
	if !strings.Contains(strings.ToLower(hits[0].Line), "loops endlessly") {
		t.Errorf("hit line = %q", hits[0].Line)
	}

	hits, err = alice.Search(ctx, "login", "ops")
	if err != nil || len(hits) != 1 || hits[0].ID != id2 {
		t.Fatalf("search with prefix = %+v, %v", hits, err)
	}

	hits, err = alice.Search(ctx, "nothing-matches-this", "")
	if err != nil || len(hits) != 0 {
		t.Fatalf("search miss = %+v, %v; want empty", hits, err)
	}
	if _, err := alice.Search(ctx, "  ", ""); ExitCode(err) != 14 {
		t.Fatalf("empty query err = %v, code %d; want 14", err, ExitCode(err))
	}
}
