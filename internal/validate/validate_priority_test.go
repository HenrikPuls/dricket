// SPDX-License-Identifier: GPL-3.0-or-later
package validate

import (
	"context"
	"testing"

	"github.com/lariscus/dricket/internal/service"
	"github.com/lariscus/dricket/internal/ticket"
)

func TestValidatePriorities(t *testing.T) {
	bare := setupBare(t)
	svc := openSvc(t, bare, "alice")
	ctx := context.Background()

	// Fresh tickets carry the default priority: no findings.
	if _, err := svc.Add(ctx, service.AddRequest{Prefix: "app", Title: "fine", Unclaimed: true}); err != nil {
		t.Fatalf("add: %v", err)
	}

	// A tampered priority value is an error.
	rewrite(t, bare, "app-1", func(tk *ticket.Ticket) {
		tk.Priority = "apocalyptic"
	})

	findings, err := Store(ctx, svc.Store)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !kindsOf(findings, "app-1")["priority-invalid"] {
		for _, f := range findings {
			t.Logf("finding: %s", f)
		}
		t.Fatal("no priority-invalid finding")
	}
}

func TestValidateWarnsOnUnsetPriority(t *testing.T) {
	bare := setupBare(t)
	svc := openSvc(t, bare, "alice")
	ctx := context.Background()
	if _, err := svc.Add(ctx, service.AddRequest{Prefix: "app", Title: "no prio", Unclaimed: true}); err != nil {
		t.Fatalf("add: %v", err)
	}
	rewrite(t, bare, "app-1", func(tk *ticket.Ticket) {
		tk.Priority = ""
		tk.Rev++ // write like the tool would, so only the priority is flagged
	})

	findings, err := Store(ctx, svc.Store)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !kindsOf(findings, "app-1")["priority-unset"] {
		for _, f := range findings {
			t.Logf("finding: %s", f)
		}
		t.Fatal("no priority-unset warning")
	}
	if HasErrors(findings) {
		t.Error("an unset priority is a warning, not an error")
	}
}
