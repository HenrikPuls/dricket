# ADR-0011: Extension fields, configurable stages, whole-ticket put and move

- Status: accepted (the `put`/`move` permission model and `move` itself are
  superseded by ADR-0013)
- Date: 2026-11-04

## Context

A frontend that keeps its workflow in dricket (owner request, requirements
R-01 to R-04 of the Drey integration) needs four things the tool did not
offer. It stores its own metadata in the ticket header, and today only the
eight free fields can be set. It edits a whole ticket without a checkout
and without being the claimant. Its workflow has its own stage names. And a
person drags a ticket to another stage while nobody works on it, which cost
a claim and a release (two commits, two epoch bumps).

## Decision

**Extension fields.** The key namespace `x-<name>` (`^x-[a-z0-9][a-z0-9-]*$`)
is a fourth field class. Values are single-line, at most 1 000 bytes and
secret-scanned; dricket never interprets them and preserves them, in order,
on every write. `set` takes several `key=value` pairs and writes them in one
commit with one `rev` bump; an empty value removes an extension. `add --set`
creates a ticket with extensions.

**Configurable stages.** `[stages]` in `dricket.toml` replaces the built-in
seven stages: `values` (ordered), `initial`, `terminal`, `claim` and an
optional reserved `cancelled`. Without the table nothing changes. A custom
vocabulary has these consequences: a claim is only valid in a `claim` stage
and moves a ticket into the first one when it is elsewhere; `next` offers
unclaimed tickets in a claim stage; terminal stages and `cancelled` satisfy
`depends`; the legacy coupling "in-progress means claimed" does not apply
(an unclaimed ticket may wait in a claim stage); `release` needs an explicit
`--stage`; the default agent transition table becomes "any non-terminal
stage to any other non-terminal stage" until `[transitions.agent]` says
otherwise. History replay in `validate` does not check stage membership, so
editing the list later cannot invalidate old commits; the current snapshot
is checked against the current list.

**`dk put`.** Publishes the complete `ticket.md` text as the new content,
with the checks of `commit` (identity and claim fields unchanged, `rev` =
fresh + 1, secret scan, size limit) but without the exact-base compare.
Allowed for the claim holder, for a caller that declares `--kind human` or
is in `security.admins` when the ticket is unclaimed, and for an admin with
`--force` on someone else's claim. `--if-version` is required unless the
caller holds the claim. The document is normalised to LF.

**`dk move`.** Changes only the stage, in one commit: `rev` + 1, `epoch`
unchanged, one log line. Same permissions as `put`; `--force` by an admin on
someone else's claim also clears the claim (force-release plus move).

The caller's kind is self-declared (`--kind`, like `claim --kind`): roles
are detected, not enforced, in line with ADR-0009. Repository access stays
the real authorization.

## Consequences

- `ticket.md` gains an optional header namespace; older tools keep the
  fields (unknown keys are preserved) and warn about them.
- Two new commands and the `--set` flag are public interface; `set --json`
  keeps its single-pair shape and adds `fields`.
- A store with a custom vocabulary requires a tool with `[stages]` support;
  set `min_version` when adopting it.
- Not decided here: `add` from a complete document, import with preserved
  ids, a change feed. They are separate roadmap items.
