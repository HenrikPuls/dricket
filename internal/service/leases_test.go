// SPDX-License-Identifier: GPL-3.0-or-later
package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/HenrikPuls/dricket/internal/ticket"
)

func at(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestExtendHappyLimitAndOwnership(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	base := time.Now().Truncate(time.Second)

	alice := open(t, bare, "alice")
	alice.Now = at(base)
	bob := open(t, bare, "bob")

	id, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "long task", Size: ticket.SizeM})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	res, err := alice.Extend(ctx, id, "tests take longer than planned")
	if err != nil {
		t.Fatalf("extend 1: %v", err)
	}
	if res.Extensions != 1 || res.Epoch != 1 || res.MaxExtend != MaxExtensions {
		t.Fatalf("extend result = %+v", res)
	}
	want := base.Add(LeaseM).UTC().Format(time.RFC3339)
	if res.LeaseUntil != want {
		t.Errorf("lease_until = %s, want %s", res.LeaseUntil, want)
	}

	// The limit is enforced on the fourth extend.
	for i := 2; i <= MaxExtensions; i++ {
		if _, err := alice.Extend(ctx, id, "still working"); err != nil {
			t.Fatalf("extend %d: %v", i, err)
		}
	}
	if _, err := alice.Extend(ctx, id, "one more"); ExitCode(err) != 14 {
		t.Fatalf("extend beyond limit err = %v, code %d; want 14", err, ExitCode(err))
	}

	// A foreign owner may not extend; an unclaimed ticket neither.
	if _, err := bob.Extend(ctx, id, "not mine"); ExitCode(err) != 11 {
		t.Fatalf("foreign extend err = %v, code %d; want 11", err, ExitCode(err))
	}
	other, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "free", Unclaimed: true})
	if _, err := alice.Extend(ctx, other, "no claim"); ExitCode(err) != 14 {
		t.Fatalf("unclaimed extend err = %v, code %d; want 14", err, ExitCode(err))
	}
	if _, err := alice.Extend(ctx, id, "  "); ExitCode(err) != 14 {
		t.Fatalf("empty reason err = %v, code %d; want 14", err, ExitCode(err))
	}

	tk := mustTicket(t, bareShow(t, bare, "tickets/app/0000/app-1/ticket.md"))
	if tk.Extensions != MaxExtensions || !strings.Contains(tk.Body, "extend app-1: tests take longer") {
		t.Errorf("ticket after extends = %+v body:\n%s", tk, tk.Body)
	}
}

func TestNoteKeepsClaim(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")
	bob := open(t, bare, "bob")

	id, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "note target"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	res, err := alice.Note(ctx, id, "root cause found")
	if err != nil {
		t.Fatalf("note: %v", err)
	}
	tk := mustTicket(t, bareShow(t, bare, "tickets/app/0000/app-1/ticket.md"))
	if tk.Owner != "alice" || tk.Rev != res.Rev || !strings.Contains(tk.Body, "note app-1: root cause found") {
		t.Errorf("ticket after note = %+v body:\n%s", tk, tk.Body)
	}
	if _, err := bob.Note(ctx, id, "intruder"); ExitCode(err) != 11 {
		t.Fatalf("foreign note err = %v, code %d; want 11", err, ExitCode(err))
	}
	free, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "free", Unclaimed: true})
	if _, err := alice.Note(ctx, free, "no claim"); ExitCode(err) != 14 {
		t.Fatalf("unclaimed note err = %v, code %d; want 14", err, ExitCode(err))
	}
}

func TestLeasesStatusesAndFilters(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	base := time.Now().Truncate(time.Second)

	alice := open(t, bare, "alice")
	alice.Now = at(base)
	small, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "small", Size: ticket.SizeS})
	if err != nil {
		t.Fatalf("add small: %v", err)
	}
	normal, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "normal", Size: ticket.SizeM})
	if err != nil {
		t.Fatalf("add normal: %v", err)
	}

	byStatus := func(infos []LeaseInfo) map[string]LeaseInfo {
		out := map[string]LeaseInfo{}
		for _, l := range infos {
			out[l.ID] = l
		}
		return out
	}

	viewer := open(t, bare, "viewer")
	viewer.Now = at(base.Add(110 * time.Minute)) // 10 min of the 2 h lease left
	infos, err := viewer.Leases(ctx, LeasesFilter{})
	if err != nil {
		t.Fatalf("leases: %v", err)
	}
	st := byStatus(infos)
	if st[small].Status != LeaseExpiring {
		t.Errorf("small = %s, want expiring", st[small].Status)
	}
	if st[normal].Status != LeaseOK {
		t.Errorf("normal = %s, want ok", st[normal].Status)
	}
	if !st[small].Quiet {
		t.Errorf("small should be quiet: %+v", st[small])
	}
	if HasExpired(infos) {
		t.Error("nothing expired yet")
	}
	filtered, _ := viewer.Leases(ctx, LeasesFilter{Expiring: true})
	if len(filtered) != 1 || filtered[0].ID != small {
		t.Errorf("expiring filter = %+v", filtered)
	}
	filtered, _ = viewer.Leases(ctx, LeasesFilter{Stale: true})
	if len(filtered) != 0 {
		t.Errorf("stale filter = %+v, want empty", filtered)
	}

	viewer.Now = at(base.Add(2*time.Hour + 30*time.Minute)) // past lease plus grace
	infos, err = viewer.Leases(ctx, LeasesFilter{})
	if err != nil {
		t.Fatalf("leases: %v", err)
	}
	st = byStatus(infos)
	if st[small].Status != LeaseExpired || st[normal].Status != LeaseOK {
		t.Errorf("statuses = %+v", st)
	}
	if !HasExpired(infos) {
		t.Error("expired not detected")
	}
	filtered, _ = viewer.Leases(ctx, LeasesFilter{Stale: true})
	if len(filtered) != 1 || filtered[0].ID != small {
		t.Errorf("stale filter = %+v", filtered)
	}
	if st[small].RemainingMin != 0 {
		t.Errorf("remaining = %d, want 0", st[small].RemainingMin)
	}
}

