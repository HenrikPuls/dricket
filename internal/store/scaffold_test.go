// SPDX-License-Identifier: GPL-3.0-or-later
package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lariscus/dricket/internal/profile"
)

func initOpts(dir string) Options {
	return Options{
		Remote:  dir,
		Branch:  "tickets",
		Profile: profile.Profile{Name: "i", OwnerID: "init", GitName: "init", GitEmail: "init@invalid"},
	}
}

func TestInitLocalDirLikeGitInit(t *testing.T) {
	ctx := context.Background()
	dir := filepath.Join(t.TempDir(), "store")

	files := map[string]string{
		"dricket.toml": "schema_version = 1\n",
		"README.md":    "# Tickets\n",
	}
	if err := Init(ctx, initOpts(dir), files); err != nil {
		t.Fatalf("init: %v", err)
	}

	if out := gitOut(t, dir, "symbolic-ref", "--quiet", "--short", "HEAD"); strings.TrimSpace(out) != "tickets" {
		t.Errorf("checked-out branch = %q; want tickets", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "dricket.toml")); !os.IsNotExist(err) {
		t.Errorf("dricket.toml is on the ticket working tree; stat err = %v", err)
	}
	if out := gitOut(t, dir, "show", "main:dricket.toml"); out != "schema_version = 1\n" {
		t.Errorf("main:dricket.toml = %q", out)
	}
	if out := gitOut(t, dir, "log", "--format=%s", "main"); !strings.Contains(out, "dricket: scaffold") {
		t.Errorf("main log = %q", out)
	}
	// The ticket branch exists with a seed commit.
	if out := gitOut(t, dir, "log", "--format=%s", "tickets"); !strings.Contains(out, "seed the ticket branch") {
		t.Errorf("tickets log = %q", out)
	}

	// A second init refuses: the directory is a repository now.
	if err := Init(ctx, initOpts(dir), files); err == nil || !strings.Contains(err.Error(), "already a git repository") {
		t.Fatalf("second init err = %v; want refusal", err)
	}

	// The tool can work with the store right away.
	st, err := Open(ctx, Options{
		Remote:  dir,
		Branch:  "tickets",
		DataDir: t.TempDir(),
		Profile: profile.Profile{Name: "u", OwnerID: "user", GitName: "u", GitEmail: "u@invalid"},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
}

func TestInitRefusesNonEmptyDir(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "some-file.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := Init(ctx, initOpts(dir), map[string]string{"a.md": "a"}); err == nil {
		t.Fatal("init into a non-empty directory must be refused")
	}
}

func TestInitRemoteURL(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	bare := filepath.Join(base, "bare.git")
	gitOut(t, base, "init", "--quiet", "--bare", "-b", "main", bare)

	if err := Init(ctx, initOpts("file://"+bare), map[string]string{"dricket.toml": "schema_version = 1\n"}); err != nil {
		t.Fatalf("init remote: %v", err)
	}
	if out := gitOut(t, bare, "show", "main:dricket.toml"); !strings.Contains(out, "schema_version") {
		t.Errorf("main:dricket.toml = %q", out)
	}
	if out := gitOut(t, bare, "rev-parse", "--verify", "-q", "tickets"); out == "" {
		t.Error("tickets branch missing after remote init")
	}
	// A second init refuses: the branches exist.
	if err := Init(ctx, initOpts("file://"+bare), map[string]string{"x.md": "x"}); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Fatalf("second remote init err = %v; want refusal", err)
	}
}

func gitOut(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGitEnv(context.Background(), dir, Options{Profile: profile.Profile{
		Name: "t", OwnerID: "t", GitName: "t", GitEmail: "t@invalid",
	}}, args...)
	if err != nil && !strings.Contains(out, "fatal:") {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
	return out
}
