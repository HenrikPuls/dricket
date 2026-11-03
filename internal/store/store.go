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
	"hash/fnv"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
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
	Remote  string // remote URL or path
	Branch  string // ticket branch; empty means "tickets"
	DataDir string // parent for the private working copy; empty = user cache dir
	Profile profile.Profile
	// IsolateGitConfig replaces the user's global git configuration with
	// an empty file for every call, so nothing leaks in from the
	// surrounding setup. Set for runner profiles with their own
	// credentials (docs/CONCEPT.md, "Accounts per agent").
	IsolateGitConfig bool
	MaxAttempts      int
	BackoffBase      time.Duration
	BackoffCap       time.Duration
	TotalTimeout     time.Duration
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

type gitRunnerFunc func(context.Context, ...string) (string, string, error)

// Store is a private working copy plus the git access around it. It is not
// safe for concurrent use; processes and profiles each get their own.
type Store struct {
	opts            Options
	dir             string
	gitConfigGlobal string // empty = ambient user configuration applies
	gitRunner       gitRunnerFunc
	sleep           func(time.Duration)
	hasRef          bool // remote branch existed at the last fetch
	rand            *rand.Rand
	rngOnce         sync.Once
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
	if opts.IsolateGitConfig {
		// An empty file replaces the user's global git configuration;
		// the profile's own identity comes from its env (ADR: profiles
		// share nothing with the surrounding setup).
		iso := filepath.Join(opts.DataDir, filepath.Base(dir)+".gitconfig")
		if err := os.WriteFile(iso, nil, 0o600); err != nil {
			return nil, fmt.Errorf("store: git config isolation: %w", err)
		}
		s.gitConfigGlobal = iso
	}
	if err := s.initOnce(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// Dir is the private working copy path (diagnostics, tests).
func (s *Store) Dir() string { return s.dir }

// RemoteBranch returns the selected remote and ticket branch.
func (s *Store) RemoteBranch() (string, string) { return s.opts.Remote, s.opts.branch() }

// HeadSHA returns the commit at the private working copy's HEAD.
func (s *Store) HeadSHA(ctx context.Context) (string, error) {
	out, _, err := s.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

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
	if s.gitRunner != nil {
		return s.gitRunner(ctx, args...)
	}
	return s.execGit(ctx, args...)
}

func (s *Store) execGit(ctx context.Context, args ...string) (string, string, error) {
	full := append([]string{"-C", s.dir, "-c", "core.autocrlf=false"}, args...)
	cmd := exec.CommandContext(ctx, "git", full...) // #nosec G204 -- args are tool-built constants and validated paths (ADR-0002)
	cmd.Env = append(os.Environ(), s.opts.Profile.Env(s.gitConfigGlobal)...)
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

// Refresh brings the working copy to the fetched head. After it, Has and
// ReadFile read the fresh remote state. Read-only commands call this once;
// the write loop calls it on every attempt.
func (s *Store) Refresh(ctx context.Context) error {
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

// refresh is the internal alias used by the write loop.
func (s *Store) refresh(ctx context.Context) error { return s.Refresh(ctx) }

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

// slug derives a filesystem-safe, collision-resistant directory name from
// a remote or owner id: readable prefix plus a hash of the full input, so
// two long paths that share a prefix never share a working copy.
func slug(s string) string {
	s = strings.ToLower(s)
	parts := unsafeChars.Split(s, -1)
	var keep []string
	for _, p := range parts {
		if p != "" {
			keep = append(keep, p)
		}
	}
	joined := strings.Join(keep, "-")
	if len(joined) > 40 {
		joined = joined[:40]
	}
	if joined == "" {
		joined = "x"
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(s))
	return joined + "-" + strconv.FormatUint(uint64(h.Sum32()), 16)
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

// lines splits command output into non-empty trimmed lines.
func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if l = strings.TrimSpace(strings.TrimRight(l, "\r")); l != "" {
			out = append(out, l)
		}
	}
	return out
}

// ------------------------------------------------------- local state ----

// DataDir returns the directory that holds the private working copies.
func (s *Store) DataDir() string { return s.opts.DataDir }

// StateDir returns a directory for local, disposable state belonging to this
// store and profile (inbox markers and the like). It is a sibling of the
// working copy, so `git clean` can never remove it, and it is per profile
// because the working copy name already contains the owner.
func (s *Store) StateDir() (string, error) {
	dir := filepath.Join(s.opts.DataDir, filepath.Base(s.dir)+".state")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("state dir: %w", err)
	}
	return dir, nil
}

// LastChange returns the commit time of the most recent change to a path;
// ok is false when the path has no history. Call Refresh first so the local
// branch matches the remote state.
func (s *Store) LastChange(ctx context.Context, path string) (time.Time, bool) {
	if !s.hasRef {
		return time.Time{}, false
	}
	out, _, err := s.git(ctx, "log", "-1", "--format=%cI", "--", path)
	if err != nil {
		return time.Time{}, false
	}
	t := strings.TrimSpace(out)
	if t == "" {
		return time.Time{}, false
	}
	ts, err := time.Parse(time.RFC3339, t)
	if err != nil {
		return time.Time{}, false
	}
	return ts, true
}

// PushDryRun contacts the remote and reports whether a push of the current
// head would be accepted, without transferring anything. A no-op push
// cannot fully prove branch-protection rights; callers must say so.
func (s *Store) PushDryRun(ctx context.Context) error {
	_, stderr, err := s.git(ctx, "push", "--dry-run", "origin",
		fmt.Sprintf("HEAD:refs/heads/%s", s.opts.branch()))
	if err != nil {
		return errors.New(firstLine(stderr))
	}
	return nil
}

// AuthorEmail is the git author identity this store pushes as; the quota
// counts add commits by it (abuse protection, not a security boundary).
func (s *Store) AuthorEmail() string { return s.opts.Profile.GitEmail }

// RecentAdds counts distinct ticket files added by the given author
// within the window ending now. It refreshes the working copy first (the
// caller's write loop refreshes again; one extra fetch per add is the
// price of abuse protection). ok is 0 when the branch has no history.
func (s *Store) RecentAdds(ctx context.Context, window time.Duration, author string) (int, error) {
	if err := s.Refresh(ctx); err != nil {
		return 0, err
	}
	if !s.hasRef {
		return 0, nil
	}
	since := time.Now().Add(-window).UTC().Format(time.RFC3339)
	out, stderr, err := s.git(ctx, "log", "--since="+since, "--author="+author,
		"--diff-filter=A", "--name-only", "--format=", "--", "tickets/")
	if err != nil {
		return 0, fmt.Errorf("git log for quota: %s", firstLine(stderr))
	}
	seen := map[string]bool{}
	for _, l := range lines(out) {
		if strings.HasSuffix(l, "/ticket.md") {
			seen[l] = true
		}
	}
	return len(seen), nil
}

// ------------------------------------------------------------ config ----

// configRef is the local ref where the protected main branch lands. A
// private namespace, so FETCH_HEAD — which the ticket protocol uses — is
// never touched by config reads.
const configRef = "refs/dricket/config"

// ConfigFile returns the content of a file from the protected main branch
// of the remote (for example dricket.toml), or nil when the remote has no
// main branch or the file does not exist. Callers decide whether that
// means defaults or an error. Parse errors are the caller's business: a
// broken config is a hard error for writes and a finding in validate.
func (s *Store) ConfigFile(ctx context.Context, name string) ([]byte, error) {
	// --no-write-fetch-head keeps FETCH_HEAD, which the ticket protocol
	// reads against, pointing at the ticket branch.
	_, stderr, err := s.git(ctx, "fetch", "--quiet", "--no-write-fetch-head", "origin",
		fmt.Sprintf("+refs/heads/main:%s", configRef))
	if err != nil {
		if strings.Contains(stderr, "couldn't find remote ref") {
			return nil, nil // no main branch: defaults apply
		}
		return nil, fmt.Errorf("fetch origin main: %s", firstLine(stderr))
	}
	out, _, err := s.git(ctx, "show", configRef+":"+name)
	if err != nil {
		return nil, nil // file absent on main: defaults apply
	}
	return []byte(out), nil
}

// ------------------------------------------------------------ history ----

// FileChange is one entry of a commit's change list; Status is the git
// name-status letter (A, M, D).
type FileChange struct {
	Status byte
	Path   string
}

// Commits lists the commits that touched the ticket tree, oldest first.
// Call Refresh first.
func (s *Store) Commits(ctx context.Context) ([]string, error) {
	if !s.hasRef {
		return nil, nil
	}
	out, _, err := s.git(ctx, "log", "--reverse", "--format=%H", "--", "tickets/")
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	return lines(out), nil
}

// CommitChanges reports what one commit changed.
func (s *Store) CommitChanges(ctx context.Context, commit string) ([]FileChange, error) {
	out, _, err := s.git(ctx, "diff-tree", "--no-commit-id", "--name-status", "-r", commit)
	if err != nil {
		return nil, fmt.Errorf("diff-tree %s: %w", commit[:min(10, len(commit))], err)
	}
	var changes []FileChange
	for _, l := range lines(out) {
		f := strings.SplitN(l, "\t", 2)
		if len(f) == 2 && f[0] != "" {
			changes = append(changes, FileChange{Status: f[0][0], Path: f[1]})
		}
	}
	return changes, nil
}

// ShowRev returns a file's content at an arbitrary revision
// (commit, commit^, FETCH_HEAD, ...).
func (s *Store) ShowRev(ctx context.Context, rev, path string) (string, error) {
	out, _, err := s.git(ctx, "show", rev+":"+path)
	if err != nil {
		return "", err
	}
	return out, nil
}

// ---------------------------------------------------------------- grep ----

// Grep runs a fixed-string, case-insensitive search over the ticket files
// of the fetched head and returns git grep's raw "path:line:text" lines.
// The pattern is passed as one argument; no shell is involved.
func (s *Store) Grep(ctx context.Context, pattern, prefix string) ([]string, error) {
	if !s.hasRef {
		return nil, nil
	}
	args := []string{"grep", "-I", "-n", "-F", "-i", "--", pattern, "FETCH_HEAD", "--", "tickets/" + prefix}
	out, stderr, err := s.git(ctx, args...)
	if err != nil {
		if out == "" && strings.TrimSpace(stderr) == "" {
			return nil, nil // git grep exits 1 when nothing matched
		}
		return nil, fmt.Errorf("git grep: %s", firstLine(stderr))
	}
	return lines(out), nil
}
