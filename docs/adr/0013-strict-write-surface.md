# ADR-0013: Strict write surface — put --claim and commit --release

- Status: accepted
- Date: 2026-11-04

## Context

dricket's promise is "no write without a claim". ADR-0011 broke it for the
frontend's handgrips: `put` and `move` on an unclaimed ticket were allowed
for a self-declared human or an admin. ADR-0012 then showed the write can
carry its own claim (`--claim`, a short claim inside the one commit), so
the exception is no longer needed. The owner decided (requirement R-25):
keep the promise without an exception for anyone, and keep the write
surface as small as possible.

## Options considered

1. Keep `set`, `put` and `move` with the human/admin exception — smallest
   code delta, but the promise keeps a permanent hole and three commands
   overlap (a stage change is a one-line `put`).
2. Strict surface: remove `set` and `move`, make `put` the only writer
   without a checkout, require a claim for every write — the claim holder's
   own, a short claim via `put --claim`, or an admin's `--force` — and add
   `commit --release` so the checkout flow can also write and free in one
   commit. Chosen.

## Decision

**Removed commands.** `set` and `move` are gone. A field change is a
one-line `put`; a stage change is a `put` of the document with the new
`stage:` line; both go through the same checks. `add --set` keeps creating
tickets with extension fields.

**`dk put` is strict.** On an unclaimed ticket `put` needs `--claim`
(exit 17 otherwise — no human/admin exception); the claim holder writes
without it; a foreign claim is exit 11 as ever, with an admin's `--force`
as the only emergency path. The self-declared `--kind` is gone with the
exception it served, and `put` validates the priority value like every
other write.

**`dk commit --release [--stage S] [-m note]`.** Writes the edited ticket
and frees the claim in one commit, with the same exact-base, owner and
epoch checks as `commit`, and the release transition policy of the claim's
kind for the target stage (default `review` with the built-in stages;
`--stage` is required with a configured vocabulary). The `epoch` stays (a
release never changes it), no lease times are written, and one log line
`commit-release <id> -> <stage>` records the freeing. The result adds
`"stage"`.

This supersedes the `put`/`move` permission model of ADR-0011 and narrows
ADR-0012: the short claim lives on `put` only.

## Consequences

- The promise "no write without a claim" holds for every command; the only
  exception path left is the auditable admin `--force`.
- One writer without a checkout instead of three; frontends build field
  and stage edits as small whole-ticket `put` documents.
- Breaking change: `dk set` and `dk move` exit with usage error; callers
  migrate to `put --claim`. Shipped with v0.6.0.
