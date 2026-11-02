// SPDX-License-Identifier: GPL-3.0-or-later
package store

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/HenrikPuls/dricket/internal/profile"
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

// TestConfigFileAndFetchHeadIntegrity checks that reading the config from
// main does not disturb FETCH_HEAD, which every ticket read relies on.
func TestConfigFileAndFetchHeadIntegrity(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	bare := filepath.Join(base, "bare.git")
	cfgRunGit(t, base, "init", "--quiet", "--bare", "-b", "main", bare)

	seed := filepath.Join(base, "seed")
	cfgRunGit(t, base, "clone", "--quiet", bare, seed)
	cfgRunGit(t, seed, "checkout", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(seed, "dricket.toml"), []byte("min_version = \"0.2.0\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfgRunGit(t, seed, "add", "dricket.toml")
	cfgRunGit(t, seed, "-c", "user.name=a", "-c", "user.email=a@invalid", "commit", "-q", "-m", "config")
	cfgRunGit(t, seed, "push", "-q", "origin", "main")
	cfgRunGit(t, seed, "checkout", "-q", "--orphan", "tickets")
	cfgRunGit(t, seed, "rm", "-rf", "--ignore-unmatch", "-q", ".")
	if err := os.MkdirAll(filepath.Join(seed, "tickets"), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(seed, "tickets", "README"), []byte("tickets live here\n"), 0o600); err != nil {
		t.Fatalf("write seed ticket tree: %v", err)
	}
	cfgRunGit(t, seed, "add", "tickets/README")
	cfgRunGit(t, seed, "-c", "user.name=a", "-c", "user.email=a@invalid", "commit", "-q", "-m", "tickets seed")
	cfgRunGit(t, seed, "push", "-q", "origin", "tickets")

	st, err := Open(ctx, Options{
		Remote:  bare,
		Branch:  "tickets",
		DataDir: t.TempDir(),
		Profile: profile.Profile{Name: "t", OwnerID: "t", GitName: "t", GitEmail: "t@invalid"},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Refresh puts FETCH_HEAD at the tickets tip.
	if err := st.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if ok, _ := st.Has(ctx, "tickets/README"); !ok {
		t.Fatal("tickets/README missing after refresh")
	}

	// Reading the config must not break the ticket reads.
	data, err := st.ConfigFile(ctx, "dricket.toml")
	if err != nil {
		t.Fatalf("ConfigFile: %v", err)
	}
	if string(data) != "min_version = \"0.2.0\"\n" {
		t.Fatalf("config = %q", data)
	}
	if ok, _ := st.Has(ctx, "tickets/README"); !ok {
		t.Fatal("tickets/README gone after config fetch (FETCH_HEAD clobbered)")
	}

	// A file absent on main means defaults, not an error.
	if data, err := st.ConfigFile(ctx, "prefixes.toml"); err != nil || data != nil {
		t.Fatalf("missing file = %q, %v; want nil, nil", data, err)
	}
}

// TestConfigFileWithoutMain verifies the defaults path when the remote has
// no main branch at all.
func TestConfigFileWithoutMain(t *testing.T) {
	ctx := context.Background()
	base := t.TempDir()
	bare := filepath.Join(base, "bare.git")
	cfgRunGit(t, base, "init", "--quiet", "--bare", "-b", "tickets", bare)

	st, err := Open(ctx, Options{
		Remote:  bare,
		Branch:  "tickets",
		DataDir: t.TempDir(),
		Profile: profile.Profile{Name: "t", OwnerID: "t", GitName: "t", GitEmail: "t@invalid"},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	data, err := st.ConfigFile(ctx, "dricket.toml")
	if err != nil || data != nil {
		t.Fatalf("no main branch: ConfigFile = %q, %v; want nil, nil", data, err)
	}
	if !strings.Contains(filepath.Base(st.Dir()), "tickets") {
		t.Logf("working copy at %s", st.Dir())
	}
}
