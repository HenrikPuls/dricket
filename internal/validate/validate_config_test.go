// SPDX-License-Identifier: GPL-3.0-or-later
package validate

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/HenrikPuls/dricket/internal/service"
	"github.com/HenrikPuls/dricket/internal/ticket"
)

func cfgRunGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...) // #nosec G204 -- test scaffolding
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
}

// cfgPushMain writes a file onto the protected main branch of the bare
// ticket store, like an admin pushing a config change.
func cfgPushMain(t *testing.T, bare, name, content string) {
	t.Helper()
	base := t.TempDir()
	clone := filepath.Join(base, "clone")
	cfgRunGit(t, base, "clone", "--quiet", bare, clone)
	cfgRunGit(t, clone, "checkout", "--orphan", "main")
	cfgRunGit(t, clone, "rm", "-rf", "--ignore-unmatch", "-q", ".")
	if err := os.WriteFile(filepath.Join(clone, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	cfgRunGit(t, clone, "add", name)
	cfgRunGit(t, clone, "-c", "user.name=admin", "-c", "user.email=admin@invalid",
		"commit", "--quiet", "-m", "config")
	cfgRunGit(t, clone, "push", "--quiet", "origin", "main:refs/heads/main")
}

func TestValidateLeaseMaxFromConfig(t *testing.T) {
	bare := setupBare(t)
	svc := openSvc(t, bare, "alice")
	ctx := context.Background()
	// L grants 24 h by default. The claim is made before any config
	// exists; afterwards the store tightens lease_max to 10 h, and
	// validate must flag the existing ticket.
	if _, err := svc.Add(ctx, service.AddRequest{Prefix: "app", Title: "big task", Size: ticket.SizeL}); err != nil {
		t.Fatalf("add: %v", err)
	}
	cfgPushMain(t, bare, "dricket.toml", "[limits]\nlease_max = \"10h\"\n")

	findings, err := Store(ctx, svc.Store)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !kindsOf(findings, "app-1")["lease-too-long"] {
		for _, f := range findings {
			t.Logf("finding: %s", f)
		}
		t.Fatal("no lease-too-long finding")
	}
}

func TestValidateDoesNotApplyTransitionPolicyToHistory(t *testing.T) {
	bare := setupBare(t)
	svc := openSvc(t, bare, "alice")
	ctx := context.Background()

	// A legal structural history: add-claim and release to review. A later
	// legacy role policy must not retroactively invalidate that history.
	if _, err := svc.Add(ctx, service.AddRequest{Prefix: "app", Title: "watch me"}); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := svc.Release(ctx, "app-1", "review", ""); err != nil {
		t.Fatalf("release: %v", err)
	}
	cfgPushMain(t, bare, "dricket.toml", `
[transitions.agent]
in-progress = ["open", "blocked"]
`)

	findings, err := Store(ctx, svc.Store)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if kindsOf(findings, "app-1")["illegal-transition"] {
		for _, f := range findings {
			t.Logf("finding: %s", f)
		}
		t.Fatal("legacy role policy invalidated structurally legal history")
	}
}

func TestValidateReportsBrokenConfig(t *testing.T) {
	bare := setupBare(t)
	cfgPushMain(t, bare, "dricket.toml", "not = [valid\n")

	svc := openSvc(t, bare, "auditor")
	findings, err := Store(context.Background(), svc.Store)
	if err != nil {
		t.Fatalf("validate: %v", err)
	}
	if !kindsOf(findings, "")["config-invalid"] {
		for _, f := range findings {
			t.Logf("finding: %s", f)
		}
		t.Fatal("no config-invalid finding")
	}
}
