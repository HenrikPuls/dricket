# ADR-0009: Collaborative whole-ticket editing

- Status: accepted
- Date: 2026-11-03

## Context

Writers who may push to the ticket repository are trusted collaborators.
Per-field write permissions add no security boundary, while requiring a
separate command for every field makes ordinary Markdown editing
unnecessarily indirect. The claim still needs to protect coordination, and
manual edits still need safe publication through the single Git ref.

## Options considered

1. Tool commands and per-field permissions only — precise mutations, but
   redundant authorization and no ordinary whole-file editing workflow.
2. Unrestricted file writes without transition checks — simple, but malformed
   documents and accidental identity or fencing changes can enter the store.
3. Whole-file editing with structural validation and exact-base CAS — direct
   editing, while identity, revisions, claims and comments keep their
   integrity rules.

## Decision

The normal editing flow is `claim`, `sync`, an editor, and `commit`.
`sync` fast-forwards a clean ticket checkout, `restore` discards selected
local edits without fetching, and `commit` validates and publishes one
claimed ticket. `dk edit` is not provided; `set` remains a targeted
convenience operation rather than a permission mechanism.

Validation checks document shape and legal state succession. Identity fields
are immutable, `rev` increases by one per ticket-file successor, `epoch`
never decreases, and claim/takeover increments it. Role and command origin
are not integrity rules. `rev` is the only public optimistic ticket-version
token. Unknown front-matter fields are preserved across tool writes.

Comments remain separate add-only files. Anyone can add one during a claim;
a comment does not change `rev` and does not conflict with an unchanged
`ticket.md`. The human-readable log is not parsed as an event protocol.

A transport failure after a push can leave its outcome unknown. The writer
records the commit SHA and reconciles it against the remote history; an
inconclusive result uses error kind `outcome-unknown` and exit 15, never a
blind mutation replay.

## Consequences

A user checkout with uncommitted edits contains unique user data and is not
disposable. `dk commit` compares the fresh remote ticket byte-for-byte with
the edit base, writes through a clean private store, and updates the user
checkout only after an accepted push. Comments and unrelated ticket writes
can advance the ref without invalidating the edit; a changed `ticket.md`
causes a conflict.

Per-field authorization and `dk edit` are rejected. No event store, general
schema DSL, or additional version token is introduced. Public documentation
and the landing page are updated only after the workflow is verified.
