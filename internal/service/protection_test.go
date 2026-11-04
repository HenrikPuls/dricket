// SPDX-License-Identifier: GPL-3.0-or-later
package service

import (
	"context"
	"strings"
	"testing"

	"github.com/HenrikPuls/dricket/internal/ticket"
)

func TestForceReleaseAdminOnly(t *testing.T) {
	bare := setupBare(t)
	pushMainFile(t, bare, "dricket.toml", "[security]\nadmins = [\"alice\"]\n")
	ctx := context.Background()

	alice := open(t, bare, "alice")
	bob := open(t, bare, "bob")

	id, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "dead claim"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := alice.Extend(ctx, id, "long work"); err != nil {
		t.Fatalf("extend: %v", err)
	}

	// A non-admin is refused with exit 17.
	if _, err := bob.ForceRelease(ctx, id, "dead", ticket.StageOpen); ExitCode(err) != 17 {
		t.Fatalf("bob force-release err = %v, code %d; want 17", err, ExitCode(err))
	}

	// A missing reason is refused even for admins.
	if _, err := alice.ForceRelease(ctx, id, "  ", ticket.StageOpen); ExitCode(err) != 14 {
		t.Fatalf("empty reason err = %v, code %d; want 14", err, ExitCode(err))
	}

	res, err := alice.ForceRelease(ctx, id, "runner died, lease expired", ticket.StageOpen)
	if err != nil {
		t.Fatalf("force-release: %v", err)
	}
	if !res.Released || res.Stage != "open" {
		t.Fatalf("result = %+v", res)
	}
	tk := mustTicket(t, bareShow(t, bare, "tickets/app/0000/app-1/ticket.md"))
	if tk.Owner != "" || tk.Stage != ticket.StageOpen || tk.Extensions != 0 {
		t.Errorf("ticket after force-release = %+v", tk)
	}
	// The epoch is a fencing token and stays.
	if tk.Epoch != 1 {
		t.Errorf("epoch = %d, want 1 (unchanged)", tk.Epoch)
	}
	if !strings.Contains(tk.Body, "force-release app-1 by alice: runner died") {
		t.Errorf("log line missing:\n%s", tk.Body)
	}

	// Force-releasing an already free ticket is idempotent.
	res, err = alice.ForceRelease(ctx, id, "again", ticket.StageOpen)
	if err != nil || res.Released {
		t.Fatalf("idempotent force-release = %+v, %v", res, err)
	}
}

func TestForceReleaseFailsClosedWithoutAdmins(t *testing.T) {
	bare := setupBare(t)
	// No dricket.toml at all: nobody is an admin.
	ctx := context.Background()
	alice := open(t, bare, "alice")
	id, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "x"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := alice.ForceRelease(ctx, id, "no admins configured", ticket.StageOpen); ExitCode(err) != 17 {
		t.Fatalf("force-release without admins err = %v, code %d; want 17", err, ExitCode(err))
	}
}

func TestQuotaBlocksAdd(t *testing.T) {
	bare := setupBare(t)
	pushMainFile(t, bare, "dricket.toml", "[security]\nquota_tickets_per_day = 2\n")
	ctx := context.Background()

	alice := open(t, bare, "alice")
	bob := open(t, bare, "bob")

	for i := 1; i <= 2; i++ {
		if _, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "ok"}); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}
	if _, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "one too many"}); ExitCode(err) != 18 {
		t.Fatalf("add 3 err = %v, code %d; want 18", err, ExitCode(err))
	}
	// The quota is per identity, not global.
	if _, err := bob.Add(ctx, AddRequest{Prefix: "app", Title: "bob is fine"}); err != nil {
		t.Fatalf("bob add: %v", err)
	}
}

func TestSecretScanRefusesWrite(t *testing.T) {
	bare := setupBare(t)
	pushMainFile(t, bare, "dricket.toml", "[security]\nsecret_scan = true\n")
	ctx := context.Background()
	alice := open(t, bare, "alice")

	if _, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "leak inside"}); err != nil {
		t.Fatalf("clean add: %v", err)
	}

	// A secret in a description refuses the add with exit 19.
	_, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "creds",
		Description: "use ghp_abcdefghijklmnopqrstuvwxyz012345 for the api"})
	if ExitCode(err) != 19 {
		t.Fatalf("add with secret err = %v, code %d; want 19", err, ExitCode(err))
	}
	if !strings.Contains(err.Error(), "github-token") || strings.Contains(err.Error(), "ghp_abc") {
		t.Errorf("error must name the pattern but not the secret: %v", err)
	}

	// Comments, notes and free fields are scanned too.
	if _, err := alice.Comment(ctx, "app-1", "AKIAIOSFODNN7EXAMPLE"); ExitCode(err) != 19 {
		t.Fatalf("comment with secret err = %v, code %d", err, ExitCode(err))
	}
	if _, err := alice.Note(ctx, "app-1", "-----BEGIN OPENSSH PRIVATE KEY-----"); ExitCode(err) != 19 {
		t.Fatalf("note with secret err = %v, code %d", err, ExitCode(err))
	}
	// A whole-ticket write is scanned too.
	content := bareShow(t, bare, app1)
	leak := strings.Replace(content, "branch: ", "branch: glpat-ABCDEFGHIJKLMNOPQRST", 1)
	if _, err := alice.Put(ctx, "app-1", leak, PutOptions{}); ExitCode(err) != 19 {
		t.Fatalf("put with secret err = %v, code %d", err, ExitCode(err))
	}

	// With the scan off the same write goes through.
	scanOff := setupBare(t)
	pushMainFile(t, scanOff, "dricket.toml", "[security]\nsecret_scan = false\n")
	off := open(t, scanOff, "alice")
	if _, err := off.Add(ctx, AddRequest{Prefix: "app", Title: "creds",
		Description: "use ghp_abcdefghijklmnopqrstuvwxyz012345 for the api"}); err != nil {
		t.Fatalf("scan disabled should pass: %v", err)
	}
}
