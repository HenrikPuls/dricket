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

	"github.com/HenrikPuls/dricket/internal/trace"
)

// Workspace is a user-owned ticket checkout. Unlike the private store, it
// can contain unique uncommitted work and is never cleaned implicitly.
type Workspace struct {
	dir                   string
	beforeFinalResetCheck func() // deterministic race injection for adoption tests
}

// OpenWorkspace finds the enclosing Git working tree.
func OpenWorkspace(ctx context.Context, dir string) (*Workspace, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	out, err := runGitEnv(ctx, abs, Options{}, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, errors.New("not inside a git working tree")
	}
	root := strings.TrimSpace(out)
	if root == "" {
		return nil, errors.New("not inside a git working tree")
	}
	return &Workspace{dir: filepath.Clean(root)}, nil
}

// Dir returns the checkout root.
func (w *Workspace) Dir() string { return w.dir }

// RemoteBranch returns the checkout origin (or local directory) and branch.
func (w *Workspace) RemoteBranch(ctx context.Context) (string, string, error) {
	branch, err := w.git(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return "", "", errors.New("workspace is not on a local branch")
	}
	remote := w.dir
	if origin, err := w.git(ctx, "remote", "get-url", "origin"); err == nil && strings.TrimSpace(origin) != "" {
		remote = strings.TrimSpace(origin)
	}
	return remote, strings.TrimSpace(branch), nil
}

func (w *Workspace) git(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{"-c", "core.longpaths=true"}, args...)...) // #nosec G204 -- fixed executable, argument list, validated paths
	cmd.Dir = w.dir
	out, err := trace.CombinedOutput(cmd)
	return string(out), err
}

// Sync fast-forwards a clean checkout; local stores already share the ref.
func (w *Workspace) Sync(ctx context.Context) error {
	if err := w.requireClean(ctx); err != nil {
		return err
	}
	branch, err := w.git(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return errors.New("workspace is not on a local branch")
	}
	branch = strings.TrimSpace(branch)
	if err := checkRefName(ctx, branch); err != nil {
		return err
	}
	origin, err := w.git(ctx, "remote", "get-url", "origin")
	if err != nil || strings.TrimSpace(origin) == "" {
		return nil // local stores already share the ticket ref with the workspace
	}
	if _, err := w.git(ctx, "fetch", "--quiet", "origin", "refs/heads/"+branch); err != nil {
		return fmt.Errorf("fetch ticket branch: %w", err)
	}
	if _, err := w.git(ctx, "merge", "--ff-only", "--quiet", "FETCH_HEAD"); err != nil {
		return errors.New("ticket checkout cannot be fast-forwarded; local history was left unchanged")
	}
	return nil
}

// Restore discards local working-tree changes to one path without fetching.
func (w *Workspace) Restore(ctx context.Context, path string) error {
	clean, err := safePath(path)
	if err != nil {
		return err
	}
	if _, err := w.git(ctx, "restore", "--worktree", "--", clean); err != nil {
		return fmt.Errorf("restore %s: %w", clean, err)
	}
	return nil
}

// ReadPair returns the committed base and current working-tree content.
func (w *Workspace) ReadPair(ctx context.Context, path string) (base, edited string, err error) {
	clean, err := safePath(path)
	if err != nil {
		return "", "", err
	}
	base, err = w.git(ctx, "show", "HEAD:"+clean)
	if err != nil {
		return "", "", fmt.Errorf("read committed ticket: %w", err)
	}
	content, err := os.ReadFile(filepath.Join(w.dir, filepath.FromSlash(clean))) // #nosec G304 -- validated in-repo path
	if err != nil {
		return "", "", err
	}
	return base, string(content), nil
}

