# ADR-0001: Claims are decided by a Git push

- Status: accepted
- Date: 2026-11-02

## Context
Several machines, runners and humans must not work on the same ticket at once.
A separate lock service would mean a server to run, secure and keep available.
Git rejects a push that is not a fast-forward, atomically per ref.

## Options considered
1. Lock service or database next to Git — strong guarantees and fast, but a
   second system to operate and a second source of truth.
2. Push as compare-and-swap on one ref — no extra system, readable history;
   cost is retries under contention and online-only writes.
3. One ref per ticket — less contention, but many refs and hosts that handle
   them badly; harder to read for people.

## Decision
Claims, ID allocation and all writes are pushes to one branch (`tickets`)
with a fetch, check, commit, push, retry loop. No rebase, no extra server.

## Consequences
Easier: no server, one source of truth, plain Markdown history.
Harder: contention is bounded only by retries; offline writing is not
possible. Revisit if the concurrency spike (R-1) shows unacceptable rejects;
the deferred "lease service" in docs/CONCEPT.md is the way out.
