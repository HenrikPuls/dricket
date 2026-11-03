# ADR-0007: Comments are named by timestamp and author

- Status: accepted
- Date: 2026-11-03

## Context

Comment files were named `comments/NNNN.md` with a running number
allocated per ticket by push retry. Numbers are order data the reader
does not need — the file name already carries the author and (as a file
name) should carry the time — and a counter is one more piece of state
that inbox markers and validators have to interpret.

## Decision

Comment files are named `<created_at UTC>-<author>.md`, with the
timestamp formatted `2006-01-02T15-04-05Z` (colons replaced by dashes,
so the name is safe on Windows). The front matter carries `ticket`,
`author` and `created_at`; the `number` field is dropped. The only
possible collision — the same author writing twice in the same second —
is resolved inside the write precondition by trying the next second
(up to eight tries), so no extra push is needed.

`validate` accepts the v0.1 numbered names in older stores when their
`number` field matches the file name, and checks that v0.2 file names
mirror their front matter (`comment-filename-mismatch` otherwise).
An unset priority stays a warning; a comment without `created_at`
cannot be named consistently and is an error.

## Consequences

- The inbox marker stores the file names already seen instead of a
  count; a stale numeric marker resets once, reporting every comment
  as new one time.
- Lexical file-name order is chronological order (same format), which
  `inbox` relies on for display.
- Everything else is unchanged: comments stay add-only, one writer per
  file, edits and deletions are findings (exit 14).
