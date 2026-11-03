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
)

// Init sets up a ticket store the way `git init` sets up a repository:
// run against a directory, it creates a repository there with the scaffold
// files committed on main and an empty seed commit on the ticket branch, which
// stays checked out. The store is then used as remote#tickets. A remote URL
// instead of a directory is scaffolded by pushing both branches. Existing
// repositories and existing branches are refused - the scaffold never
// overwrites anything.
func Init(ctx context.Context, opts Options, files map[string]string) error {
	if opts.Remote == "" {
		return errors.New("init: remote is required")
	}
	if err := opts.Profile.Validate(); err != nil {
		return fmt.Errorf("init: %w", err)
	}
	if len(files) == 0 {
		return errors.New("init: no scaffold files given")
	}
	for name := range files {
		if name == "" || strings.ContainsAny(name, "/\\") || filepath.IsAbs(name) ||
			filepath.Clean(name) != name || strings.HasPrefix(name, "..") {
			return fmt.Errorf("init: unsafe scaffold file name %q", name)
		}
	}

	remote := opts.Remote
	isURL := strings.Contains(remote, "://") || strings.HasPrefix(remote, "git@")
	if isURL {
		return initRemote(ctx, opts, files, remote)
	}
	return initDir(ctx, opts, files, remote)
}

// initDir scaffolds a repository inside a local directory, like git init.
func initDir(ctx context.Context, opts Options, files map[string]string, dir string) error {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return fmt.Errorf("init: %s is already a git repository; refusing to scaffold", dir)
	}
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 {
		return fmt.Errorf("init: %s is not empty; refusing to scaffold", dir)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("init: %w", err)
	}
	run := func(args ...string) error {
		if out, err := runGitEnv(ctx, dir, opts, args...); err != nil {
			return fmt.Errorf("init: git %s: %s", args[0], firstLine(out+err.Error()))
		}
		return nil
	}
	if err := run("init", "--quiet", "--initial-branch=main", "."); err != nil {
		return err
	}
	if err := writeScaffold(dir, files); err != nil {
		return err
	}
	if err := run("add", "-A"); err != nil {
		return err
	}
	if err := run("-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "dricket: scaffold"); err != nil {
		return err
	}
	// Seed the ticket branch: an empty commit on top of an empty tree,
	// so clones of the store have a head to reset against.
	if err := run("symbolic-ref", "HEAD", "refs/heads/"+opts.branch()); err != nil {
		return err
	}
	if err := run("rm", "-rf", "--cached", "--ignore-unmatch", "-q", "."); err != nil {
		return err
	}
	if err := run("-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "-m", "dricket: seed the ticket branch"); err != nil {
		return err
	}
	if err := run("reset", "--quiet", "--hard"); err != nil {
		return err
	}
	for name := range files {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return fmt.Errorf("init: remove scaffold from ticket checkout: %w", err)
		}
	}
	return nil
}

// initRemote scaffolds a remote store by pushing both branches from a
// throwaway working tree.
func initRemote(ctx context.Context, opts Options, files map[string]string, remote string) error {
	out, err := runGitEnv(ctx, "", opts, "ls-remote", remote)
	if err != nil {
		return fmt.Errorf("init: %s is not reachable: %s", remote, firstLine(out+err.Error()))
	}
	for _, ref := range []string{"refs/heads/main", "refs/heads/" + opts.branch()} {
		// ls-remote separates the SHA and the ref with a tab.
		if strings.Contains(out, "\t"+ref) {
			return fmt.Errorf("init: %s already has %s; refusing to scaffold", remote, ref)
		}
	}
	work, err := os.MkdirTemp("", "dricket-init-")
	if err != nil {
		return fmt.Errorf("init: temp dir: %w", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	run := func(args ...string) error {
		if out, err := runGitEnv(ctx, work, opts, args...); err != nil {
			return fmt.Errorf("init: git %s: %s", args[0], firstLine(out+err.Error()))
		}
		return nil
	}
	if err := run("init", "--quiet", "--initial-branch=main", "."); err != nil {
		return err
	}
	if err := run("remote", "add", "origin", remote); err != nil {
		return err
	}
	if err := writeScaffold(work, files); err != nil {
		return err
	}
	if err := run("add", "-A"); err != nil {
		return err
	}
	if err := run("-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "dricket: scaffold"); err != nil {
		return err
	}
	if err := run("push", "--quiet", "origin", "main:refs/heads/main"); err != nil {
		return err
	}
	if err := run("symbolic-ref", "HEAD", "refs/heads/"+opts.branch()); err != nil {
		return err
	}
	if err := run("rm", "-rf", "--cached", "--ignore-unmatch", "-q", "."); err != nil {
		return err
	}
	if err := run("-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "-m", "dricket: seed the ticket branch"); err != nil {
		return err
	}
	return run("push", "--quiet", "origin", opts.branch()+":refs/heads/"+opts.branch())
}

// writeScaffold writes the scaffold files. Names were validated by the
// caller; only content is variable, and only via code, never via CLI
// input.
func writeScaffold(dir string, files map[string]string) error {
	for name, content := range files {
		full := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
			return fmt.Errorf("init: %w", err)
		}
		if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
			return fmt.Errorf("init: write %s: %w", name, err)
		}
	}
	return nil
}

// runGitEnv runs one git command in dir with the profile's identity
// environment. Arguments are a list; no shell is involved. autocrlf is
// pinned off so the scaffold lands byte-identical everywhere.
func runGitEnv(ctx context.Context, dir string, opts Options, args ...string) (string, error) {
	full := append([]string{"-c", "core.autocrlf=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...) // #nosec G204 -- tool-built constants, no user input in args
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), opts.Profile.Env("")...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