// Adopt moves a checkout to an accepted commit after proving that the exact
// published file is still its only dirty path.
func (w *Workspace) Adopt(ctx context.Context, commit, path, expected string) error {
	clean, err := safePath(path)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(filepath.Join(w.dir, filepath.FromSlash(clean))) // #nosec G304 -- validated in-repo path
	if err != nil {
		return err
	}
	if string(content) != expected {
		return errors.New("edited file changed while the commit was running")
	}
	if err := w.requireOnly(ctx, clean); err != nil {
		return err
	}
	branch, err := w.git(ctx, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return errors.New("workspace is not on a local branch")
	}
	branch = strings.TrimSpace(branch)
	historyRef := "refs/heads/" + branch
	origin, err := w.git(ctx, "remote", "get-url", "origin")
	local := err != nil || strings.TrimSpace(origin) == ""
	if !local {
		if _, err := w.git(ctx, "fetch", "--quiet", "origin", historyRef); err != nil {
			return fmt.Errorf("fetch accepted commit: %w", err)
		}
		historyRef = "FETCH_HEAD"
	} else {
		release, busy, lockErr := lockGitRepository(ctx, w.dir)
		if lockErr != nil {
			return lockErr
		}
		if busy {
			return errors.New("local adoption is busy with another publication")
		}
		defer release()
	}
	if _, err := w.git(ctx, "cat-file", "-e", commit+"^{commit}"); err != nil {
		return errors.New("accepted commit is not available in the store")
	}
	if _, err := w.git(ctx, "merge-base", "--is-ancestor", commit, historyRef); err != nil {
		return errors.New("accepted commit is not in the ticket history")
	}
	if _, err := w.git(ctx, "merge-base", "--is-ancestor", "HEAD", commit); err != nil {
		return errors.New("ticket checkout has unpushed or divergent commits; local history was left unchanged")
	}
	if w.beforeFinalResetCheck != nil {
		w.beforeFinalResetCheck()
	}
	if local {
		if _, err := w.git(ctx, "merge-base", "--is-ancestor", commit, historyRef); err != nil {
			return errors.New("accepted commit is no longer in the ticket history")
		}
		head, err := w.git(ctx, "rev-parse", "HEAD")
		if err != nil {
			return err
		}
		if strings.TrimSpace(head) != commit {
			return errors.New("a later local commit was accepted; checkout was left unchanged")
		}
	}
	content, err = os.ReadFile(filepath.Join(w.dir, filepath.FromSlash(clean))) // #nosec G304 -- validated in-repo path
	if err != nil {
		return err
	}
	if string(content) != expected {
		return errors.New("edited file changed while the commit was running")
	}
	if err := w.requireOnly(ctx, clean); err != nil {
		return err
	}
	if _, err := w.git(ctx, "reset", "--hard", "--quiet", commit); err != nil {
		return fmt.Errorf("adopt accepted commit: %w", err)
	}
	return nil
}

func (w *Workspace) requireClean(ctx context.Context) error {
	paths, err := w.dirtyPaths(ctx)
	if err != nil {
		return err
	}
	if len(paths) != 0 {
		return fmt.Errorf("ticket checkout has local changes (%s); commit or restore them first", strings.Join(paths, ", "))
	}
	return nil
}

func (w *Workspace) requireOnly(ctx context.Context, allowed string) error {
	paths, err := w.dirtyPaths(ctx)
	if err != nil {
		return err
	}
	for _, path := range paths {
		if path != allowed {
			return errors.New("ticket checkout has changes outside the committed ticket")
		}
	}
	return nil
}

func (w *Workspace) dirtyPaths(ctx context.Context) ([]string, error) {
	out, err := w.git(ctx, "status", "--porcelain=v1", "-z", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	parts := strings.Split(out, "\x00")
	paths := make([]string, 0, len(parts))
	for i := 0; i < len(parts); i++ {
		entry := parts[i]
		if entry == "" {
			continue
		}
		if len(entry) < 4 {
			return nil, errors.New("cannot parse git status")
		}
		path := strings.ReplaceAll(entry[3:], "\\", "/")
		paths = append(paths, path)
		if entry[0] == 'R' || entry[0] == 'C' {
			i++
		}
	}
	return paths, nil
}
