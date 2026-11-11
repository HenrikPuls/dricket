// SPDX-License-Identifier: GPL-3.0-or-later
package validate

import (
	"context"
	"strings"
	"testing"

	"github.com/lariscus/dricket/internal/service"
)

// History written under short claims replays clean: the owner never
// changed in any committed state, so the epoch rightly never moved
// (ADR-0012).
func TestShortClaimHistoryReplaysClean(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := openSvc(t, bare, "alice")
	bob := openSvc(t, bare, "bob")
	id, err := alice.Add(ctx, service.AddRequest{Prefix: "app", Title: "replay", Unclaimed: true})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	content := runGit(t, bare, "show", "tickets:tickets/app/0000/app-1/ticket.md")
	edited := strings.Replace(content, "(no description)", "under a short claim", 1)
	edited = strings.Replace(edited, "stage: open", "stage: review", 1)
	zero := 0
	if _, err := bob.Put(ctx, id, edited, service.PutOptions{Claim: true, IfVersion: &zero}); err != nil {
		t.Fatalf("put --claim: %v", err)
	}

	findings, err := Store(ctx, alice.Store)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, f := range findings {
		if !f.Warning {
			t.Errorf("finding: %s", f.String())
		}
	}
}
