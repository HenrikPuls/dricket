// SPDX-License-Identifier: GPL-3.0-or-later
package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func commitVisibleFile(t *testing.T, dir, path, content, message string) string {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	runGit(t, dir, "add", "--", path)
	runGit(t, dir, "-c", "user.name=local-test", "-c", "user.email=local@invalid",
		"commit", "--quiet", "-m", message)
	return strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))
}

func TestLocalStoreUsesLinkedWorktreeAndSnapshot(t *testing.T) {
	ctx := context.Background()
	dir := initDiscoveryStore(t, "tickets")
	base := commitVisibleFile(t, dir, "base.txt", "before\n", "base")
	st := openStore(t, dir, "local", nil)

	if st.localDir != dir {
		t.Errorf("localDir = %q; want %q", st.localDir, dir)
	}
	if st.headRev != "HEAD" {
		t.Errorf("headRev = %q; want HEAD", st.headRev)
	}
	if got := strings.TrimSpace(runGit(t, st.Dir(), "rev-parse", "--abbrev-ref", "HEAD")); got != "HEAD" {
		t.Errorf("private worktree is attached to branch %q; want detached HEAD", got)
	}
	common := filepath.Clean(strings.TrimSpace(runGit(t, st.Dir(), "rev-parse", "--git-common-dir")))
	if common != filepath.Join(dir, ".git") {
		t.Errorf("common git dir = %q; want %q", common, filepath.Join(dir, ".git"))
	}

	if err := st.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if st.baseSHA != base {
		t.Errorf("baseSHA = %q; want %q", st.baseSHA, base)
	}
	commitVisibleFile(t, dir, "base.txt", "after\n", "advance public ref")
	got, err := st.ReadFile(ctx, "base.txt")
	if err != nil || got != "before\n" {
		t.Errorf("snapshot ReadFile = %q, %v; want before", got, err)
	}

	config, err := st.ConfigFile(ctx, "dricket.toml")
	if err != nil || !strings.Contains(string(config), `branch = "tickets"`) {
		t.Errorf("local main config = %q, %v", config, err)
	}
	missing, err := st.ConfigFile(ctx, "missing.toml")
	if err != nil || missing != nil {
		t.Errorf("missing config = %q, %v; want nil, nil", missing, err)
	}
}

func TestBarePathStoreKeepsPushMode(t *testing.T) {
	bare := setupBare(t, true)
	st := openStore(t, bare, "bare-user", nil)
	if st.localDir != "" || st.headRev != "FETCH_HEAD" {
		t.Errorf("bare store localDir=%q headRev=%q; want push mode", st.localDir, st.headRev)
	}
	if got := strings.TrimSpace(runGit(t, st.Dir(), "remote", "get-url", "origin")); got != bare {
		t.Errorf("origin = %q; want %q", got, bare)
	}
}
