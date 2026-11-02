// SPDX-License-Identifier: GPL-3.0-or-later
package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/HenrikPuls/dricket/internal/profile"
)

// runGit runs git outside any store; test scaffolding only.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...) // #nosec G204 -- test scaffolding with tool-built arguments
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
	return string(out)
}

// setupBare creates a bare repository whose ticket branch exists (one
// empty seed commit) and returns its path.
func setupBare(t *testing.T, seeded bool) string {
	t.Helper()
	base := t.TempDir()
	bare := filepath.Join(base, "bare.git")
	runGit(t, base, "init", "--quiet", "--bare", "-b", "tickets", bare)
	if seeded {
		seed := filepath.Join(base, "seed")
		runGit(t, base, "clone", "--quiet", bare, seed)
		runGit(t, seed, "-c", "user.name=seed", "-c", "user.email=seed@invalid",
			"commit", "--quiet", "--allow-empty", "-m", "seed")
		runGit(t, seed, "push", "--quiet", "origin", "HEAD:refs/heads/tickets")
	}
	return bare
}

// openStore opens a store with its own working copy directory.
func openStore(t *testing.T, remote, owner string, tune func(*Options)) *Store {
	t.Helper()
	opts := Options{
		Remote:  remote,
		Branch:  "tickets",
		DataDir: t.TempDir(),
		Profile: profile.Profile{
			Name:     owner,
			OwnerID:  owner,
			GitName:  "runner-" + owner,
			GitEmail: owner + "@dricket.invalid",
		},
	}
	if tune != nil {
		tune(&opts)
	}
	s, err := Open(context.Background(), opts)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func writeFileSpec(path, content string, check func(Head) error) WriteSpec {
	return WriteSpec{
		Paths:   []string{path},
		Message: "test: write " + path,
		Check:   check,
		Mutate: func(w Worktree) error {
			return w.WriteFile(path, content)
		},
	}
}

func bareFile(t *testing.T, bare, branch, path string) string {
	t.Helper()
	out := runGit(t, bare, "show", fmt.Sprintf("%s:%s", branch, path))
	return out
}

func bareHas(t *testing.T, bare, branch, path string) bool {
	t.Helper()
	cmd := exec.Command("git", "show", branch+":"+path) // #nosec G204 -- branch and path come from the test
	cmd.Dir = bare
	return cmd.Run() == nil
}

func TestOpenRejectsInvalidBranch(t *testing.T) {
	_, err := Open(context.Background(), Options{
		Remote:  setupBare(t, true),
		Branch:  "../evil",
		DataDir: t.TempDir(),
		Profile: profile.Profile{OwnerID: "a", GitName: "a", GitEmail: "a@x"},
	})
	if err == nil || !strings.Contains(err.Error(), "branch") {
		t.Fatalf("err = %v, want a branch name error", err)
	}
}

func TestWriteAndRead(t *testing.T) {
	bare := setupBare(t, true)
	s := openStore(t, bare, "alice", nil)

	path := "tickets/app/0000/app-1/ticket.md"
	spec := writeFileSpec(path, "content-one", func(h Head) error {
		if has, err := h.Has(path); err != nil {
			return err
		} else if has {
			return errors.New("path already taken")
		}
		return nil
	})
	if err := s.Write(context.Background(), spec); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := bareFile(t, bare, "tickets", path); got != "content-one" {
		t.Errorf("bare has %q", got)
	}

	// Reads through the store see the remote state.
	if err := s.refreshForRead(t); err != nil {
		t.Fatal(err)
	}
	if has, _ := s.Has(context.Background(), path); !has {
		t.Error("Has = false after write")
	}
	files, err := s.List(context.Background(), "tickets/")
	if err != nil || len(files) != 1 || files[0] != path {
		t.Errorf("List = %v, %v", files, err)
	}
	content, err := s.ReadFile(context.Background(), path)
	if err != nil || content != "content-one" {
		t.Errorf("ReadFile = %q, %v", content, err)
	}

	// The commit carries the profile identity.
	log := runGit(t, bare, "log", "-1", "--format=%an <%ae>", "tickets")
	if !strings.Contains(log, "runner-alice <alice@dricket.invalid>") {
		t.Errorf("author = %q", log)
	}
}

// refreshForRead is the test hook for the unexported refresh.
func (s *Store) refreshForRead(t *testing.T) error {
	t.Helper()
	return s.refresh(context.Background())
}

func TestWriteOnEmptyRemoteCreatesBranch(t *testing.T) {
	bare := setupBare(t, false) // no ticket branch at all
	s := openStore(t, bare, "alice", nil)

	path := "tickets/app/0000/app-1/ticket.md"
	if has, err := s.Has(context.Background(), path); has || err != nil {
		t.Fatalf("Has on empty remote = %t, %v", has, err)
	}
	if _, err := s.List(context.Background(), "tickets/"); err != nil {
		t.Fatalf("List on empty remote: %v", err)
	}
	err := s.Write(context.Background(), writeFileSpec(path, "first", nil))
	if err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := bareFile(t, bare, "tickets", path); got != "first" {
		t.Errorf("bare has %q", got)
	}
}

func TestPreconditionFailureLeavesRemoteUntouched(t *testing.T) {
	bare := setupBare(t, true)
	s := openStore(t, bare, "alice", nil)

	spec := WriteSpec{
		Paths:   []string{"tickets/a.txt"},
		Message: "must not land",
		Check: func(Head) error {
			return errors.New("claimed by someone else")
		},
		Mutate: func(w Worktree) error { return w.WriteFile("tickets/a.txt", "x") },
	}
	werr := s.Write(context.Background(), spec)
	if werr == nil {
		t.Fatal("write succeeded")
	}
	var we *WriteError
	if !errors.As(werr, &we) || we.Outcome != PreconditionFailed {
		t.Fatalf("err = %v, want precondition failure", werr)
	}
	if bareHas(t, bare, "tickets", "tickets/a.txt") {
		t.Error("remote changed despite precondition failure")
	}
}

func TestNonFastForwardRetriesAndSucceeds(t *testing.T) {
	bare := setupBare(t, true)
	s := openStore(t, bare, "alice", func(o *Options) {
		o.BackoffBase = time.Millisecond
		o.BackoffCap = 5 * time.Millisecond
	})
	rival := openStore(t, bare, "bob", nil)

	// Alice's precondition lets Bob win the race once: his push makes her
	// push a non-fast-forward, and she must succeed on the retry.
	spec := WriteSpec{
		Paths:   []string{"tickets/alice.txt"},
		Message: "alice after race",
		Check: func(h Head) error {
			if has, err := h.Has("tickets/bob.txt"); err != nil {
				return err
			} else if !has {
				// Bob has not pushed yet: let him, right now.
				bobSpec := writeFileSpec("tickets/bob.txt", "bob was here", nil)
				if err := rival.Write(context.Background(), bobSpec); err != nil {
					return err
				}
			}
			return nil
		},
		Mutate: func(w Worktree) error { return w.WriteFile("tickets/alice.txt", "alice content") },
	}
	if err := s.Write(context.Background(), spec); err != nil {
		t.Fatalf("write: %v", err)
	}
	if got := bareFile(t, bare, "tickets", "tickets/alice.txt"); got != "alice content" {
		t.Errorf("alice file = %q", got)
	}
	if got := bareFile(t, bare, "tickets", "tickets/bob.txt"); got != "bob was here" {
		t.Errorf("bob file = %q", got)
	}
}

// TestConcurrentClaimsKeepOneOwner is the permanent regression test for
// the claim protocol: many runners race for the same ticket path; exactly
// one push may win, everyone else must end with a precondition failure or
// a bounded retry result, and the remote must hold one file with one
// owner. This must stay green forever.
func TestConcurrentClaimsKeepOneOwner(t *testing.T) {
	bare := setupBare(t, true)
	path := "tickets/app/0000/app-7/ticket.md"

	const n = 8
	stores := make([]*Store, n)
	for i := range stores {
		stores[i] = openStore(t, bare, fmt.Sprintf("runner-%d", i), func(o *Options) {
			o.BackoffBase = time.Millisecond
			o.BackoffCap = 10 * time.Millisecond
			o.TotalTimeout = 30 * time.Second
		})
	}
	results := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int, s *Store) {
			defer wg.Done()
			owner := fmt.Sprintf("runner-%d", i)
			spec := WriteSpec{
				Paths:   []string{path},
				Message: "claim " + owner,
				Check: func(h Head) error {
					if has, err := h.Has(path); err != nil {
						return err
					} else if has {
						return fmt.Errorf("%s already taken", path)
					}
					return nil
				},
				Mutate: func(w Worktree) error {
					return w.WriteFile(path, "owner: "+owner+"\n")
				},
			}
			results <- s.Write(context.Background(), spec)
		}(i, stores[i])
	}
	wg.Wait()
	close(results)

	winners, precond, other := 0, 0, 0
	for err := range results {
		if err == nil {
			winners++
			continue
		}
		var we *WriteError
		if errors.As(err, &we) && we.Outcome == PreconditionFailed {
			precond++
			continue
		}
		other++
		t.Errorf("unexpected outcome: %v", err)
	}
	if winners != 1 || precond != n-1 || other != 0 {
		t.Fatalf("winners=%d precond=%d other=%d; want 1/%d/0", winners, precond, n-1, other)
	}
	if got := bareFile(t, bare, "tickets", path); !strings.HasPrefix(got, "owner: runner-") {
		t.Errorf("final file = %q", got)
	}
	// Exactly one claim commit besides the seed.
	count := runGit(t, bare, "rev-list", "--count", "tickets")
	if strings.TrimSpace(count) != "2" {
		t.Errorf("commits on tickets = %q, want 2", count)
	}
}