func TestListStaleFilter(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	base := time.Now().Truncate(time.Second)

	alice := open(t, bare, "alice")
	alice.Now = at(base)
	dead, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "dead claim", Size: ticket.SizeS})
	alive, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "alive claim", Size: ticket.SizeL})

	viewer := open(t, bare, "viewer")
	viewer.Now = at(base.Add(3 * time.Hour)) // S lease long gone, L fine

	items, _, err := viewer.List(ctx, ListFilter{Stale: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(items) != 1 || items[0].ID != dead {
		t.Fatalf("stale list = %+v, want only %s", items, dead)
	}
	all, _, _ := viewer.List(ctx, ListFilter{})
	if len(all) != 2 {
		t.Fatalf("unfiltered list = %d items, want 2", len(all))
	}
	_ = alive
}

func TestInboxMarker(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")
	bob := open(t, bare, "bob")

	id, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "watched", Unclaimed: true})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := bob.Comment(ctx, id, "first look"); err != nil {
		t.Fatalf("comment 1: %v", err)
	}
	if _, err := bob.Comment(ctx, id, "second look"); err != nil {
		t.Fatalf("comment 2: %v", err)
	}

	// First sight only sets the marker.
	first, err := alice.Inbox(ctx, id)
	if err != nil || !first.FirstSeen || len(first.NewComments) != 0 {
		t.Fatalf("first inbox = %+v, %v", first, err)
	}

	// Claim, note and a third comment: all three are new exactly once.
	if _, err := alice.Claim(ctx, id, ticket.SizeM, ticket.KindAgent); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if _, err := bob.Comment(ctx, id, "third look"); err != nil {
		t.Fatalf("comment 3: %v", err)
	}
	if _, err := alice.Note(ctx, id, "root cause found"); err != nil {
		t.Fatalf("note: %v", err)
	}

	second, err := alice.Inbox(ctx, id)
	if err != nil {
		t.Fatalf("second inbox: %v", err)
	}
	if second.FirstSeen || second.Rev != 2 {
		t.Fatalf("second inbox = %+v", second)
	}
	if len(second.NewLog) != 2 {
		t.Fatalf("new log = %v, want 2 entries", second.NewLog)
	}
	if !strings.Contains(strings.Join(second.NewLog, "\n"), "claim app-1") ||
		!strings.Contains(strings.Join(second.NewLog, "\n"), "note app-1: root cause found") {
		t.Errorf("new log = %v", second.NewLog)
	}
	if len(second.NewComments) != 1 || second.NewComments[0].Author != "bob" ||
		second.NewComments[0].Excerpt != "third look" {
		t.Fatalf("new comments = %+v", second.NewComments)
	}

	third, err := alice.Inbox(ctx, id)
	if err != nil || len(third.NewLog) != 0 || len(third.NewComments) != 0 {
		t.Fatalf("third inbox = %+v, %v; want nothing new", third, err)
	}

	// Another owner has their own marker.
	carol := open(t, bare, "carol")
	if res, err := carol.Inbox(ctx, id); err != nil || !res.FirstSeen {
		t.Fatalf("carol inbox = %+v, %v; want first sight", res, err)
	}

	// Unknown ticket is exit 10.
	if _, err := alice.Inbox(ctx, "app-99"); ExitCode(err) != 10 {
		t.Fatalf("inbox missing err = %v, code %d; want 10", err, ExitCode(err))
	}
}

// TestLastChangeFeedsQuiet exercises the store helper the quiet hint uses.
func TestLastChangeFeedsQuiet(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")
	id, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "x", Unclaimed: true})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	path, _ := PathFor(id)
	if err := alice.Store.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	ts, ok := alice.Store.LastChange(ctx, path)
	if !ok || ts.IsZero() {
		t.Fatalf("last change = %v, %t", ts, ok)
	}
	_, ok = alice.Store.LastChange(ctx, "tickets/app/0000/app-99/ticket.md")
	if ok {
		t.Error("unknown path reported a change")
	}
}
