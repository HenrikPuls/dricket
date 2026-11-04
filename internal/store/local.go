// SPDX-License-Identifier: GPL-3.0-or-later

// Package store is dricket's only gateway to Git: it owns private working
// copies, runs Git as a subprocess (ADR-0002) and implements the shared write
// protocol with remote push or local ref CAS (ADR-0010), immutable snapshots,
// bounded retries with jitter and no rebase.
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

func localWorkingRepository(ctx context.Context, remote string) (string, bool) {
	if strings.Contains(remote, "://") || strings.HasPrefix(remote, "git@") {
		return "", false
	}
	abs, err := filepath.Abs(remote)
	if err != nil {
		return "", false
	}
	out, err := runGitEnv(ctx, abs, Options{}, "rev-parse", "--is-bare-repository")
	if err != nil || strings.TrimSpace(out) != "false" {
		return "", false
	}
	root, err := runGitEnv(ctx, abs, Options{}, "rev-parse", "--show-toplevel")
	if err != nil || strings.TrimSpace(root) == "" {
		return "", false
	}
	return filepath.Clean(strings.TrimSpace(root)), true
}

func (s *Store) initLocal(ctx context.Context) error {
	if out, _, err := s.git(ctx, "rev-parse", "--git-common-dir"); err == nil {
		common := strings.TrimSpace(out)
		if !filepath.IsAbs(common) {
			common = filepath.Join(s.dir, common)
		}
		selected, selectedErr := runGitEnv(ctx, s.localDir, s.opts, "rev-parse", "--git-common-dir")
		if selectedErr != nil {
			return fmt.Errorf("store: resolve local common Git dir: %w", selectedErr)
		}
		expected := strings.TrimSpace(selected)
		if !filepath.IsAbs(expected) {
			expected = filepath.Join(s.localDir, expected)
		}
		if !samePath(common, expected) {
			return fmt.Errorf("store: local working copy %s belongs to another repository", s.dir)
		}
		return nil
	}

	ref := "refs/heads/" + s.opts.branch()
	base := "HEAD"
	if _, err := runGitEnv(ctx, s.localDir, s.opts, "rev-parse", "--verify", ref+"^{commit}"); err == nil {
		base = ref
	}
	if _, err := runGitEnv(ctx, s.localDir, s.opts,
		"-c", "core.hooksPath="+s.hooksPath,
		"worktree", "add", "--quiet", "--detach", s.dir, base); err != nil {
		return fmt.Errorf("store: add local worktree: %w", err)
	}
	if base == "HEAD" {
		return s.resetLocalEmpty(ctx)
	}
	return nil
}

func (s *Store) refreshLocal(ctx context.Context) error {
	ref := "refs/heads/" + s.opts.branch()
	if err := s.captureLocalDirty(ctx, ref); err != nil {
		return err
	}
	out, _, err := s.git(ctx, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		s.hasRef = false
		s.baseSHA = ""
		return s.resetLocalEmpty(ctx)
	}
	s.baseSHA = strings.TrimSpace(out)
	s.hasRef = true
	if _, _, err := s.git(ctx, "reset", "--hard", "--quiet", s.baseSHA); err != nil {
		return errorsNew("reset to local head", err)
	}
	if _, _, err := s.git(ctx, "checkout", "--detach", "--quiet", "HEAD"); err != nil {
		return errorsNew("detach local worktree", err)
	}
	if _, _, err := s.git(ctx, "clean", "-fdq"); err != nil {
		return errorsNew("clean local private worktree", err)
	}
	return nil
}

func (s *Store) resetLocalEmpty(ctx context.Context) error {
	s.localAttempt++
	privateRef := fmt.Sprintf("refs/dricket/private/%s/%d", filepath.Base(s.dir), s.localAttempt)
	if _, _, err := s.git(ctx, "symbolic-ref", "HEAD", privateRef); err != nil {
		return errorsNew("set empty local private head", err)
	}
	if _, _, err := s.git(ctx, "read-tree", "--empty"); err != nil {
		return errorsNew("empty local private index", err)
	}
	if _, _, err := s.git(ctx, "clean", "-fdq"); err != nil {
		return errorsNew("clean local private worktree", err)
	}
	return nil
}

func errorsNew(action string, err error) error {
	return fmt.Errorf("%s: %w", action, err)
}

var (
	errCheckoutConflict = errors.New("local checkout changes would be overwritten")
	errLockBusy         = errors.New("local publication lock is busy")
)

