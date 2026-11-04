// SPDX-License-Identifier: GPL-3.0-or-later
package validate

import (
	"context"
	"testing"

	"github.com/HenrikPuls/dricket/internal/service"
	"github.com/HenrikPuls/dricket/internal/ticket"
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
	if _, err := bob.SetMany(ctx, id, []service.FieldValue{{Key: "x-persona", Value: "ben"}}, nil, true); err != nil {
		t.Fatalf("set --claim: %v", err)
	}
	if _, err := bob.Move(ctx, id, ticket.StageReview, service.MoveOptions{Claim: true, Kind: ticket.KindHuman}); err != nil {
		t.Fatalf("move --claim: %v", err)
	}

	st := alice.Store
	findings, err := Store(ctx, st)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	for _, f := range findings {
		if !f.Warning {
			t.Errorf("finding: %s", f.String())
		}
	}
}
