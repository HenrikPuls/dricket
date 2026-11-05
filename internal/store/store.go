// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
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
	"github.com/HenrikPuls/dricket/internal/trace"
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
	// Probe shares repository facts found during discovery with Open so the
	// same question is not asked of git twice in one call; nil means ask.
	Probe        *Probe
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

type gitRunnerFunc func(context.Context, ...string) (string, string, error)
type gitAtFunc func(context.Context, string, Options, ...string) (string, error)

// Store is a private working copy plus the git access around it. It is not
// safe for concurrent use; processes and profiles each get their own.
type Store struct {
	opts            Options
	dir             string
	localDir        string // non-bare local repository root; empty = push CAS
	headRev         string // immutable read revision for this attempt
	baseSHA         string // expected old local head for the current attempt
	hooksPath       string // fixed empty hook path for local shared worktrees
	localAttempt    int
	localDirty      map[string]bool // user paths dirty at the attempt's refresh
	gitConfigGlobal string          // empty = ambient user configuration applies
	gitRunner       gitRunnerFunc
	gitAt           gitAtFunc
	sleep           func(time.Duration)
	hasRef          bool   // ticket branch existed at the last refresh
	privateReady    bool   // local private worktree exists (created by the first write)
	writeMode       bool   // reads go through the private worktree HEAD (inside a write)
	branchChecked   bool   // local read mode: hasRef was verified against git
	pinned          string // local read mode: commit all reads use after Pin
	rand            *rand.Rand
	rngOnce         sync.Once
}

// Open prepares the private working copy for one profile against one
// remote branch. It never touches the current working directory.
func Open(ctx context.Context, opts Options) (*Store, error) {
	defer trace.Begin("open store")()
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

	s := &Store{opts: opts, dir: dir, headRev: "FETCH_HEAD", gitAt: runGitEnv, sleep: time.Sleep}
	if localDir, ok := localWorkingRepository(ctx, opts.Probe, opts.Remote); ok {
		// A local store is read straight from the repository; the private
		// worktree is created by the first write (ensurePrivate), so a read
		// never touches it (ADR-0014).
		s.localDir = localDir
		s.headRev = "HEAD"
	} else if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("store: working copy dir: %w", err)
	}
	if opts.IsolateGitConfig {
		// An empty file replaces the user's global git configuration;
		// the profile's own identity comes from its env (ADR: profiles
		// share nothing with the surrounding setup).
		if err := os.MkdirAll(opts.DataDir, 0o750); err != nil {
			return nil, fmt.Errorf("store: data dir: %w", err)
		}
		iso := filepath.Join(opts.DataDir, filepath.Base(dir)+".gitconfig")
		if err := os.WriteFile(iso, nil, 0o600); err != nil {
			return nil, fmt.Errorf("store: git config isolation: %w", err)
		}
		s.gitConfigGlobal = iso
	}
	if s.localDir == "" {
		if err := s.initOnce(ctx); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// ensurePrivate creates the private worktree of a local store on first use.
// Only writes need it; remote stores prepare their clone in Open.
func (s *Store) ensurePrivate(ctx context.Context) error {
	if s.localDir == "" || s.privateReady {
		return nil
	}
	defer trace.Begin("prepare private worktree")()
	hooks, err := ensureEmptyHooks(s.opts.DataDir)
	if err != nil {
		return err
	}
	s.hooksPath = hooks
	if err := os.MkdirAll(s.dir, 0o750); err != nil {
		return fmt.Errorf("store: working copy dir: %w", err)
	}
	if err := s.initLocal(ctx); err != nil {
		return err
	}
	s.privateReady = true
	return nil
}

// Dir is the private working copy path (diagnostics, tests).
func (s *Store) Dir() string { return s.dir }

// RemoteBranch returns the selected remote and ticket branch.
func (s *Store) RemoteBranch() (string, string) { return s.opts.Remote, s.opts.branch() }

// IsLocal reports whether writes publish through local ref CAS instead of push.
func (s *Store) IsLocal() bool { return s.localDir != "" }

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

// initOnce makes the private working copy usable for local CAS or push CAS.
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
	return s.gitIn(ctx, s.dir, nil, args...)
}

