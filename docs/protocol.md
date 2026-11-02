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

## Configuration (dricket.toml)

`dricket.toml` lives on the protected `main` branch and is maintained by
admins through pull requests — never through `dk`. The tool fetches
`main` into the private local ref `refs/dricket/config` (with
`--no-write-fetch-head`, so the ticket protocol's `FETCH_HEAD` is never
touched) and caches it for the process. Defaults apply when the remote
has no `main` branch or no file; a broken file is a hard error for every
operation that needs limits, and a `config-invalid` finding in
`validate` (which keeps working with defaults).

```toml
schema_version = 1        # this tool reads 1
min_version = "0.2.0"     # optional; an older tool exits 16 ("dev" skips)

[limits]
lease_s = "2h"            # starting leases by size
lease_m = "8h"
lease_l = "24h"
lease_min = "15m"         # clamps every granted lease
lease_max = "72h"         # clamps claims and bounds validate
max_extensions = 3

[security]
admins = ["henrik"]       # force-release; an empty list means nobody
quota_tickets_per_day = 50   # new tickets per owner and day; 0 disables
secret_scan = true        # refuse likely credentials before they land

[priorities]
values = ["critical", "high", "normal", "low"]   # allowed values of the
default = "normal"        # free field `priority`; fills `add` without --priority

[transitions.agent]       # agent stage moves; human/admin are fixed
open = ["in-progress", "blocked", "needs-input"]
in-progress = ["review", "blocked", "needs-input", "open"]
blocked = ["in-progress", "open"]
needs-input = ["in-progress", "open"]
review = ["in-progress"]
```

Parsing is strict: unknown keys, unknown stages, non-positive durations
and a wrong `schema_version` are errors with the line named. Missing
roles or stages in `[transitions.agent]` keep the defaults shown above;
an explicit empty list means "no moves from this stage". Priorities
replace the starting set as a whole; the default must be one of the
values, and the starting set (critical, high, normal, low, default
normal) applies without a `[priorities]` table (ADR-0005). The lease is
the size value raised to `lease_min` and capped at `lease_max`; a claim
made before a tightening keeps its old lease until the next claim or
extend, and `validate` then reports `lease-too-long`.

`grace` is deliberately not configurable: it is part of the fencing
model and changes only with an ADR. See ADR-0004 for the decisions
behind this file and the TOML library.

## Store setup and profiles

- `dk init [dir-or-url]` sets up a store the way `git init` sets up a
  repository: in a directory it creates a repository with the scaffold
  committed on `main` (checked out, so dricket.toml is directly
  editable) and an empty seed commit on the ticket branch; a remote URL
  gets both branches pushed. Existing repositories and existing branches
  are refused — the scaffold never overwrites anything. The tool then
  works with `--store <dir>#<ticket-branch>`. The scaffold carries
  dricket.toml with the defaults, prefixes.toml, README.md, AGENTS.md,
  CLAUDE.md and a `.gitattributes` that normalizes line endings.
- `dk setup-agent` writes one pointer line into the AGENTS.md of the
  current code repository: which store holds the tickets, and that
  `dk guide` carries the rules. Idempotent; creates AGENTS.md when it is
  missing.
- **Profiles** bundle one actor's identity in the user config
  (`<user config dir>/dricket/config.toml`, outside any repository):

  ```toml
  [profile.orch-1]
  owner = "orch-1.sub-2"
  git_name = "Orch One"
  git_email = "orch-1@invalid"
  ssh_key = "C:/keys/orch_ed25519"   # optional
  ```

  `--profile <name>` or `DK_PROFILE` selects the entry; without one, the
  `[profile.default]` entry or the operating system identity (user@host)
  applies. `DK_OWNER` still overrides the owner id. A profile with an
  `ssh_key` pushes through `GIT_SSH_COMMAND` (with `IdentitiesOnly`) and
  is isolated from the user's global git configuration through
  `GIT_CONFIG_GLOBAL`, so nothing leaks in from the surrounding setup;
  plain profiles keep the ambient configuration and credentials. An
  unknown profile name is an error, never a silent fallback. Profiles
  are organisation, not security: agents in the same OS user context can
  read each other's key files (docs/CONCEPT.md, "Limit").

## Claims

- `claim` sets `owner`, `kind`, `size`, `claimed_at`, `lease_until`,
  raises `epoch` by one and `rev` by one. Only the push decides.
- Lease starting values by size: S 2 h, M 8 h, L 24 h (dricket.toml
  defaults, clamped by `lease_min`/`lease_max`); the takeover grace is 20
  minutes and deliberately not configurable.
- After `lease_until` plus grace another actor may take over: a claim that
  raises `epoch` again and logs `takeover`.
- `claim` on a ticket the caller already owns succeeds without a commit
  (idempotent); so does `release` on an unclaimed ticket.
- Comments never touch `ticket.md`; third parties never write into a
  claimed ticket file.

## Leases and monitoring

- `extend <id> --reason` lengthens the lease of a ticket its owner still
  holds: `lease_until = now + lease(size)`, `extensions` grows by one,
  `rev` by one, the reason lands in the log. At most 3 extends per claim
  (starting value); the reason is required.
- `note <id> <text>` (and `update <id> --note`) appends a log entry and
  keeps the claim with its owner. Body editing through the tool comes
  later.
