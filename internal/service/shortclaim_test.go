// SPDX-License-Identifier: GPL-3.0-or-later
package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/HenrikPuls/dricket/internal/ticket"
)

// ------------------------------------------------------------- R-24 ----

// A short claim takes and releases the claim inside the one commit: the
// ticket stays unclaimed in every committed state, the epoch does not
// move, and the write is open to every kind without the human/admin
// exception of a plain put or move (ADR-0012).
func TestSetUnderShortClaim(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")
	bob := open(t, bare, "bob")
	id, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "short", Unclaimed: true})

	before := commitCount(t, bare)
	res, err := bob.SetMany(ctx, id, []FieldValue{{Key: "x-persona", Value: "ben"}}, nil, true)
	if err != nil {
		t.Fatalf("set --claim: %v", err)
	}
	if commitCount(t, bare) != before+1 || res.Rev != 1 {
		t.Errorf("one commit, rev 1: %+v, commits %d -> %d", res, before, commitCount(t, bare))
	}
	tk := mustTicket(t, bareShow(t, bare, app1))
	if tk.Owner != "" || tk.Epoch != 0 || !tk.LeaseUntil.IsZero() || tk.Rev != 1 {
		t.Errorf("the short claim left claim state behind: %+v", tk)
	}
	if v, _ := tk.Extension("x-persona"); v != "ben" {
		t.Errorf("x-persona = %q", v)
	}
	if !strings.Contains(tk.Body, "set x-persona (short claim)") {
		t.Errorf("log line missing the short-claim mark:\n%s", tk.Body)
	}
	raw, _ := json.Marshal(res)
	if !strings.Contains(string(raw), `"short_claim":true`) {
		t.Errorf("JSON = %s", raw)
	}

	// A ticket claimed by someone else is refused as ever (exit 11).
	carol := open(t, bare, "carol")
	if _, err := carol.Claim(ctx, id, ticket.SizeM, ticket.KindAgent); err != nil {
		t.Fatalf("claim: %v", err)
	}
	_, err = bob.SetMany(ctx, id, []FieldValue{{Key: "x-persona", Value: "paul"}}, nil, true)
	wantCode(t, "set --claim on a foreign claim", err, 11)
	// The claim holder needs no short claim: it holds a real one.
	_, err = carol.SetMany(ctx, id, []FieldValue{{Key: "x-persona", Value: "paul"}}, nil, true)
	wantCode(t, "set --claim on the own claim", err, 14)
	// A plain set by the claim holder keeps working and marks nothing.
	if _, err := carol.Set(ctx, id, "x-persona", "paul", 0); err != nil {
		t.Fatalf("plain set by the holder: %v", err)
	}
	if got := mustTicket(t, bareShow(t, bare, app1)).Body; strings.Contains(got, "paul (short claim)") {
		t.Errorf("plain set was marked as a short claim:\n%s", got)
	}
}

func TestMoveUnderShortClaim(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")
	bob := open(t, bare, "bob")
	id, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "drag", Unclaimed: true})

	// Without --claim an agent may not move an unclaimed ticket (exit 17);
	// under a short claim the same actor may, inside its transition table.
	_, err := bob.Move(ctx, id, ticket.StageBlocked, MoveOptions{})
	wantCode(t, "plain agent move", err, 17)
	res, err := bob.Move(ctx, id, ticket.StageBlocked, MoveOptions{Claim: true})
	if err != nil || res.Stage != "blocked" || res.From != "open" {
		t.Fatalf("agent move --claim = %+v, %v", res, err)
	}
	tk := mustTicket(t, bareShow(t, bare, app1))
	if tk.Owner != "" || tk.Epoch != 0 || tk.Stage != ticket.StageBlocked {
		t.Errorf("after agent move --claim: %+v", tk)
	}
	if !strings.Contains(tk.Body, "move app-1 open -> blocked (short claim)") {
		t.Errorf("log line missing the short-claim mark:\n%s", tk.Body)
	}
	raw, _ := json.Marshal(res)
	if !strings.Contains(string(raw), `"short_claim":true`) {
		t.Errorf("JSON = %s", raw)
	}

	// The caller's kind governs the transition policy, like a real claim:
	// the agent table does not reach review from blocked, the human may.
	_, err = bob.Move(ctx, id, ticket.StageReview, MoveOptions{Claim: true})
	wantCode(t, "agent move --claim outside its table", err, 14)
	if _, err := bob.Move(ctx, id, ticket.StageReview, MoveOptions{Claim: true, Kind: ticket.KindHuman}); err != nil {
		t.Fatalf("human move --claim: %v", err)
	}
	// An unclaimed ticket must not land in in-progress (lint), short claim or not.
	_, err = bob.Move(ctx, id, ticket.StageInProgress, MoveOptions{Claim: true, Kind: ticket.KindHuman})
	wantCode(t, "move --claim to in-progress", err, 14)
	// --claim and --force exclude each other.
	_, err = bob.Move(ctx, id, ticket.StageOpen, MoveOptions{Claim: true, Force: true, Kind: ticket.KindHuman})
	wantCode(t, "move --claim with --force", err, 14)
}

func TestPutUnderShortClaim(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")
	bob := open(t, bare, "bob")
	id, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "doc", Unclaimed: true})
	content := bareShow(t, bare, app1)
	edited := strings.Replace(content, "(no description)", "written under a short claim", 1)

	// An agent may put an unclaimed ticket only under --claim; the version
	// guard still applies because the caller holds no committed claim.
	_, err := bob.Put(ctx, id, edited, PutOptions{Claim: true})
	wantCode(t, "put --claim without if-version", err, 14)
	_, err = bob.Put(ctx, id, edited, PutOptions{Claim: true, IfVersion: ver(9)})
	wantCode(t, "put --claim with a stale if-version", err, 12)
	res, err := bob.Put(ctx, id, edited, PutOptions{Claim: true, IfVersion: ver(0)})
	if err != nil || res.Rev != 1 {
		t.Fatalf("agent put --claim = %+v, %v", res, err)
	}
	tk := mustTicket(t, bareShow(t, bare, app1))
	if tk.Owner != "" || tk.Epoch != 0 || !strings.Contains(tk.Body, "written under a short claim") {
		t.Errorf("after put --claim: %+v", tk)
	}
	// put never touches the document's log; the commit message carries the mark.
	if strings.Contains(tk.Body, "(short claim)") {
		t.Errorf("put rewrote the body log:\n%s", tk.Body)
	}
	if msg := runGit(t, bare, "log", "-1", "--format=%s", "tickets"); !strings.Contains(msg, "(short claim)") {
		t.Errorf("commit message = %q", msg)
	}
	raw, _ := json.Marshal(res)
	if !strings.Contains(string(raw), `"short_claim":true`) {
		t.Errorf("JSON = %s", raw)
	}

	_, err = bob.Put(ctx, id, edited, PutOptions{Claim: true, Force: true, IfVersion: ver(1)})
	wantCode(t, "put --claim with --force", err, 14)

	// Claimed by someone else: exit 11, unchanged.
	if _, err := alice.Claim(ctx, id, ticket.SizeM, ticket.KindAgent); err != nil {
		t.Fatalf("claim: %v", err)
	}
	_, err = bob.Put(ctx, id, edited, PutOptions{Claim: true, IfVersion: ver(2)})
	wantCode(t, "put --claim on a foreign claim", err, 11)
}
