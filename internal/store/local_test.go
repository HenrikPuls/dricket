// SPDX-License-Identifier: GPL-3.0-or-later
package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

func TestLocalWriteWithMainCheckedOut(t *testing.T) {
	dir := initDiscoveryStore(t, "tickets")
	runGit(t, dir, "switch", "--quiet", "main")
	st := openStore(t, dir, "local-main", nil)
	path := "tickets/app/0000/app-1/ticket.md"
	if err := st.Write(context.Background(), writeFileSpec(path, "local content", nil)); err != nil {
		t.Fatalf("local write: %v", err)
	}
	if got := runGit(t, dir, "show", "tickets:"+path); got != "local content" {
		t.Errorf("ticket branch content = %q", got)
	}
	if got := strings.TrimSpace(runGit(t, dir, "symbolic-ref", "--short", "HEAD")); got != "main" {
		t.Errorf("visible branch = %q; want main", got)
	}
	if content, err := os.ReadFile(filepath.Join(dir, "dricket.toml")); err != nil || !strings.Contains(string(content), "schema_version") {
		t.Errorf("main checkout changed: %q, %v", content, err)
	}
}

func TestLocalConcurrentClaimsKeepOneOwner(t *testing.T) {
	dir := initDiscoveryStore(t, "tickets")
	path := "tickets/app/0000/app-7/ticket.md"
	const n = 4
	stores := make([]*Store, n)
	for i := range stores {
		stores[i] = openStore(t, dir, fmt.Sprintf("runner-%d", i), func(o *Options) {
			o.BackoffBase = time.Millisecond
			o.BackoffCap = 10 * time.Millisecond
		})
	}
	results := make(chan error, n)
	var wg sync.WaitGroup
	for i, st := range stores {
		wg.Add(1)
		go func(i int, s *Store) {
			defer wg.Done()
			owner := fmt.Sprintf("runner-%d", i)
			results <- s.Write(context.Background(), WriteSpec{
				Message: "claim " + owner,
				Check: func(h Head) error {
					has, err := h.Has(path)
					if err == nil && has {
						return errors.New("path already taken")
					}
					return err
				},
				Mutate: func(w Worktree) error { return w.WriteFile(path, "owner: "+owner+"\n") },
			})
		}(i, st)
	}
	wg.Wait()
	close(results)
	winners, losers := 0, 0
	for err := range results {
		if err == nil {
			winners++
			continue
		}
		var we *WriteError
		if errors.As(err, &we) && we.Outcome == PreconditionFailed {
			losers++
		} else {
			t.Errorf("unexpected local claim result: %v", err)
		}
	}
	if winners != 1 || losers != n-1 {
		t.Fatalf("winners=%d losers=%d; want 1/%d", winners, losers, n-1)
	}
	if got := runGit(t, dir, "rev-list", "--count", "refs/heads/tickets"); strings.TrimSpace(got) != "2" {
		t.Errorf("ticket commits = %q; want 2", got)
	}
}

func TestLocalConcurrentDistinctPathsAllLand(t *testing.T) {
	dir := initDiscoveryStore(t, "tickets")
	const n = 4
	stores := make([]*Store, n)
	for i := range stores {
		stores[i] = openStore(t, dir, fmt.Sprintf("writer-%d", i), func(o *Options) {
			o.BackoffBase = time.Millisecond
			o.BackoffCap = 10 * time.Millisecond
		})
	}
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i, st := range stores {
		wg.Add(1)
		go func(i int, s *Store) {
			defer wg.Done()
			path := fmt.Sprintf("tickets/app/0000/app-%d/ticket.md", i+1)
			errs <- s.Write(context.Background(), writeFileSpec(path, fmt.Sprintf("file %d", i), nil))
		}(i, st)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("concurrent local write: %v", err)
		}
	}
	if got := runGit(t, dir, "rev-list", "--count", "refs/heads/tickets"); strings.TrimSpace(got) != fmt.Sprint(n+1) {
		t.Errorf("ticket commits = %q; want %d", got, n+1)
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
