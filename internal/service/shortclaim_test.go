// SPDX-License-Identifier: GPL-3.0-or-later
package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lariscus/dricket/internal/ticket"
)

// ------------------------------------------------------------- R-24 ----

// A short claim takes and releases the claim inside the one commit: the
// ticket stays unclaimed in every committed state and the epoch does not
// move. Since ADR-0013 it is also the only way to write an unclaimed
// ticket: no write without a claim.
func TestPutUnderShortClaim(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")
	bob := open(t, bare, "bob")
	id, _ := alice.Add(ctx, AddRequest{Prefix: "app", Title: "doc", Unclaimed: true})
	content := bareShow(t, bare, app1)
	edited := strings.Replace(content, "(no description)", "written under a short claim", 1)

	// Without --claim nobody writes an unclaimed ticket, whatever the kind.
	_, err := bob.Put(ctx, id, edited, PutOptions{IfVersion: ver(0)})
	wantCode(t, "claim-free put on an unclaimed ticket", err, 17)
	// The version guard still applies because the caller holds no
	// committed claim.
	_, err = bob.Put(ctx, id, edited, PutOptions{Claim: true})
	wantCode(t, "put --claim without if-version", err, 14)
	_, err = bob.Put(ctx, id, edited, PutOptions{Claim: true, IfVersion: ver(9)})
	wantCode(t, "put --claim with a stale if-version", err, 12)
	res, err := bob.Put(ctx, id, edited, PutOptions{Claim: true, IfVersion: ver(0)})
	if err != nil || res.Rev != 1 {
		t.Fatalf("put --claim = %+v, %v", res, err)
	}
	tk := mustTicket(t, bareShow(t, bare, app1))
	if tk.Owner != "" || tk.Epoch != 0 || !tk.LeaseUntil.IsZero() || !strings.Contains(tk.Body, "written under a short claim") {
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

	// Claimed by someone else: exit 11, unchanged. Claimed by the caller:
	// a real claim needs no short one.
	if _, err := alice.Claim(ctx, id, ticket.SizeM, ticket.KindAgent); err != nil {
		t.Fatalf("claim: %v", err)
	}
	_, err = bob.Put(ctx, id, edited, PutOptions{Claim: true, IfVersion: ver(2)})
	wantCode(t, "put --claim on a foreign claim", err, 11)
	_, err = alice.Put(ctx, id, edited, PutOptions{Claim: true})
	wantCode(t, "put --claim on the own claim", err, 14)
}
