# dricket protocol — on-disk format and write protocol

Version 1 · 2 November 2026 · License: GPL-3.0-or-later

This document specifies what dricket stores in Git and how every write
happens, independently of the Go implementation. The specification of the
product is [CONCEPT.md](CONCEPT.md); this is the contract consumers and
frontends can rely on.

## Storage

A dricket store is a Git repository with two branches:

- `main` is protected: `dricket.toml`, `prefixes.toml`, README, AGENTS.md.
  Configuration on `main` arrives with stage v2.
- `tickets` holds the tickets. Its history is linear and fast-forward only;
  no rebase, no force-push, no merges.

The storage triple is `(remote, branch, path)`. The default branch is
`tickets`; the default path prefix is `tickets/`.

## Ticket layout

```
tickets/<prefix>/<shard>/<id>/
  ticket.md
  comments/
    0001.md
    0002.md
```

- `<id>` is `PREFIX-N`, lower case, for example `app-6842`. The prefix
  belongs to one project; the number grows. Comparison is
  case-insensitive, storage is lower case.
- `<shard>` is N÷1000 in four digits (`0006` for 6842). The path never
  changes; the stage lives in the front matter.
- Numbers are never reused; gaps are fine. Tickets are never deleted;
  discarding means `stage: cancelled`.

## Ticket file format

`ticket.md` starts with a flat front-matter block: the first `---` line
opens it, the next `---` line closes it. One `key: value` per line, no
nesting, no duplicate keys, no tabs in keys, no multi-line values, no
control characters. Everything after the closing `---` is the body.

Fields and their classes:

| Class | Fields | Who may change |
|---|---|---|
| identity | `id`, `created_at`, `created_by`, `schema_version` | nobody after creation; `id` equals the path |
| state | `stage`, `owner`, `kind`, `size`, `claimed_at`, `lease_until`, `epoch`, `rev`, `extensions` | only through dricket commands |
| free | `title`, `labels`, `depends`, `parent`, `touches`, `branch`, `repos` | the owner or a human, validated |

Timestamps are RFC 3339 in UTC. Empty values are written as `key:` with
nothing after the colon (an unclaimed ticket has an empty `owner`).
Unknown keys are warnings, never errors: newer tools must not break older
repos.

The body carries three fixed headings: *Description* (free text), *Result*
(written by `release --result`), *Log* (append-only, written by the tool
only, one line per event: `- <timestamp> <actor> <event>`).

Comments are separate add-only files. Each has a small front matter with
`ticket`, `number`, `author`, `created_at` and a body with the text.
Anyone may add a comment, claim or not; nobody edits or deletes one.

## Version tokens

- `rev` rises by exactly one with every write to the ticket file.
- `epoch` is the fencing token: it rises by one on every claim or takeover
  and never decreases, also not on release.
- `--if-version` compares `rev` (optimistic writes, exit 12). A writer
  whose `epoch` no longer matches has lost the claim (exit 13).

## Write protocol

Every write is one push to the `tickets` branch. Never a rebase:

1. `git fetch` the ticket branch.
2. Reset the private working copy to the fetched head.
3. Check the preconditions **on that fresh head** (ticket free, owner and
   epoch as expected, path not taken, comment number free).
4. Apply the mutation to the working copy; exactly the files the mutation
   wrote are staged.
5. Commit with the actor's git identity.
6. Push `HEAD` to `refs/heads/<branch>`.
7. If the push was rejected because someone else pushed first —
   non-fast-forward, fetch-first, or the server-side `cannot lock ref`
   race — back off and start over at step 1.
8. Any other push failure (rights, protection, network) ends the write
   immediately: retrying cannot help.

Retry starting values (configuration, not constants): at most 8 attempts,
backoff 200 ms doubling per attempt, capped at 5 s, jitter between 0.5 and
1.5, total budget 30 s.

The private working copy lives in the tool's data directory outside any
project; it is disposable. Each profile gets its own.

## Id allocation

`add` computes N = highest existing number of the prefix + 1 on the fresh
head, inside the precondition of the write above. If two adds race, one
push wins; the other recomputes N on its next attempt and takes the next
number. Collisions are impossible; gaps can happen.

## Claims

- `claim` sets `owner`, `kind`, `size`, `claimed_at`, `lease_until`,
  raises `epoch` by one and `rev` by one. Only the push decides.
- Lease starting values by size: S 2 h, M 8 h, L 24 h; the takeover grace
  is 20 minutes (all configuration values).
- After `lease_until` plus grace another actor may take over: a claim that
  raises `epoch` again and logs `takeover`.
- `claim` on a ticket the caller already owns succeeds without a commit
  (idempotent); so does `release` on an unclaimed ticket.
- Comments never touch `ticket.md`; third parties never write into a
  claimed ticket file.

## Command output

stdout is data, stderr is messages. With `--json` every command prints one
object:

```json
{"schema_version": 1, "data": { ... }}
```

On failure:

```json
{"schema_version": 1, "error": {"kind": "<machine-readable>", "exit_code": 11, "message": "..."}}
```

The JSON `schema_version` is the envelope version, independent of the
ticket file `schema_version` (currently both 1).

## Exit codes

| Code | Meaning |
|---|---|
| 0 | ok |
| 1 | general error |
| 2 | usage error |
| 10 | not found |
| 11 | already claimed by someone else |
| 12 | race lost or version conflict (`--if-version`) |
| 13 | claim lost (lease taken over), own changes saved |
| 14 | invalid transition or validation error |
| 15 | network or push failure after all attempts |
| 16 | tool too old or unknown schema version |
| 20 | `leases --check` found expired leases (stage v1) |

## Environment

- `DK_STORE`: the store as `remote[#branch]`; `--store` overrides.
- `DK_OWNER`: the owner id of this process (default `user@host`).
- `DK_DATA_DIR`: where private working copies live (default: the user
  cache directory).
- `DK_PROFILE`: profile selection (with stage v2 profiles).
