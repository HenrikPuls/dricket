# Local and remote CAS design

- Status: accepted
- Date: 2026-11-03
- Roadmap: R-14
- Related: ADR-0002, ADR-0008, ADR-0009

## Intent

A local dricket store should be one ordinary Git repository. `dk init` leaves
its ticket branch checked out, and subsequent `dk` commands work from that
checkout without a binding or an explicit store flag. Configuration remains on
`main`; ticket history remains on its own branch. When the store resolves to a
remote, the existing fast-forward-push coordination remains unchanged.

The trigger is a real v0.4.0 failure: after manually checking out `tickets`,
store discovery can no longer find `dricket.toml`, so commands from ticket
subdirectories report that no store exists. Merely changing `init` to check out
`tickets` would make this the default failure mode and would additionally make
local pushes target the checked-out branch.

## Goals

- One local repository, not a bare store plus a second user repository.
- Local `dk init [dir] [prefix]` checks out the selected ticket branch.
- Discovery works from `main`, from the ticket branch and from nested ticket
  directories, including custom ticket branch names.
- Local writes retain atomic compare-and-swap behavior, bounded retries and the
  existing claim, revision and validation invariants.
- A dirty user checkout is never cleaned or hard-reset implicitly. Unrelated
  local edits and accepted remote/local comments must survive.
- Existing stores and the remote protocol continue to work without migration.

## Non-goals and anti-goals

- No single-branch store and no change to the protected-config model.
- No offline write mode when discovery has selected a remote. Falling back to a
  local ref would fork the coordination history.
- No direct commits in the user's visible working tree by the ordinary write
  loop; concurrent processes and unrelated staged changes make that unsafe.
- No new CLI command, flag, exit code, JSON schema or ticket format.
- No automatic migration or cleanup of existing user repositories.

## Approaches

### Chosen: one repository with transport-specific publication

The service layer and write preconditions stay common. A filesystem store uses
local atomic fast-forward publication; a URL store uses the existing
fast-forward push. Local preparation uses a private detached linked worktree
of the same repository, not a clone. Only private, disposable worktrees are
hard-reset.

This meets the one-repository requirement and preserves the existing
file-based mutation logic. It adds a local publication implementation and
requires explicit integration with the user checkout.

### Rejected: bare store plus checkout

A bare canonical repository would preserve push CAS everywhere. It is simpler
inside the store but violates the requirement that local initialization create
only one repository.

### Rejected: allow pushes to the checked-out branch

`receive.denyCurrentBranch=updateInstead` rejects every publication while the
user checkout is dirty, including unrelated comments during a ticket edit.
`ignore` lets HEAD and the working tree diverge without a safe edit-base or
adoption protocol. Neither preserves ADR-0009's behavior.

### Rejected: direct commits in the visible checkout

This would couple independent writers to the user's index, working tree and
current branch. A private detached worktree avoids that coupling while still
sharing one object database and refs.

## Store selection and discovery

A store root is a Git working tree with `dricket.toml` either in its current
checkout or in its local `main` branch. The latter is read with a Git object
lookup such as `git show refs/heads/main:dricket.toml`; no file is materialized
in the ticket checkout. A malformed config still defaults only during
discovery and fails when loaded for an operation, as today.

The committed `dricket.toml` on `main` is authoritative whenever that blob
exists; the current-checkout file is only a compatibility fallback when `main`
has no config. Thus a dirty working-tree edit or a file on the ticket branch
cannot override the protected branch selection. The ticket branch defaults to
`tickets`.

Discovery continues to prefer the `origin` URL when one exists. Therefore
adding an origin deliberately moves a discovered store from local CAS to
remote push CAS. An explicit filesystem `--store`/`DK_STORE` binding selects
local CAS only when its target is a non-bare working repository. Filesystem
paths and `file://` URLs naming bare repositories retain fetch/push CAS, which
keeps existing bare-local fixtures, workflows and integrations unchanged. Any
URL other than an explicit non-bare filesystem store selects remote CAS.

Configuration reads for a local non-bare filesystem store come directly from
its local `main` ref. Remote and bare stores retain the existing fetch into
`refs/dricket/config`.

## Initialization

Local initialization keeps the existing refusal rules for non-empty or already
initialized directories. It commits the scaffold on `main`, seeds the selected
ticket branch and finishes with that ticket branch checked out. The working
tree therefore contains ticket data rather than the scaffold. Existing JSON
fields remain unchanged; human output may state the checked-out branch.

Remote URL initialization still only scaffolds and pushes `main` and the
ticket branch from a temporary repository; it has no user checkout to switch.

## Common write preparation

Both modes retain the existing `Store.Write` contract:

1. Refresh the private worktree to the current ticket-ref head.
2. Check all preconditions against that fresh head.
3. Apply the mutation in the private worktree and create one commit.
4. Publish with local fast-forward CAS or remote fast-forward push.
5. On a concurrent-head rejection, back off with jitter and repeat from step 1.
   Never rebase.

For local mode, refresh resolves the current local ticket ref to a commit SHA
and resets only the private detached worktree to that SHA. The attempt retains
this immutable base SHA and performs all precondition/read/mutation work
against private `HEAD`, never against the still-moving public ref. Publication
uses the captured SHA as the expected old value. For remote mode, refresh
remains fetch from the selected remote and reads use `FETCH_HEAD`.