// gitIn runs one git command in dir, optionally feeding stdin.
func (s *Store) gitIn(ctx context.Context, dir string, stdin io.Reader, args ...string) (string, string, error) {
	if s.gitRunner != nil {
		return s.gitRunner(ctx, args...)
	}
	return s.execGitIn(ctx, dir, stdin, args...)
}

func (s *Store) execGit(ctx context.Context, args ...string) (string, string, error) {
	return s.execGitIn(ctx, s.dir, nil, args...)
}

func (s *Store) execGitIn(ctx context.Context, dir string, stdin io.Reader, args ...string) (string, string, error) {
	// core.longpaths lets git on Windows write below paths over MAX_PATH
	// (deep data dirs); it is a no-op elsewhere.
	pre := []string{"-C", dir, "-c", "core.autocrlf=false", "-c", "core.longpaths=true"}
	if s.localDir != "" && s.hooksPath != "" {
		pre = append(pre, "-c", "core.hooksPath="+s.hooksPath)
	}
	full := append(pre, args...)
	cmd := exec.CommandContext(ctx, "git", full...) // #nosec G204 -- args are tool-built constants and validated paths (ADR-0002)
	cmd.Env = append(os.Environ(), s.opts.Profile.Env(s.gitConfigGlobal)...)
	cmd.Stdin = stdin
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err := trace.Run(cmd)
	return out.String(), errb.String(), err
}

// readGit runs a read-only git command against the repository that holds the
// current read snapshot: the user's repository for a local store outside a
// write, the private clone or worktree otherwise.
func (s *Store) readGit(ctx context.Context, args ...string) (string, string, error) {
	return s.readGitIn(ctx, nil, args...)
}

func (s *Store) readGitIn(ctx context.Context, stdin io.Reader, args ...string) (string, string, error) {
	dir := s.dir
	if s.localReadMode() {
		dir = s.localDir
	}
	return s.gitIn(ctx, dir, stdin, args...)
}

// localReadMode reports whether reads bypass the private worktree.
func (s *Store) localReadMode() bool { return s.localDir != "" && !s.writeMode }

// readRev is the revision reads resolve paths against: the ticket branch
// itself for a local store outside a write, the pinned head otherwise.
func (s *Store) readRev() string {
	if s.localReadMode() {
		if s.pinned != "" {
			return s.pinned
		}
		return "refs/heads/" + s.opts.branch()
	}
	return s.headRev
}

// verifyBranch settles whether the ticket branch exists after an optimistic
// local read failed; it costs one process and only runs on a failure path.
func (s *Store) verifyBranch(ctx context.Context) bool {
	if !s.localReadMode() {
		return s.hasRef
	}
	if !s.branchChecked {
		_, _, err := s.readGit(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+s.opts.branch()+"^{commit}")
		s.hasRef = err == nil
		s.branchChecked = true
	}
	return s.hasRef
}

// ---------------------------------------------------------------- reads --

