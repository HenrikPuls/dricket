// SPDX-License-Identifier: GPL-3.0-or-later
package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lariscus/dricket/internal/ticket"
)

// pushMainFile writes a file onto the protected main branch of the bare
// repo, exactly like an admin pushing a config change would.
func pushMainFile(t *testing.T, bare, name, content string) {
	t.Helper()
	base := t.TempDir()
	clone := filepath.Join(base, "clone")
	runGit(t, base, "clone", "--quiet", bare, clone)
	runGit(t, clone, "checkout", "--orphan", "main")
	runGit(t, clone, "rm", "-rf", "--ignore-unmatch", "-q", ".")
	if err := os.WriteFile(filepath.Join(clone, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	runGit(t, clone, "add", name)
	runGit(t, clone, "-c", "user.name=admin", "-c", "user.email=admin@invalid",
		"commit", "--quiet", "-m", "config")
	runGit(t, clone, "push", "--quiet", "origin", "main:refs/heads/main")
}

func TestConfigLimitsApply(t *testing.T) {
	bare := setupBare(t)
	pushMainFile(t, bare, "dricket.toml", `
[limits]
max_extensions = 1
lease_max = "10h"
`)
	ctx := context.Background()
	alice := open(t, bare, "alice")

	// L normally leases 24 h; the config caps it at 10 h.
	id, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "capped", Size: ticket.SizeL})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	tk := mustTicket(t, bareShow(t, bare, "tickets/app/0000/app-1/ticket.md"))
	granted := tk.LeaseUntil.Sub(tk.ClaimedAt)
	if granted != 10*time.Hour {
		t.Fatalf("granted lease = %v, want 10h (clamped)", granted)
	}

	if _, err := alice.Extend(ctx, id, "one is allowed"); err != nil {
		t.Fatalf("extend 1: %v", err)
	}
	if _, err := alice.Extend(ctx, id, "two is too many"); ExitCode(err) != 14 {
		t.Fatalf("extend 2 err = %v, code %d; want 14", err, ExitCode(err))
	}
}

func TestConfigMinVersion(t *testing.T) {
	bare := setupBare(t)
	pushMainFile(t, bare, "dricket.toml", "min_version = \"99.0\"\n")
	ctx := context.Background()

	svc := open(t, bare, "alice")
	svc.ToolVersion = "0.1.0"
	if _, err := svc.Add(ctx, AddRequest{Prefix: "app", Title: "x"}); ExitCode(err) != 16 {
		t.Fatalf("add with old tool err = %v, code %d; want 16", err, ExitCode(err))
	}

	// The dev build skips the check.
	svcDev := open(t, bare, "bob")
	svcDev.ToolVersion = "dev"
	if _, err := svcDev.Add(ctx, AddRequest{Prefix: "app", Title: "y"}); err != nil {
		t.Fatalf("dev build should skip min_version: %v", err)
	}

	// A new-enough build passes.
	svcNew := open(t, bare, "carol")
	svcNew.ToolVersion = "99.0.1"
	if _, err := svcNew.Add(ctx, AddRequest{Prefix: "app", Title: "z"}); err != nil {
		t.Fatalf("new build should pass: %v", err)
	}
}

func TestConfigTransitions(t *testing.T) {
	bare := setupBare(t)
	// Agents may no longer claim from open; blocked stays reachable.
	pushMainFile(t, bare, "dricket.toml", `
[transitions.agent]
open = ["blocked"]
`)
	ctx := context.Background()
	alice := open(t, bare, "alice")

	id, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "agents blocked here", Unclaimed: true})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := alice.Claim(ctx, id, ticket.SizeM, ticket.KindAgent); ExitCode(err) != 14 {
		t.Fatalf("agent claim err = %v, code %d; want 14", err, ExitCode(err))
	}
	// A human is fixed by the concept and may claim anything.
	if _, err := alice.Claim(ctx, id, ticket.SizeM, ticket.KindHuman); err != nil {
		t.Fatalf("human claim: %v", err)
	}
}

func TestBrokenConfigBlocksWrites(t *testing.T) {
	bare := setupBare(t)
	pushMainFile(t, bare, "dricket.toml", "this_is = [not valid toml\n")
	ctx := context.Background()
	alice := open(t, bare, "alice")

	_, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "blocked"})
	if err == nil || ExitCode(err) != 1 || !strings.Contains(err.Error(), "dricket.toml") {
		t.Fatalf("add with broken config err = %v, code %d; want 1 with a dricket.toml hint", err, ExitCode(err))
	}
}

func TestReleaseRoleComesFromKind(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := open(t, bare, "alice")

	// A human claim may release to done (before stage v2 every release
	// was judged as an agent and done was unreachable).
	human, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "human work", Kind: ticket.KindHuman})
	if err != nil {
		t.Fatalf("add human: %v", err)
	}
	if _, err := alice.Release(ctx, human, ticket.StageDone, "checked by hand"); err != nil {
		t.Fatalf("human release to done: %v", err)
	}

	// An agent release to done stays forbidden.
	agent, err := alice.Add(ctx, AddRequest{Prefix: "app", Title: "agent work"})
	if err != nil {
		t.Fatalf("add agent: %v", err)
	}
	if _, err := alice.Release(ctx, agent, ticket.StageDone, "sneaky"); ExitCode(err) != 14 {
		t.Fatalf("agent release to done err = %v, code %d; want 14", err, ExitCode(err))
	}
}