## Local publication

Local publication must distinguish an unreferenced branch from a branch
checked out in a user worktree.

### Ticket ref not checked out

Publish with an atomic expected-old-value ref update, equivalent to:

```console
git update-ref refs/heads/<ticket-branch> <candidate> <fresh-base>
```

Success means the candidate was fast-forwarded onto exactly the checked base.
An expected-old mismatch is retryable. An inconclusive result is reconciled by
checking whether the candidate is an ancestor of the accepted ticket history,
just as for remote pushes.

### Ticket ref checked out: ordinary writes

For ordinary writes, publish by fast-forwarding the worktree that has the
ticket branch checked out, equivalent to:

```console
git -C <ticket-checkout> merge --ff-only <candidate>
```

The candidate already has the fresh base as parent. Fast-forward-only makes a
concurrent non-descendant head a retryable rejection; no merge commit is
created. A non-blocking OS file lock in the common Git directory serializes
local publishers and is released on process exit, preventing another
publisher's files from appearing before its ref update. Git also updates the
index and working tree so that HEAD cannot move while leaving an otherwise
clean checkout stale.

Local changes that would be overwritten make publication fail without
discarding them. The command reports error kind `conflict` with existing exit
12. This is a local checkout conflict, not a concurrent-ref race, and must not
be retried until the retry budget expires. The attempt captures dirty paths at
refresh: a conflicting path already present then is a user conflict, while a
path first created by another in-flight publisher is a retryable race.
Non-conflicting dirty paths, including an edited `ticket.md` during an add-only
comment write, must survive and the comment must become visible.

### Ticket ref checked out: whole-ticket commit

The ordinary fast-forward merge cannot publish `dk commit`: its candidate
intentionally changes the same `ticket.md` that is dirty in the user checkout.
This path therefore uses the expected-old-value ref update first and then the
existing guarded adoption flow:

1. The service has already read the exact edit base and edited content.
2. Local CAS publishes the candidate without touching the user working tree.
3. `Workspace.Adopt` verifies that the working-tree file still exactly matches
   the accepted candidate and that no unrelated dirty path exists.
4. Only then may it hard-reset the user checkout to the accepted commit.

For local stores, adoption verifies ancestry against the local ticket ref and
does not fetch a nonexistent origin. If adoption refuses or loses a race, the
accepted commit remains published, all user content remains in place and the
command reports the existing accepted-but-not-synced outcome. A later writer
advancing the ref must never be rewound by adoption.

Internal commits and publication in a linked local worktree must not newly
inherit repository hooks that the former private clone did not carry. Internal
Git calls keep their existing isolated argument/environment handling and use a
fixed empty hook path where necessary.

## Workspace behavior

Remote workspace sync and adoption remain unchanged. For a local store, the
user checkout and the store already share refs; ordinary local publication has
already fast-forwarded the checkout. `dk sync` therefore performs its clean
checkout and branch checks but needs no origin fetch. It must not hide or reset
local changes. Local restore semantics are unchanged.

## Compatibility and failure rules

- Existing local stores left on `main` continue to work: local ref publication
  updates the non-checked-out ticket branch and leaves the config checkout
  untouched.
- Switching an existing local store to `tickets` enables the checked-out
  publication path without migration.
- Remote writes, push rejection classification and unknown-push reconciliation
  remain covered by their existing tests.
- No path is ever deleted to recover from a failed local publication. Candidate
  commits may remain as unreachable Git objects after a rejected attempt.
- Private linked-worktree cache entries are disposable and may be recreated if
  stale or if a repository moved. User worktrees are never pruned or removed.

## Required verification

The implementation needs real Git integration tests, not only mocked command
runners:

- Local init checks out the configured ticket branch; immediate `dk add`
  without flags or bindings succeeds and the new ticket is visible.
- Discovery from nested directories works on `main`, on `tickets` and with a
  custom ticket branch; plain Git repositories are still not stores.
- Existing main-checked-out local stores and all remote workflows still pass.
- Concurrent local adds allocate unique IDs; concurrent claims have exactly one
  owner; all concurrency tests retain zero duplicate IDs, double owners, lost
  updates and identity violations.
- A comment published during a dirty whole-ticket edit preserves the edit and
  updates the comment; a conflicting same-ticket write preserves the edit and
  does not advance the ref.
- Local whole-ticket commit publishes and adopts the exact edit. Unrelated
  dirty files, an edit changed during commit and a later accepted writer follow
  ADR-0009's non-loss behavior.
- Expected-old ref races and inconclusive local publication results are fault
  injected and reconciled by commit identity, never by blind replay.
- Local sync, restore and custom branch behavior are exercised through the CLI.
- Full project gate and CI race tests pass.

## Documentation and implementation boundaries

Add ADR-0010 for the durable local/remote CAS decision. Update the protocol,
architecture, concept, README and any landing-page quick-start text that
currently describes main-checked-out initialization or discovery only from a
checked-out config. Add the user-facing change under Unreleased and move R-14
through the normal verification ledger.

The builder may choose internal helper names, cache layout and the exact
internal option that distinguishes ordinary writes from whole-ticket commit
publication. Those are not public interfaces. Any change to the stated checkout
safety, store-selection precedence or CLI/JSON contract requires renewed owner
approval.
