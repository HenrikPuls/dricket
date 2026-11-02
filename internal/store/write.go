// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Head is read access to the fresh remote head, given to preconditions.
// It sees the state after the latest fetch, never a stale local branch.
type Head interface {
	// Has reports whether a path exists on the head.
	Has(path string) (bool, error)
	// ReadFile returns a file's content from the head.
	ReadFile(path string) (string, error)
	// List returns all file paths under a prefix.
	List(prefix string) ([]string, error)
}

// Worktree is write access to the private working copy, given to the
// mutation after all preconditions passed.
type Worktree interface {
	ReadFile(path string) (string, error)
	WriteFile(path string, content string) error
}

// WriteSpec is one write through the protocol: Check inspects the fresh
// head and refuses the write when a precondition fails; Mutate changes the
// working copy. Everything Mutate writes through the Worktree is staged,
// so dynamically allocated paths (ids, comment numbers) need no separate
// declaration.
type WriteSpec struct {
	Message string
	Check   func(Head) error
	Mutate  func(Worktree) error
}

// Outcome says how a write ended.
type Outcome int

// Write outcomes; the service layer maps them and its errors to the
// documented exit codes.
const (
	// Written means the push was accepted.
	Written Outcome = iota
	// PreconditionFailed means Check refused the write on the fresh head.
	PreconditionFailed
	// MutationFailed means Mutate could not apply the change.
	MutationFailed
	// PushFailed means the push failed permanently (rights, protection,
	// network) after exhausting what retries could fix.
	PushFailed
	// Timeout means the retry budget was exhausted without a decision.
	Timeout
)

func (o Outcome) String() string {
	switch o {
	case Written:
		return "written"
	case PreconditionFailed:
		return "precondition failed"
	case MutationFailed:
		return "mutation failed"
	case PushFailed:
		return "push failed"
	case Timeout:
		return "timeout"
	}
	return "unknown"
}

// WriteError carries the outcome of a failed write.
type WriteError struct {
	Outcome    Outcome
	Attempts   int
	PushReject int // pushes rejected as non-fast-forward before the end
	Err        error
	PushStderr string
}

func (e *WriteError) Error() string {
	switch {
	case e.Err != nil:
		return fmt.Sprintf("%s: %v", e.Outcome, e.Err)
	case e.PushStderr != "":
		return fmt.Sprintf("%s: %s", e.Outcome, firstLine(e.PushStderr))
	default:
		return e.Outcome.String()
	}
}

func (e *WriteError) Unwrap() error { return e.Err }

// Write runs the protocol of docs/CONCEPT.md: fetch, check preconditions
// on the fresh head, mutate, commit, push; on a rejected push back off and
// start over from a fresh fetch. Never rebase. A nil return means the push
// was accepted; every other end is a *WriteError.
func (s *Store) Write(ctx context.Context, spec WriteSpec) error {
	ctx, cancel := context.WithTimeout(ctx, s.opts.TotalTimeout)
	defer cancel()

	var werr *WriteError
	for attempt := 1; attempt <= s.opts.MaxAttempts; attempt++ {
		werr = &WriteError{Attempts: attempt, PushReject: attempt - 1}
		if err := s.refresh(ctx); err != nil {
			werr.Outcome = PushFailed
			werr.Err = err
			return werr
		}
		if spec.Check != nil {
			if err := spec.Check(s.head(ctx)); err != nil {
				werr.Outcome = PreconditionFailed
				werr.Err = err
				return werr
			}
		}
		wt := &worktreeAdapter{s: s}
		if spec.Mutate != nil {
			if err := spec.Mutate(wt); err != nil {
				werr.Outcome = MutationFailed
				werr.Err = err
				return werr
			}
		}
		if len(wt.written) == 0 {
			werr.Outcome = PreconditionFailed
			werr.Err = errors.New("nothing changed")
			return werr
		}
		addArgs := append([]string{"add", "--"}, wt.written...)
		if _, stderr, err := s.git(ctx, addArgs...); err != nil {
			werr.Outcome = MutationFailed
			werr.Err = fmt.Errorf("git add: %s", firstLine(stderr))
			return werr
		}
		_, stderr, err := s.git(ctx, "commit", "--quiet", "-m", spec.Message)
		if err != nil {
			if strings.Contains(stderr, "nothing to commit") {
				werr.Outcome = PreconditionFailed
				werr.Err = errors.New("nothing changed")
				return werr
			}
			werr.Outcome = MutationFailed
			werr.Err = fmt.Errorf("git commit: %s", firstLine(stderr))
			return werr
		}
		_, pushStderr, err := s.git(ctx, "push", "--quiet", "origin",
			fmt.Sprintf("HEAD:refs/heads/%s", s.opts.branch()))
		if err == nil {
			return nil // accepted
		}
		if !retryablePush(pushStderr) {
			werr.Outcome = PushFailed
			werr.PushStderr = pushStderr
			return werr
		}
		// Rejected as non-fast-forward: back off, then start over from a
		// fresh fetch and re-check the preconditions.
		if attempt == s.opts.MaxAttempts || ctx.Err() != nil {
			werr.Outcome = Timeout
			return werr
		}
		s.sleep(s.backoff(attempt))
	}
	return &WriteError{Outcome: Timeout}
}

