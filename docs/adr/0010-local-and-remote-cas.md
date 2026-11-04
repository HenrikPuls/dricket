# ADR-0010: Local ref CAS and remote push CAS

- Status: accepted
- Date: 2026-11-03

## Context

ADR-0001 uses an atomic fast-forward push as the claim and write boundary. That
works for remote and bare repositories, but a non-bare local store is also the
user's checkout. Git refuses pushes to its checked-out branch. Allowing such
pushes with `updateInstead` would reject unrelated writes during a dirty ticket
edit; ignoring the checkout would desynchronize HEAD, the index and user files.

Discovery also depended on `dricket.toml` being present in the current checkout,
although protected configuration intentionally lives only on `main`. Checking
out `tickets` therefore made the store undiscoverable.

The owner requires one ordinary local repository, with the ticket branch
checked out after `dk init`, and unchanged remote collaboration.

## Decision

**Keep two coordination paths behind one write protocol.** Both capture a fresh
base, check preconditions against an immutable snapshot, create one candidate
commit and publish only by fast-forward. Retries always re-read the base and
repeat every precondition; neither path rebases.

- A remote, URL or bare filesystem store retains fetch and fast-forward push.
- A non-bare filesystem store uses a private linked worktree with shared Git
  objects and refs. The private HEAD is detached for existing ticket history;
  candidate commits never directly move the public ticket ref.
- Local refresh resolves the ticket ref to a base SHA and reads private `HEAD`
  throughout the attempt. The public ref is not a stable snapshot.
- If the ticket ref is not checked out, publication uses `git update-ref` with
  the captured base as expected old value.
- Ordinary local writes to a checked-out ticket branch use `merge --ff-only` in
  that checkout. A non-blocking OS file lock in the common Git directory
  serializes local publication and is released automatically on process exit,
  preventing another publisher's files from appearing before its ref update.
  Fast-forward publication updates ref, index and working tree together and
  preserves non-conflicting dirty paths. A pre-existing conflicting dirty path returns
  error kind `conflict`, exit 12, without retrying. Dirty paths are captured at
  refresh so a file concurrently created by another publisher remains a
  retryable race instead of being mistaken for pre-existing user work.
- Whole-ticket commit is the deliberate exception: its candidate changes the
  intentionally dirty ticket file. It publishes with expected-old ref CAS, then
  uses ADR-0009's guarded adoption. Local adoption verifies the local ticket
  history without an origin fetch and never rewinds a later writer.

`dk init` leaves the ticket branch checked out. Discovery reads committed
`main:dricket.toml` at the actual working-tree root; the current-checkout file
is only a fallback when main has no config. A discovered origin selects remote
CAS. An explicit non-bare filesystem path selects local CAS; bare paths keep
push CAS.

## Consequences

- Local stores need no network, bare repository, store binding or second
  repository. Existing main-checked-out stores continue to work.
- Remote branch rights and the accepted push remain the shared multi-machine
  boundary. When a discovered store has an origin, there is no automatic offline
  fallback to local writes, because that would fork coordination history.
- Local publication has to manage the visible checkout explicitly. The
  concurrency, dirty-file and whole-ticket adoption tests are permanent safety
  gates, not optional implementation coverage.
- Private linked-worktree cache entries are disposable; user worktrees are never
  cleaned, pruned or reset implicitly.
- ADR-0001's remote claim evidence remains valid. Local CAS is an additional
  transport implementation, not a change to claims, IDs, leases or ticket data.
