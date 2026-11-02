// SPDX-License-Identifier: GPL-3.0-or-later

// Package store is dricket's only gateway to Git: it owns a private
// working copy outside any project directory, runs git as a subprocess
// (ADR-0002) and implements the write protocol of docs/CONCEPT.md - fetch,
// check preconditions on the fresh head, commit on top, push, and on a
// rejected push retry with exponential backoff and jitter. Never rebase.
package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/HenrikPuls/dricket/internal/profile"
)

// Retry starting values from docs/CONCEPT.md; they are options, not
// constants, because the concept marks them as estimates.
const (
	defaultMaxAttempts  = 8
	defaultBackoffBase  = 200 * time.Millisecond
	defaultBackoffCap   = 5 * time.Second
	defaultTotalTimeout = 30 * time.Second
)

// Errors callers distinguish.
var (
	// ErrNoBranch means the ticket branch does not exist on the remote yet.
	ErrNoBranch = errors.New("ticket branch does not exist on the remote")
	// ErrNotExist means the path is absent on the fetched head.
	ErrNotExist = errors.New("path does not exist on the head")
)

// Options configures a Store. Zero values get the concept's starting
// values.
type Options struct {
	Remote       string // remote URL or path
	Branch       string // ticket branch; empty means "tickets"
	DataDir      string // parent for the private working copy; empty = user cache dir
	Profile      profile.Profile
	MaxAttempts  int
	BackoffBase  time.Duration
	BackoffCap   time.Duration
	TotalTimeout time.Duration
}

func (o *Options) branch() string {
	if o.Branch == "" {
		return "tickets"
	}
	return o.Branch
}

func (o *Options) fillDefaults() {
	if o.MaxAttempts == 0 {
		o.MaxAttempts = defaultMaxAttempts
	}
	if o.BackoffBase == 0 {
		o.BackoffBase = defaultBackoffBase
	}
	if o.BackoffCap == 0 {
		o.BackoffCap = defaultBackoffCap
	}
	if o.TotalTimeout == 0 {
		o.TotalTimeout = defaultTotalTimeout
	}
}

// Store is a private working copy plus the git access around it. It is not
// safe for concurrent use; processes and profiles each get their own.
type Store struct {
	opts    Options
	dir     string
	sleep   func(time.Duration)
	hasRef  bool // remote branch existed at the last fetch
	rand    *rand.Rand
	rngOnce sync.Once
}