// fetch refreshes FETCH_HEAD from the remote branch. Missing remote branch
// is not an error; it leaves the store without a head.
func (s *Store) fetch(ctx context.Context) error {
	defer trace.Begin("fetch ticket branch")()
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

// Refresh pins the store head for reading. It never touches a working copy:
// a local store is read straight from its ticket branch (no process), a
// remote or bare store fetches and then reads FETCH_HEAD (ADR-0014). Read-only
// commands call this once; writes use refresh, which also resets the private
// working copy.
func (s *Store) Refresh(ctx context.Context) error {
	defer trace.Begin("refresh head")()
	s.writeMode = false
	if s.localDir != "" {
		s.hasRef = true // optimistic; verifyBranch settles it if a read fails
		s.branchChecked = false
		s.pinned = ""
		return nil
	}
	return s.fetch(ctx)
}

// Pin fixes the local read head to the commit the ticket branch has now, so
// a command that reads in several steps (validate, inbox) sees one state even
// while another process publishes. It costs one process. Refresh alone reads
// the live branch: every single read is atomic, several reads are not pinned.
// Remote and bare stores are pinned by their fetch already.
func (s *Store) Pin(ctx context.Context) error {
	if !s.localReadMode() {
		return nil
	}
	defer trace.Begin("pin head")()
	out, _, err := s.readGit(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+s.opts.branch()+"^{commit}")
	s.branchChecked = true
	if err != nil {
		s.hasRef = false
		return nil
	}
	s.hasRef = true
	s.pinned = strings.TrimSpace(out)
	return nil
}

// refresh prepares one write attempt: the immutable base is the private
// working copy reset to the current head.
func (s *Store) refresh(ctx context.Context) error {
	defer trace.Begin("refresh private copy")()
	s.writeMode = true
	if s.localDir != "" {
		return s.refreshLocal(ctx)
	}
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
	if !s.hasRef {
		return false, nil
	}
	_, _, err := s.readGit(ctx, "cat-file", "-e", s.readRev()+":"+path)
	return err == nil, nil
}

// ReadFile returns a file from the current head.
func (s *Store) ReadFile(ctx context.Context, path string) (string, error) {
	if !s.hasRef {
		return "", fmt.Errorf("%s: %w", path, ErrNoBranch)
	}
	defer trace.Begin("read " + path)()
	out, _, err := s.readGit(ctx, "show", s.readRev()+":"+path)
	if err != nil {
		if !s.verifyBranch(ctx) {
			return "", fmt.Errorf("%s: %w", path, ErrNoBranch)
		}
		return "", fmt.Errorf("%s: %w", path, ErrNotExist)
	}
	return out, nil
}

// List returns all file paths under a prefix on the current head.
func (s *Store) List(ctx context.Context, prefix string) ([]string, error) {
	if !s.hasRef {
		return nil, nil
	}
	defer trace.Begin("list " + prefix)()
	args := []string{"ls-tree", "-r", "--name-only", s.readRev()}
	if prefix != "" {
		args = append(args, "--", prefix)
	}
	out, _, err := s.readGit(ctx, args...)
	if err != nil {
		if !s.verifyBranch(ctx) {
			return nil, nil
		}
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

// FileContent is one file of a batch read.
type FileContent struct {
	Path    string
	Content string
}

// ReadAll returns every file under prefix whose path ends in suffix, with
// its content, from one consistent snapshot of the head: one ls-tree for the
// blob ids and one `cat-file --batch` for all contents, however many files
// there are. A missing branch reads as empty, like List.
func (s *Store) ReadAll(ctx context.Context, prefix, suffix string) ([]FileContent, error) {
	if !s.hasRef {
		return nil, nil
	}
	if s.gitRunner != nil { // test stubs cannot feed stdin
		return s.readAllSlow(ctx, prefix, suffix)
	}
	defer trace.Begin("read all " + prefix + "*" + suffix)()
	args := []string{"ls-tree", "-r", "-z", s.readRev()}
	if prefix != "" {
		args = append(args, "--", prefix)
	}
	out, _, err := s.readGit(ctx, args...)
	if err != nil {
		if !s.verifyBranch(ctx) {
			return nil, nil
		}
		return nil, fmt.Errorf("ls-tree %s: %w", prefix, err)
	}
	var paths []string
	var request strings.Builder
	for _, rec := range strings.Split(out, "\x00") {
		meta, path, ok := strings.Cut(rec, "\t")
		if !ok || !strings.HasSuffix(path, suffix) {
			continue
		}
		f := strings.Fields(meta) // mode type sha
		if len(f) != 3 || f[1] != "blob" {
			continue
		}
		paths = append(paths, path)
		request.WriteString(f[2] + "\n")
	}
	if len(paths) == 0 {
		return nil, nil
	}
	batch, stderr, err := s.readGitIn(ctx, strings.NewReader(request.String()), "cat-file", "--batch")
	if err != nil {
		return nil, fmt.Errorf("cat-file --batch: %s", firstLine(stderr))
	}
	files := make([]FileContent, 0, len(paths))
	rest := batch
	for _, path := range paths {
		nl := strings.IndexByte(rest, '\n')
		if nl < 0 {
			return nil, fmt.Errorf("cat-file --batch: truncated output at %s", path)
		}
		header := strings.Fields(rest[:nl]) // sha type size, or sha missing
		if len(header) != 3 || header[1] != "blob" {
			return nil, fmt.Errorf("cat-file --batch: %s: unexpected %q", path, rest[:nl])
		}
		size, perr := strconv.Atoi(header[2])
		if perr != nil || size < 0 || nl+1+size > len(rest) {
			return nil, fmt.Errorf("cat-file --batch: bad size for %s", path)
		}
		files = append(files, FileContent{Path: path, Content: rest[nl+1 : nl+1+size]})
		rest = rest[nl+1+size:]
		rest = strings.TrimPrefix(rest, "\n")
	}
	return files, nil
}

// readAllSlow is ReadAll without batching: List plus one ReadFile per path.
func (s *Store) readAllSlow(ctx context.Context, prefix, suffix string) ([]FileContent, error) {
	paths, err := s.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	var files []FileContent
	for _, p := range paths {
		if !strings.HasSuffix(p, suffix) {
			continue
		}
		content, err := s.ReadFile(ctx, p)
		if err != nil {
			continue
		}
		files = append(files, FileContent{Path: p, Content: content})
	}
	return files, nil
}

// -------------------------------------------------------------- helpers --

// checkRefName validates a branch name in process (see checkBranchName); the
// context is kept for callers that used to start `git check-ref-format`.
func checkRefName(_ context.Context, name string) error {
	if err := checkBranchName(name); err != nil {
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
	defer trace.Begin("last change " + path)()
	out, _, err := s.readGit(ctx, "log", "-1", "--format=%cI", s.readRev(), "--", path)
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
	src := "HEAD"
	if s.hasRef && !s.writeMode {
		src = s.headRev // reads no longer move the clone's HEAD
	}
	_, stderr, err := s.git(ctx, "push", "--dry-run", "origin",
		fmt.Sprintf("%s:refs/heads/%s", src, s.opts.branch()))
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
	out, stderr, err := s.readGit(ctx, "log", s.readRev(), "--since="+since, "--author="+author,
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

// ConfigFile returns a file from protected main, or nil when main or the file
// is absent. Local non-bare stores read their main ref directly; remote and
// bare stores fetch into a private config ref.
func (s *Store) ConfigFile(ctx context.Context, name string) ([]byte, error) {
	defer trace.Begin("read config " + name)()
	if s.localDir != "" {
		return s.configFileLocal(ctx, name)
	}
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
	defer trace.Begin("list commits")()
	out, _, err := s.readGit(ctx, "log", "--reverse", "--format=%H", s.readRev(), "--", "tickets/")
	if err != nil {
		if !s.verifyBranch(ctx) {
			return nil, nil
		}
		return nil, fmt.Errorf("git log: %w", err)
	}
	return lines(out), nil
}

// CommitChanges reports what one commit changed.
func (s *Store) CommitChanges(ctx context.Context, commit string) ([]FileChange, error) {
	out, _, err := s.readGit(ctx, "diff-tree", "--no-commit-id", "--name-status", "-r", commit)
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
	out, _, err := s.readGit(ctx, "show", rev+":"+path)
	if err != nil {
		return "", err
	}
	return out, nil
}

// ---------------------------------------------------------------- grep ----

// Grep runs a fixed-string, case-insensitive search over the ticket files
// of the current snapshot and returns git grep's raw "path:line:text" lines.
// The pattern is passed as one argument; no shell is involved.
func (s *Store) Grep(ctx context.Context, pattern, prefix string) ([]string, error) {
	if !s.hasRef {
		return nil, nil
	}
	defer trace.Begin("search")()
	args := []string{"grep", "-I", "-n", "-F", "-i", "--", pattern, s.readRev(), "--", "tickets/" + prefix}
	out, stderr, err := s.readGit(ctx, args...)
	if err != nil {
		if s.localReadMode() && !s.verifyBranch(ctx) {
			return nil, nil
		}
		if out == "" && strings.TrimSpace(stderr) == "" {
			return nil, nil // git grep exits 1 when nothing matched
		}
		return nil, fmt.Errorf("git grep: %s", firstLine(stderr))
	}
	return lines(out), nil
}
