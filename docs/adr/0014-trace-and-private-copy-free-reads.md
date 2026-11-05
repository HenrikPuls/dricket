# ADR-0014: Call trace and reads without the private working copy

- Status: accepted
- Date: 2026-11-05

## Context

Measured on Windows with dk v0.6.0 against a local store of 91 tickets
(`GIT_TRACE2_EVENT`): `dk show` started 16 git processes (about 0.38 s),
`dk ls --json` 107 (about 2.1 s), `dk put --claim --if-version` about 0.62 s.
A bare git call costs about 47 ms on that machine, the same as an empty
process, so the process count is the cost. For a read, almost all of them were
housekeeping: five `rev-parse` calls that one call can answer, `check-ref-format`
on a constant string, a refresh of the private working copy (`rev-parse`,
`worktree list`, `reset --hard`, `checkout --detach`, `clean`), and for `ls` one
`git show` per ticket. The refresh also caused the parallel-read failure (Drey requirement 26): two parallel
reads reset the same private working copy and one exited with status 128.

The owner asked for two things, in this order: an instrument that shows what a
call does (Drey requirement 29), then fewer git processes for reads (Drey requirement 30). Those numbers belong to the Drey list; dricket tracks the work as R-26 (trace) and R-27 (reads).
Hard limit: claims, epoch fencing, compare-and-set on the ticket ref, the
on-disk format, exit codes and JSON output must not change.

## Options considered

For the trace:

1. Rely on git's `GIT_TRACE2_EVENT`. It shows the git side only and cannot say
   which dk step a process belongs to, nor how long dk itself works.
2. A built-in recorder around every git process start, with named steps.
   Chosen.

For the read path:

1. Keep the refresh but skip the reset when HEAD has not moved. Still touches
   the shared private copy (the parallel-read failure stays a race), still several processes.
2. Read straight from the ticket ref of the user's repository and create the
   private working copy only when a write needs it. Chosen.
3. Embed go-git or libgit2. Rejected earlier (go-git ignores `~/.ssh/config`:
   aliases, `IdentityFile`, `ProxyJump`); dricket keeps using the installed git.
4. A long-lived reader process or daemon (Q-07). Not needed once a read costs
   four processes; stays under "Later" with its trigger.

## Decision

**Trace (`internal/trace`).** `dk --trace` or `DK_TRACE=1` records the call as
named steps (store discovery, open store, refresh head, read, every write
attempt's refresh, check, commit, publish and backoff) with their duration and
every git process they started, with arguments and run time. The report is
written to stderr after the command ended: one line per step and process, then
`trace: git: N processes, X ms of Y ms`. With `--json` it is one JSON object on
stderr (`trace_schema_version`, `steps`, `git_processes`, `git_ms`,
`total_ms`). Stdout and the exit code are byte-identical with and without trace;
the recorder is off by default and every call into it is then a no-op. Arguments
are redacted before they are stored: the value of `-m`, the pattern of
`git grep` and the credentials in URLs never appear (no user content in logs).

**Read path.** A read never touches the private working copy.

- *Local store* (non-bare working repository): `Refresh` starts no process.
  Reads run `git -C <repository>` against `refs/heads/<branch>`: one
  `git show refs/heads/<branch>:<path>` for a ticket; `ls-tree -r -z` plus one
  `cat-file --batch` (`Store.ReadAll`) for every ticket, from the blob ids of a
  single tree listing, however many tickets exist. A missing branch is detected
  only on a failing read (one `rev-parse --verify`) and reads as empty, as before.
- *Remote and bare store*: unchanged freshness: `fetch` into `FETCH_HEAD`, then
  reads from `FETCH_HEAD`. Only `reset --hard` and `clean` are gone for reads.
- *Discovery* asks git one question per level: `rev-parse --is-inside-work-tree
  --show-toplevel` (was two calls, repeated again in `Open`). Levels between the
  working directory and its work-tree root are known not to be roots without
  asking, and the root found is handed to `Open` (`store.Probe`, per
  invocation, never process-global) so `Open` asks nothing.
- `check-ref-format --branch` is replaced by an in-process validator that is at
  least as strict as git (differential test against git).
- *Pinning.* Every single read is atomic by itself (one git process on a
  ref). Commands that read in several steps and need one state (`validate`,
  `inbox`) call `Store.Pin`, which resolves the branch to a commit with one
  `rev-parse` and reads that commit afterwards.
- *Writes* keep their protocol exactly: the private working copy is created
  lazily by the first `Write` (`ensurePrivate`: same `worktree add` and
  ownership check as before), each attempt still refreshes, resets, checks,
  commits and publishes by ref CAS or push. Reads inside `Write` (the
  precondition) still read the private `HEAD`, the immutable base of that
  attempt (ADR-0010). Writes profit from the shared, cheaper discovery only.

Result on the 91-ticket store (same machine and method as the baseline):
`show` 16 → 4 processes (0.38 s → 0.14 s), `ls --json` 107 → 5 (2.1 s → 0.16 s),
`put --claim --if-version` 26 → 22 processes (0.62 s → 0.56 s).

Four processes remain for `show`: discovery's `rev-parse`, the config read
(`show refs/heads/main:dricket.toml`, needed for the branch name), the origin
lookup (`remote get-url origin`, decides local versus remote store) and the
ticket read. The origin lookup could be merged into a `git config` read; that
is left out because `remote get-url` applies `url.<base>.insteadOf` rewriting
and reproducing it in process would be a behaviour risk for a 15 ms gain.

## Consequences

- A read no longer creates, resets or locks anything in the data directory:
  parallel reads cannot fail with exit 128. A regression test starts
  real processes in parallel; it fails on v0.6.0 and passes now.
- **Guarantees.** Claims, epoch fencing, the CAS on the ticket ref, the
  on-disk format, exit codes and JSON are untouched; the write loop and
  `publishLocal` are the same code. What changes is read semantics: a plain
  `Refresh` on a local store no longer pins a snapshot. A command that does two
  separate reads can see two successive heads if another process publishes in
  between (before: both reads saw the head resolved at refresh). Single reads,
  `ls`, `lint`, `leases` and `next` (one batch read) are consistent; `validate`
  and `inbox` pin. `leases` additionally runs one `git log` per claimed ticket on
  the live ref; a tear there can only affect the "quiet" hint.
- A local read now runs git inside the user's repository instead of a private
  worktree. Reads use no index and take no lock (`show`, `ls-tree`, `cat-file`,
  `grep`, `log`), so they cannot disturb the user's checkout; they also see the
  ticket ref without waiting for the private copy.
- `ReadFile` on a missing local branch reports `ErrNoBranch` only after the
  verifying `rev-parse`; the CLI maps both errors to exit 10 as before.
- `dk doctor` and `dk validate` on a remote store push-dry-run and read from
  `FETCH_HEAD` instead of the reset `HEAD`.
- Not changed, noted for later (roadmap R-28, R-29): the write path could move
  to plumbing for local stores: temporary index (`GIT_INDEX_FILE`) +
  `read-tree <base>`, `hash-object -w`, `update-index --cacheinfo`,
  `write-tree`, `commit-tree -p <base>`, `update-ref <ref> <new> <base>`. That is
  about six processes against twelve today and needs no private working copy,
  so it looks feasible, and `commit-tree` runs no hooks. It changes the
  publish and recovery code of ADR-0010 (the checked-out branch case still
  needs `merge --ff-only`) and signing behaviour, so it is a separate decision.