func TestConcurrentDistinctPathsAllLand(t *testing.T) {
	bare := setupBare(t, true)
	const n = 6
	stores := make([]*Store, n)
	for i := range stores {
		stores[i] = openStore(t, bare, fmt.Sprintf("w-%d", i), func(o *Options) {
			o.BackoffBase = time.Millisecond
			o.BackoffCap = 10 * time.Millisecond
		})
	}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int, s *Store) {
			defer wg.Done()
			path := fmt.Sprintf("tickets/app/0000/app-%d/ticket.md", i+1)
			errs <- s.Write(context.Background(), writeFileSpec(path, fmt.Sprintf("file %d", i), nil))
		}(i, stores[i])
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	count := runGit(t, bare, "rev-list", "--count", "tickets")
	if strings.TrimSpace(count) != fmt.Sprint(n+1) {
		t.Errorf("commits = %q, want %d", count, n+1)
	}
}

func TestUnreachableRemoteFailsFast(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.git")
	s := openStore(t, missing, "alice", func(o *Options) {
		o.BackoffBase = time.Millisecond
		o.TotalTimeout = 10 * time.Second
	})
	err := s.Write(context.Background(), writeFileSpec("tickets/x.txt", "x", nil))
	var we *WriteError
	if !errors.As(err, &we) || we.Outcome != PushFailed {
		t.Fatalf("err = %v, want push failure", err)
	}
}