// Open prepares the private working copy for one profile against one
// remote branch. It never touches the current working directory.
func Open(ctx context.Context, opts Options) (*Store, error) {
	if opts.Remote == "" {
		return nil, errors.New("store: remote is required")
	}
	if err := opts.Profile.Validate(); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	opts.fillDefaults()

	if err := checkRefName(ctx, opts.branch()); err != nil {
		return nil, fmt.Errorf("store: branch %q: %w", opts.branch(), err)
	}
	if opts.DataDir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			return nil, fmt.Errorf("store: cache dir: %w", err)
		}
		opts.DataDir = filepath.Join(cache, "dricket")
	}
	dir := filepath.Join(opts.DataDir, slug(opts.Remote)+"-"+opts.branch()+"-"+slug(opts.Profile.OwnerID))
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("store: working copy dir: %w", err)
	}

	s := &Store{opts: opts, dir: dir, sleep: time.Sleep}
	if err := s.initOnce(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Dir is the private working copy path (diagnostics, tests).
func (s *Store) Dir() string { return s.dir }

// Close releases nothing today; it exists so callers get used to it.
func (s *Store) Close() error { return nil }

// initOnce makes the working copy usable: init (or reuse), set the remote,
// and make sure a local branch matching the ticket branch exists.
func (s *Store) initOnce(ctx context.Context) error {
	if _, _, err := s.git(ctx, "rev-parse", "--git-dir"); err == nil {
		if out, _, err := s.git(ctx, "remote", "get-url", "origin"); err != nil || strings.TrimSpace(out) != s.opts.Remote {
			if err == nil {
				return fmt.Errorf("store: working copy %s belongs to remote %q, not %q", s.dir, strings.TrimSpace(out), s.opts.Remote)
			}
			_, _, _ = s.git(ctx, "remote", "add", "origin", s.opts.Remote)
		}
	} else if _, _, err := s.git(ctx, "init", "--quiet", "--initial-branch="+s.opts.branch()); err != nil {
		return fmt.Errorf("store: init: %w", err)
	} else if _, _, err := s.git(ctx, "remote", "add", "origin", s.opts.Remote); err != nil {
		return fmt.Errorf("store: remote add: %w", err)
	}
	if _, _, err := s.git(ctx, "symbolic-ref", "--quiet", "HEAD"); err != nil {
		// Unborn HEAD: point it at the ticket branch.
		if _, _, err := s.git(ctx, "symbolic-ref", "HEAD", "refs/heads/"+s.opts.branch()); err != nil {
			return fmt.Errorf("store: set HEAD: %w", err)
		}
	}
	return nil
}

// git runs one git command in the working copy. Arguments are a list; no
// shell is involved, and nothing from ticket content reaches them.
func (s *Store) git(ctx context.Context, args ...string) (string, string, error) {
	full := append([]string{"-C", s.dir, "-c", "core.autocrlf=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...) // #nosec G204 -- args are tool-built constants and validated paths (ADR-0002)
	cmd.Env = append(os.Environ(), s.opts.Profile.Env()...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := cmd.Run()
	return out.String(), errb.String(), err
}

// ---------------------------------------------------------------- reads --

// fetch refreshes FETCH_HEAD from the remote branch. Missing remote branch
// is not an error; it leaves the store without a head.
func (s *Store) fetch(ctx context.Context) error {
	_, stderr, err := s.git(ctx, "fetch", "--quiet", "origin", s.opts.branch())
	switch {
	case err == nil:
		s.hasRef = true
		return nil
	case strings.Contains(stderr, "couldn't find remote ref"):
		s.hasRef = false
		return nil
	default:
		return fmt.Errorf("fetch origin %s: %s", s.opts.branch(), firstLine(stderr))
	}
}

// refresh brings the working copy to the fetched head. After it, Has and
// ReadFile read the fresh remote state.
func (s *Store) refresh(ctx context.Context) error {
	if err := s.fetch(ctx); err != nil {
		return err
	}
	if !s.hasRef {
		return nil // empty remote branch; the working copy stays unborn
	}
	if _, _, err := s.git(ctx, "reset", "--hard", "--quiet", "FETCH_HEAD"); err != nil {
		return errors.New("reset to fetched head")
	}
	if _, _, err := s.git(ctx, "clean", "-fdq"); err != nil {
		return errors.New("clean working copy")
	}
	return nil
}

// Has reports whether a path exists on the current head.
func (s *Store) Has(ctx context.Context, path string) (bool, error) {
	spec := fmt.Sprintf("FETCH_HEAD:%s", path)
	if !s.hasRef {
		return false, nil
	}
	_, _, err := s.git(ctx, "cat-file", "-e", spec)
	if err == nil {
		return true, nil
	}
	return false, nil
}

// ReadFile returns a file from the current head.
func (s *Store) ReadFile(ctx context.Context, path string) (string, error) {
	if !s.hasRef {
		return "", fmt.Errorf("%s: %w", path, ErrNoBranch)
	}
	out, _, err := s.git(ctx, "show", fmt.Sprintf("FETCH_HEAD:%s", path))
	if err != nil {
		return "", fmt.Errorf("%s: %w", path, ErrNotExist)
	}
	return out, nil
}

// List returns all file paths under a prefix on the current head.
func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	if !s.hasRef {
		return nil, nil
	}
	args := []string{"ls-tree", "-r", "--name-only", "FETCH_HEAD"}
	if prefix != "" {
		args = append(args, "--", prefix)
	}
	out, _, err := s.git(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("ls-tree %s: %w", prefix, err)
	}
	var files []string
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

// -------------------------------------------------------------- helpers --

// checkRefName validates a branch name with git itself, as the concept
// prescribes for every ref that can carry untrusted input.
func checkRefName(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, "git", "check-ref-format", "--branch", name) // #nosec G204 -- constant tool, single validated argument
	if err := cmd.Run(); err != nil {
		return errors.New("not a valid branch name")
	}
	return nil
}

var unsafeChars = regexp.MustCompile(`[^a-z0-9]+`)

// slug derives a filesystem-safe directory name from a remote or owner id.
func slug(s string) string {
	s = strings.ToLower(s)
	parts := unsafeChars.Split(s, -1)
	var keep []string
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	if len(keep) == 0 {
		return "x"
	}
	joined := strings.Join(keep, "-")
	if len(joined) > 40 {
		joined = joined[:40]
	}
	return joined
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}