// retryablePush separates "someone pushed first" (try again) from
// everything else (rights, protection, network), as the concept demands:
// an agent must not wait on an error that never goes away. Three messages
// mean the same race, two from the client side and one from the server
// side when it loses the compare-and-swap on the ref lock.
func retryablePush(stderr string) bool {
	return strings.Contains(stderr, "non-fast-forward") ||
		strings.Contains(stderr, "fetch first") ||
		strings.Contains(stderr, "cannot lock ref")
}

// backoff grows exponentially from the base, is capped, and carries
// jitter so concurrent runners do not retry in lockstep.
func (s *Store) backoff(attempt int) time.Duration {
	d := s.opts.BackoffBase << (attempt - 1)
	if d <= 0 || d > s.opts.BackoffCap {
		d = s.opts.BackoffCap
	}
	jitter := 0.5 + s.rng().Float64()
	return time.Duration(float64(d) * jitter)
}

func (s *Store) rng() *rand.Rand {
	s.rngOnce.Do(func() {
		seed := uint64(time.Now().UnixNano())
		// #nosec G404 -- the rng feeds retry jitter only, no security use.
		s.rand = rand.New(rand.NewPCG(seed, seed^0xd41c3e7))
	})
	return s.rand
}

// head adapts the store to the Head interface with a bound context.
type headAdapter struct {
	s   *Store
	ctx context.Context
}

func (h headAdapter) Has(path string) (bool, error) {
	return h.s.Has(h.ctx, path)
}

func (h headAdapter) ReadFile(path string) (string, error) {
	return h.s.ReadFile(h.ctx, path)
}

func (h headAdapter) List(prefix string) ([]string, error) {
	return h.s.List(h.ctx, prefix)
}

func (s *Store) head(ctx context.Context) Head { return headAdapter{s, ctx} }

// worktreeAdapter adapts the working copy directory to Worktree and
// remembers every file the mutation wrote; exactly those are staged.
type worktreeAdapter struct {
	s       *Store
	written []string
}

func (w *worktreeAdapter) ReadFile(path string) (string, error) {
	clean, err := safePath(path)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(filepath.Join(w.s.dir, filepath.FromSlash(clean)))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func (w *worktreeAdapter) WriteFile(path string, content string) error {
	clean, err := safePath(path)
	if err != nil {
		return err
	}
	full := filepath.Join(w.s.dir, filepath.FromSlash(clean))
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return err
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		return err
	}
	w.written = append(w.written, clean)
	return nil
}

// safePath normalises an in-repo path and rejects everything that could
// escape the working copy. Ticket content is untrusted input; paths built
// from it are checked here.
func safePath(p string) (string, error) {
	p = strings.TrimSpace(p)
	p = strings.ReplaceAll(p, "\\", "/")
	if p == "" {
		return "", errors.New("empty path")
	}
	if strings.HasPrefix(p, "/") {
		return "", fmt.Errorf("absolute path %q", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return "", fmt.Errorf("invalid path segment in %q", p)
		}
		if strings.ContainsAny(seg, "\x00:") {
			return "", fmt.Errorf("invalid characters in path %q", p)
		}
	}
	return p, nil
}