func TestUnsafePathsAreRejected(t *testing.T) {
	bare := setupBare(t, true)
	s := openStore(t, bare, "alice", nil)
	for _, p := range []string{"../escape.txt", "/abs.txt", "a/../../b", "", "bad\x00name", "c:d.txt"} {
		spec := WriteSpec{
			Paths:   []string{p},
			Message: "escape",
			Mutate: func(w Worktree) error {
				return w.WriteFile(p, "x")
			},
		}
		werr := s.Write(context.Background(), spec)
		var we *WriteError
		if !errors.As(werr, &we) || we.Outcome != MutationFailed {
			t.Errorf("path %q: err = %v, want mutation failure", p, werr)
		}
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(bare), "escape.txt")); !os.IsNotExist(err) {
		t.Error("a file escaped the working copy")
	}
}

func TestSafePath(t *testing.T) {
	ok := map[string]string{
		"tickets/app/0000/app-1/ticket.md": "tickets/app/0000/app-1/ticket.md",
		"a/b.txt":                          "a/b.txt",
		" plain.txt ":                      "plain.txt",
	}
	for in, want := range ok {
		got, err := safePath(in)
		if err != nil || got != want {
			t.Errorf("safePath(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"..", "a/../b", "/x", "", "a//b", "a/./b"} {
		if got, err := safePath(in); err == nil {
			t.Errorf("safePath(%q) = %q, want an error", in, got)
		}
	}
	// Backslashes are normalised, so a Windows-style input stays inside
	// the tree instead of becoming an escape hatch.
	if got, err := safePath(`a\b.txt`); err != nil || got != "a/b.txt" {
		t.Errorf(`safePath("a\\b.txt") = %q, %v; want "a/b.txt"`, got, err)
	}
}

func TestRetryablePush(t *testing.T) {
	cases := map[string]bool{
		" ! [rejected]        HEAD -> tickets (non-fast-forward)":                                                          true,
		"hint: Updates were rejected because the tip of your current branch is behind\n ! [remote rejected] (fetch first)": true,
		"remote: error: cannot lock ref 'refs/heads/tickets': is at aaa but expected bbb":                                  true,
		"remote: error: branch protection":                                                                                 false,
		"fatal: Authentication failed":                                                                                     false,
		"remote: pre-receive hook declined":                                                                                false,
		"":                                                                                                                 false,
	}
	for stderr, want := range cases {
		if got := retryablePush(stderr); got != want {
			t.Errorf("retryablePush(%q) = %t, want %t", stderr, got, want)
		}
	}
}

func TestPrivateWorkingCopyOutsideCwd(t *testing.T) {
	bare := setupBare(t, true)
	s := openStore(t, bare, "alice", nil)
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(s.Dir()) {
		t.Errorf("working copy %q is not absolute", s.Dir())
	}
	if rel, err := filepath.Rel(wd, s.Dir()); err == nil && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && rel != ".." {
		t.Errorf("working copy %q is inside the package directory", s.Dir())
	}
}
