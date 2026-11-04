# ADR-0012: One-step change under a short claim

- Status: accepted (narrowed by ADR-0013: the short claim lives on `put`
  only, and `commit --release` was built after all)
- Date: 2026-11-04

## Context

dricket's promise is "no write without a claim". ADR-0011 had to break it
for the frontend's handgrips: `put` and `move` on an unclaimed ticket are
allowed for a self-declared human or an admin, because the classic flow
(claim → write → release) is three commits and three remote round trips
for one small edit (owner requirement R-24). The owner would rather keep
the promise without an exception for humans and admins — if one change
can happen in one commit under a claim.

## Decision

`set`, `put` and `move` accept `--claim`: the claim is taken and released
inside the one commit of the write (a "short claim"). Rules:

- The ticket must be unclaimed. A foreign claim is refused exactly like
  before (exit 11, with the lease in the message); the caller's own claim
  is exit 14 (a real claim needs no short one); `--claim` and `--force`
  exclude each other — `--force` remains the exception path.
- Under a short claim every kind may write, agent or human: the claim
  replaces the human/admin permission of a plain `put` or `move`, so the
  promise holds without an exception.
- The `epoch` does not rise. Claim and release never land in a committed
  state, so in every committed state the owner is unchanged — and the
  transition invariant "epoch must stay unchanged while the owner stays"
  (`ticket.ValidateTransitionWith`, replayed by `dk validate`) forbids the
  bump. It is also unnecessary: the single CAS commit is the mutual
  exclusion; there is no window a fencing token would have to close.
- No `claimed_at`/`lease_until` is ever written, so there is no lease
  clock to read. `set` and `move` mark their log line with
  "(short claim)" — the line's timestamp is claim and release time at
  once; `put` keeps the document byte-exact and marks the commit message
  instead.
- `move --claim` applies the transition policy of the caller's `--kind`,
  like the kind of a real claim would. The atomic form skips the
  intermediate claim stage, so on the legacy vocabulary an agent cannot
  reach `review` from `open` this way (the real path passes through
  `in-progress`); an unclaimed ticket can never land in `in-progress`,
  because the lint of the written state applies unchanged.
- The version guard is unchanged: `--if-version` compares `rev`, and
  `put --claim` still requires it (the caller holds no committed claim).
- Results add `"short_claim": true`.

`dk commit --release` (write and release in one commit on a checkout) was
considered and is feasible with the same checks as `commit`, but it needs
a checkout and an already held claim — it does not serve the frontend
writes this decision is about. Not built.

## Consequences

- For the frontend's handgrips (fields, stage, whole-ticket sections on
  an unclaimed ticket) `put` and `move` without `--claim` are no longer
  needed; they stay shipped until the owner decides (R-24).
- The permission model of `put`/`move` gains a claim-shaped path with no
  role exception; kind is still self-declared, authorization still lives
  with repository access (ADR-0009, ADR-0011).
- History written under short claims replays clean under `dk validate`;
  a test proves it.