func (s *Store) captureLocalDirty(ctx context.Context, ref string) error {
	s.localDirty = nil
	checkout, checkedOut, err := s.localTicketCheckout(ctx, ref)
	if err != nil || !checkedOut {
		return err
	}
	paths, err := (&Workspace{dir: checkout}).dirtyPaths(ctx)
	if err != nil {
		return fmt.Errorf("read local checkout status: %w", err)
	}
	s.localDirty = make(map[string]bool, len(paths))
	for _, path := range paths {
		s.localDirty[path] = true
	}
	return nil
}

func lockGitRepository(ctx context.Context, dir string) (func(), bool, error) {
	out, err := runGitEnv(ctx, dir, Options{}, "rev-parse", "--git-common-dir")
	if err != nil {
		return nil, false, fmt.Errorf("find local Git lock directory: %w", err)
	}
	common := strings.TrimSpace(out)
	if err := os.MkdirAll(common, 0o750); err != nil {
		return nil, false, err
	}
	file, err := os.OpenFile(filepath.Join(common, "dricket-publish.lock"), os.O_CREATE|os.O_RDWR, 0o600) // #nosec G304 -- fixed lock in the selected repository's Git directory
	if err != nil {
		return nil, false, err
	}
	if err := lockFile(file); err != nil {
		_ = file.Close()
		if errors.Is(err, errLockBusy) {
			return nil, true, nil
		}
		return nil, false, err
	}
	return func() { _ = file.Close() }, false, nil
}

func (s *Store) lockLocalPublication(ctx context.Context) (func(), bool, error) {
	return lockGitRepository(ctx, s.dir)
}

func (s *Store) publishLocal(ctx context.Context, candidate, base string, preserve bool) (bool, error) {
	release, busy, err := s.lockLocalPublication(ctx)
	if err != nil {
		return false, err
	}
	if busy {
		return true, errLockBusy
	}
	defer release()

	ref := "refs/heads/" + s.opts.branch()
	checkout, checkedOut, err := s.localTicketCheckout(ctx, ref)
	if err != nil {
		return false, err
	}
	if preserve || !checkedOut {
		_, stderr, err := s.git(ctx, "update-ref", ref, candidate, base)
		if err == nil {
			return false, nil
		}
		return localPublicationRace(stderr), fmt.Errorf("local ref update: %s", firstLine(stderr))
	}

	out, err := s.gitAt(ctx, checkout, s.opts,
		"-c", "core.hooksPath="+s.hooksPath,
		"merge", "--ff-only", "--quiet", candidate)
	if err == nil {
		return false, nil
	}
	if s.localDirtyConflict(ctx, candidate, base, out) {
		return false, fmt.Errorf("%w: %s", errCheckoutConflict, firstLine(out))
	}
	return localPublicationRace(out), fmt.Errorf("local checkout fast-forward: %s", firstLine(out))
}

func (s *Store) localDirtyConflict(ctx context.Context, candidate, base, output string) bool {
	lower := strings.ToLower(output)
	if !strings.Contains(lower, "local changes") && !strings.Contains(lower, "untracked working tree") {
		return false
	}
	out, _, err := s.git(ctx, "diff", "--name-only", base, candidate)
	if err != nil {
		return false
	}
	for _, path := range lines(out) {
		if s.localDirty[path] {
			return true
		}
	}
	return false
}

func (s *Store) localTicketCheckout(ctx context.Context, ref string) (string, bool, error) {
	out, _, err := s.git(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return "", false, fmt.Errorf("list worktrees: %w", err)
	}
	var current string
	for _, line := range strings.Split(out, "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			current = path
			continue
		}
		if line == "branch "+ref && current != "" {
			return filepath.Clean(current), true, nil
		}
	}
	return "", false, nil
}

func localPublicationRace(output string) bool {
	lower := strings.ToLower(output)
	for _, fragment := range []string{
		"non-fast-forward", "not possible to fast-forward", "cannot lock ref",
		"index.lock", "but expected", "untracked working tree files would be overwritten",
		"your local changes",
	} {
		if strings.Contains(lower, fragment) {
			return true
		}
	}
	return false
}

func (s *Store) reconcileLocal(ctx context.Context, candidate string) (bool, error) {
	ref := "refs/heads/" + s.opts.branch()
	head, _, err := s.git(ctx, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return false, nil
	}
	_, _, err = s.git(ctx, "merge-base", "--is-ancestor", candidate, strings.TrimSpace(head))
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

func (s *Store) configFileLocal(ctx context.Context, name string) ([]byte, error) {
	out, _, err := s.git(ctx, "show", "refs/heads/main:"+name)
	if err != nil {
		return nil, nil
	}
	return []byte(out), nil
}

func ensureEmptyHooks(dataDir string) (string, error) {
	dir := filepath.Join(dataDir, "empty-hooks")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("store: empty hooks dir: %w", err)
	}
	return dir, nil
}
