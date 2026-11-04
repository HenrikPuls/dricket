// SPDX-License-Identifier: GPL-3.0-or-later
package service

import (
	"context"
	"strings"
	"testing"
)

func TestPriorityDefaultsAndValidation(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")

	// Without a flag, the configured default (normal) is stored.
	id, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "plain"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	tk := mustTicket(t, bareShow(t, bare, "tickets/app/0000/app-1/ticket.md"))
	if tk.Priority != "normal" {
		t.Errorf("priority = %q, want normal", tk.Priority)
	}

	// An explicit flag is stored (case-normalised).
	if _, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "hot", Priority: "HIGH"}); err != nil {
		t.Fatalf("add high: %v", err)
	}
	tk = mustTicket(t, bareShow(t, bare, "tickets/app/0000/app-2/ticket.md"))
	if tk.Priority != "high" {
		t.Errorf("priority = %q, want high", tk.Priority)
	}

	// An unknown value is refused with exit 14.
	if _, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "x", Priority: "apocalyptic"}); ExitCode(err) != 14 {
		t.Fatalf("add bogus priority err = %v, code %d; want 14", err, ExitCode(err))
	}

	// put can change the priority within the configured set; an unknown
	// value is refused (the ticket is claimed, so this is a claimant put).
	putField(t, alice, bare, id, "priority", "critical")
	tk = mustTicket(t, bareShow(t, bare, "tickets/app/0000/app-1/ticket.md"))
	if tk.Priority != "critical" {
		t.Errorf("priority after put = %q, want critical", tk.Priority)
	}
	content := bareShow(t, bare, "tickets/app/0000/app-1/ticket.md")
	_, err = alice.Put(ctx, id, strings.Replace(content, "priority: critical", "priority: bogus", 1), PutOptions{})
	wantCode(t, "put bogus priority", err, 14)
}

func TestPriorityFromConfig(t *testing.T) {
	bare := setupBare(t)
	pushMainFile(t, bare, "dricket.toml", `
[priorities]
values = ["urgent", "normal"]
default = "urgent"
`)
	ctx := context.Background()
	alice := open(t, bare, "alice")

	// The custom default applies.
	id, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "custom set"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	tk := mustTicket(t, bareShow(t, bare, "tickets/app/0000/app-1/ticket.md"))
	if tk.Priority != "urgent" {
		t.Fatalf("priority = %q, want urgent", tk.Priority)
	}

	// The starting set no longer applies.
	if _, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "x", Priority: "high"}); ExitCode(err) != 14 {
		t.Fatalf("add high outside custom set err = %v, code %d; want 14", err, ExitCode(err))
	}
	// A configured value works, also through put.
	putField(t, alice, bare, id, "priority", "normal")
}

func TestPriorityVisibleInQueries(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")
	if _, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "visible", Priority: "critical", Unclaimed: true}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "low prio", Priority: "low", Unclaimed: true}); err != nil {
		t.Fatalf("add low: %v", err)
	}

	items, _, err := alice.List(ctx, ListFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 2 || items[0].Priority != "critical" || items[1].Priority != "low" {
		t.Fatalf("list priorities = %+v", items)
	}

	res, err := alice.Next(ctx, NextOptions{})
	if err != nil || res.Priority != "critical" {
		t.Fatalf("next = %+v, %v; want critical", res, err)
	}
	if res.Stage != "open" {
		t.Errorf("next stage = %q, want open (no --claim)", res.Stage)
	}
}

func TestNextOrdersByPriorityThenNumber(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")

	// The lowest number carries the lowest priority: the critical
	// ticket with the higher number must win.
	if _, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "routine", Priority: "low", Unclaimed: true}); err != nil {
		t.Fatalf("add 1: %v", err)
	}
	if _, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "urgent", Priority: "critical", Unclaimed: true}); err != nil {
		t.Fatalf("add 2: %v", err)
	}
	if _, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "normal", Priority: "normal", Unclaimed: true}); err != nil {
		t.Fatalf("add 3: %v", err)
	}

	res, err := alice.Next(ctx, NextOptions{})
	if err != nil || res.ID != "app-2" || res.Priority != "critical" {
		t.Fatalf("next = %+v, %v; want app-2 (critical first)", res, err)
	}

	// Within one priority rank the lowest number wins: demoting app-2
	// to normal puts it in a rank with app-3, and the lower number wins.
	putField(t, alice, bare, "app-2", "priority", "normal")
	res, err = alice.Next(ctx, NextOptions{})
	if err != nil || res.ID != "app-2" || res.Priority != "normal" {
		t.Fatalf("next after demote = %+v, %v; want app-2 (lowest number in the normal rank)", res, err)
	}
}