- `leases` lists active leases, most urgent first. Status per lease:
  `ok`, `expiring` (less than 20 percent of the lease left), `expired`
  (past `lease_until` plus grace). `quiet` is a separate hint: nothing
  happened to the ticket for more than half its lease. `--expiring` and
  `--stale` filter (`--stale` shows dead claims); `--check` exits with 20
  when expired leases exist, for cron jobs and CI. `ls --stale` applies
  the same dead-claim filter to the ticket list.
- `inbox <id>` reports what is new since this owner last looked: new log
  lines and new comments. The marker is local state per store and owner in
  the tool's data directory (`<workdir>.state/inbox.json`); it is
  disposable and never part of the repository. The first call only sets
  the marker. Polling shortens reaction time; fencing at the next write
  remains the safety net.
- `doctor` checks the setup: git version (minimum 2.32), owner and git
  identity, store configuration, remote reachability, a dry-run push
  (transport and credentials; branch protection is only fully provable on
  a real write) and a writable data directory. Exit 1 when a check fails.

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
| 17 | permission denied (force-release by a non-admin) |
| 18 | quota exceeded |
| 19 | refused write: the secret scan found likely credentials |
| 20 | `leases --check` found expired leases (stage v1) |

## Validation

Two levels (docs/CONCEPT.md): `lint` runs before every tool write on one
file; `validate` is the deep, read-only check for hooks, CI and scheduled
jobs. Exit 14 with the findings, warnings do not fail.

`dk validate` checks the fetched head and replays the complete history:

- current state: every file under `tickets/` parses, passes lint, has the
  id its path says; comments carry matching number, ticket and author;
  `depends` and `parent` exist; the dependency graph has no cycles; the
  granted lease of every claim is within `limits.lease_max` from
  dricket.toml;
- history replay: identity fields never changed, `rev` rose by exactly
  one per write, `epoch` never decreased, every ownership change was a
  claim, a release or a takeover after `lease_until` plus grace, stage
  moves match the transitions of dricket.toml (or the defaults without a
  config), comments stayed add-only, nothing was deleted, one ticket per
  commit.

`dk validate --trailers <git-range>` runs in a code repository and checks
every commit of the range for the dricket trailers (`Dricket-Ticket`,
`Dricket-Epoch`, `Dricket-Op`, `Dricket-Actor`, written by the runner
wrapper, not by dricket): the ticket must exist, the commit's epoch must
not be older than the ticket's current epoch (stale work after a
takeover), and while the ticket is claimed the actor must be its owner.
Every commit in the range must carry the trailers; wrapper branches
satisfy that. This is the merge gate for epoch trailers; without a
host-side gate it stays advisory.

## Protection (stage v2)

All three guards are configured in dricket.toml (see Configuration).

- **Quota.** `add` counts the ticket files this identity added in the
  last 24 hours (commit author identity) and refuses further adds with
  exit 18 over `security.quota_tickets_per_day`. This is abuse
  protection — a buggy agent must not create thousands of tickets — not
  a security boundary: an actor that can forge commits can forge the
  author.
- **Secret scan.** Before every write the tool scans the untrusted text
  (title, description, comment, note, free fields, release result) for
  likely credentials: private key blocks, AWS access key ids, GitHub,
  GitLab, Slack, OpenAI-style and Stripe live tokens. A hit refuses the
  write with exit 19 and names the pattern and the line; the matched
  content is never echoed. The scan is deliberately incomplete and never
  a guarantee — rotate credentials that land in a ticket regardless.
  `validate` scans every ticket and comment file, so anything pushed
  around the tool is still detected.
- **force-release.** `dk force-release <id> --reason "..."` frees a
  ticket without the owner's consent — the runbook answer to dead
  claims. Only owners in `security.admins` may call it; an empty list
  means nobody (fail closed). The reason is mandatory and lands in the
  log. The epoch stays (it is a fencing token), `extensions` resets, and
  the ticket goes to the target stage — default `open`, so `next` picks
  it up again. Force-releasing an already free ticket is idempotent.
- **guide.** `dk guide` prints the agent guide matching the tool
  version; the repo's AGENTS.md only points to it, so they cannot drift.

## Workflow commands

- `next` picks the next suitable ticket: free and open, or its lease
  expired past the grace period (a takeover candidate), with every
  dependency and the parent in `done` or `cancelled`. Highest priority
  first (the `[priorities].values` order), then lowest number, and exit
  10 when nothing qualifies (ADR-0006). `--claim` takes it
  directly, `--size` sets the lease size, `-p` restricts the prefix.
- `search <query>` is a fixed-string, case-insensitive full-text search
  over the ticket files of the fetched head (`git grep`; `ls` filters by
  field, search reads the text). `-p` restricts the prefix. Exit 1 when
  nothing matches, like grep.

## Environment

- `DK_STORE`: the store as `remote[#branch]`; `--store` overrides.
- `DK_OWNER`: the owner id of this process (default `user@host`).
- `DK_DATA_DIR`: where private working copies live (default: the user
  cache directory).
- `DK_PROFILE`: profile selection (with stage v2 profiles).
- `DK_CONFIG`: path of the user config file with the profiles (default:
  `<user config dir>/dricket/config.toml`).
