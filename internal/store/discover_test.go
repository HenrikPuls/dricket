// SPDX-License-Identifier: GPL-3.0-or-later
package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func initDiscoveryStore(t *testing.T, branch string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "store")
	opts := initOpts(dir)
	opts.Branch = branch
	files := map[string]string{
		"dricket.toml": fmt.Sprintf("schema_version = 1\nbranch = %q\n", branch),
	}
	if err := Init(context.Background(), opts, files); err != nil {
		t.Fatalf("init: %v", err)
	}
	return dir
}

func TestDiscoverFromTicketBranch(t *testing.T) {
	for _, branch := range []string{"tickets", "work"} {
		t.Run(branch, func(t *testing.T) {
			dir := initDiscoveryStore(t, branch)
			gitOut(t, dir, "switch", "--quiet", branch)
			nested := filepath.Join(dir, "tickets", "app", "0000")
			if err := os.MkdirAll(nested, 0o750); err != nil {
				t.Fatalf("mkdir: %v", err)
			}

			got, ok, err := DiscoverFrom(context.Background(), nested)
			if err != nil || !ok {
				t.Fatalf("DiscoverFrom = %+v, %v, %v", got, ok, err)
			}
			if got.Dir != dir || got.Branch != branch || got.Remote != dir {
				t.Errorf("discovery = %+v; want root/remote %s and branch %s", got, dir, branch)
			}
		})
	}
}

func TestInspectRootRequiresActualRootAndStoreConfig(t *testing.T) {
	ctx := context.Background()
	plain := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(plain, 0o750); err != nil {
		t.Fatalf("mkdir plain repository: %v", err)
	}
	gitOut(t, plain, "init", "--quiet", "--initial-branch=main", ".")
	if _, ok, err := InspectRoot(ctx, plain); err != nil || ok {
		t.Fatalf("plain repository InspectRoot ok=%v err=%v; want false", ok, err)
	}

	dir := initDiscoveryStore(t, "tickets")
	nested := filepath.Join(dir, "tickets")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, ok, err := InspectRoot(ctx, nested); err != nil || ok {
		t.Errorf("nested InspectRoot ok=%v err=%v; want false", ok, err)
	}
}

func TestInspectRootUsesCommittedMainConfig(t *testing.T) {
	ctx := context.Background()
	dir := initDiscoveryStore(t, "tickets")
	config := filepath.Join(dir, "dricket.toml")
	if err := os.WriteFile(config, []byte("schema_version = 1\nbranch = \"wrong\"\n"), 0o600); err != nil {
		t.Fatalf("write dirty config: %v", err)
	}
	got, ok, err := InspectRoot(ctx, dir)
	if err != nil || !ok || got.Branch != "tickets" {
		t.Errorf("dirty main config discovery = %+v, %v, %v; want committed tickets branch", got, ok, err)
	}

	gitOut(t, dir, "restore", "dricket.toml")
	gitOut(t, dir, "switch", "--quiet", "tickets")
	if err := os.WriteFile(config, []byte("schema_version = 1\nbranch = \"wrong\"\n"), 0o600); err != nil {
		t.Fatalf("write ticket config: %v", err)
	}
	got, ok, err = InspectRoot(ctx, dir)
	if err != nil || !ok || got.Branch != "tickets" {
		t.Errorf("ticket config discovery = %+v, %v, %v; want committed main tickets branch", got, ok, err)
	}
}
