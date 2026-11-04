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

## Consistency and trust

The accepted commits on the ticket ref have one total order; the accepted
remote push or local fast-forward ref update is the write's linearization
point. Reads are immutable snapshots and can become stale immediately. There is no atomic transaction
across `main` and `tickets`, across repositories, or across external side
effects. Leases additionally depend on local clocks.

Repository writers are trusted collaborators. Authentication and branch
authorization belong to the Git host. dricket validates document integrity
and legal state succession; it does not prove actor truthfulness. Epoch
fencing stops compliant stale writers, not arbitrary external side effects.

## Ticket layout

```
tickets/<prefix>/<shard>/<id>/
  ticket.md
  comments/
    2026-11-03T10-04-24Z-henrik.md
    2026-11-03T11-30-00Z-bob.md
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

Field rules are structural, not per-field authorization:

| Rule | Fields |
|---|---|
| immutable identity | `id`, `created_at`, `created_by`, `schema_version`; `id` equals the path |
| claim and lease invariants | `owner`, `kind`, `size`, `claimed_at`, `lease_until`, `epoch`, `rev`, `extensions` |
| editable content and workflow | `stage`, `title`, `labels`, `depends`, `parent`, `touches`, `branch`, `repos`, `priority`, and the body |
| extension fields | `x-<name>` (`^x-[a-z0-9][a-z0-9-]*$`): single line, at most 1 000 bytes, secret-scanned, never interpreted, preserved in order on every write (ADR-0011) |

Timestamps are RFC 3339 in UTC. Empty values are written as `key:` with
nothing after the colon (an unclaimed ticket has an empty `owner`).
Extension fields are the supported place for consumer metadata; other unknown keys are warnings, never errors: newer tools must not break older
repos, and tool writes preserve unknown fields.

The body carries three fixed headings: *Description*, *Result* and *Log*.
The log uses one line per entry (`- <timestamp> <actor> <event>`), but is
human-readable information, not a machine-parsed event protocol.

Comments are separate add-only files. Current comments carry `ticket`,
`author`, `created_at` and a body; legacy numbered names retain their
`number` field. Anyone may add a comment, claim or not; nobody edits or
deletes one. Adding a comment does not change the ticket's `rev`.

## Version tokens

- `rev` is the public optimistic ticket version and rises by exactly one
  with every successor of `ticket.md`; `--if-version` compares it.
- `epoch` is the fencing token: it rises by one on every claim or takeover
  and never decreases, also not on release. A writer whose epoch no longer
  matches has lost the claim (exit 13).
- Blob SHA is storage metadata, not a second public version token.

## Write protocol

Every write publishes one commit on the `tickets` branch. Never a rebase. The
common loop is:

1. Refresh the ticket head. Remote and bare stores fetch it; a local non-bare
   store resolves its local ref to a commit SHA.
2. Reset only the private working copy to that immutable base. Local attempts
   read private `HEAD`, never the moving public ref.
3. Check the preconditions **on that snapshot** (ticket free, owner and epoch as
   expected, path not taken, comment name free).
4. Apply the mutation; exactly the files it wrote are staged.
5. Commit with the actor's Git identity and record the candidate SHA.
6. Publish by fast-forward as described below.
7. On a concurrent-head rejection, back off and start over at step 1. Definite
   rights, protection or local checkout failures do not retry.

Remote and bare stores push `HEAD` to `refs/heads/<branch>`. A transport failure
can leave the outcome unknown: fetch and check whether the recorded commit is
an ancestor of the remote head. Return success if it is, definite failure if a
complete fetch proves absence, and otherwise error kind `outcome-unknown` with
exit 15. Never blindly replay a mutation after an unknown outcome.

A non-bare filesystem store uses a private detached linked worktree of the same
repository. If the ticket ref is not checked out, publication is an atomic
`update-ref <ref> <candidate> <base>` compare-and-swap. If it is checked out,
ordinary writes use `merge --ff-only <candidate>` in that checkout, updating
ref, index and working tree together. A non-blocking OS file lock in the common
Git directory serializes local publishers and is released on process exit, so
another publisher's files cannot appear before its ref update. Pre-existing
conflicting dirty paths
return `conflict`/exit 12 immediately and remain untouched. Non-conflicting
dirty paths, including a ticket edit during a comment write, survive. Dirty
paths captured at refresh distinguish user conflicts from files created by a
concurrent publisher; the latter are retryable races. Inconclusive local results
are reconciled against candidate ancestry, never replayed blindly.

Retry starting values (configuration, not constants): at most 8 attempts,
backoff 200 ms doubling per attempt, capped at 5 s, jitter between 0.5 and
1.5, total budget 30 s.

The private working copy lives in the tool's data directory outside any
project; it is disposable. Each profile gets its own. A user checkout with
uncommitted edits is not disposable and is never reset or cleaned implicitly.

## Whole-ticket editing

The normal workflow is `claim`, `sync`, an ordinary editor, and `commit`:

- `sync` runs inside an existing ticket checkout. Remote checkouts fetch and
  fast-forward; local stores already share the ref and need no fetch. Dirty or
  non-fast-forward state is refused.
- `restore <path>` discards local changes to one safe in-repo path, like
  `git restore`; it does not fetch.
- `commit <id>` reads the ticket at local `HEAD` as the exact edit base and
  the working-tree file as the edited candidate. On every CAS attempt the fresh
  store ticket must still equal that base, and the caller must own the claim.
  The command preserves identity and claim fields, sets `rev = fresh rev + 1`,
  validates the complete transition and commits through the private store.
- Only after accepted publication does `commit` update the user checkout, and
  only when the published file is still the checkout's only dirty path. Local
  whole-ticket publication deliberately uses expected-old ref CAS before this
  guarded adoption, because the edited file is intentionally dirty. A change to
  the same ticket is exit 12. Comments and unrelated tickets can advance
  without causing that conflict.

## Writes without a checkout

`set`, `put` and `move` write a ticket without `claim`, `sync` and an exact
edit base (ADR-0011). Each is one commit with `rev` + 1, retried from a fresh
base like every write.

- `set <id> key=value...` changes free fields and `x-` fields. Several pairs
  are one commit. An empty `x-` value removes the key. A ticket claimed by
  someone else is refused (exit 11).
- `put <id> (-F <file> | -)` replaces the whole ticket. Identity and claim
  fields (`owner`, `kind`, `size`, `claimed_at`, `lease_until`, `epoch`,
  `extensions`) must equal the current ticket, `rev` in the document is
  ignored, CRLF is normalised to LF, the secret scan and the size limit
  (body 100 000 bytes) apply and the whole transition is linted. A document
  equal to the current ticket is a no-op without a commit.
- `move <id> --stage <s> [-m note]` changes the stage and appends one log line.
  Moving to the current stage is a no-op.

Who may call `put` and `move`: the claim holder; on an unclaimed ticket a
caller that declares `--kind human` or is listed in `security.admins` (an
agent gets exit 17); on a ticket claimed by someone else nobody (exit 11),
except an admin with `--force` (exit 17 without admin). A forced `put` keeps
the claim fields (the claimant's next exact-base `commit` fails with exit 12);
a forced `move` clears the claim, like `force-release`, and keeps the epoch.
`--if-version N` compares `rev` (exit 12 on mismatch); it is required unless
the caller holds the claim. `0` is a valid version.

### Command results (`--json`, inside the `data` envelope)

| Command | `data` |
|---|---|
| `set` with one pair | `{"id","key","value","rev","fields":{key:value}}` |
| `set` with several pairs | `{"id","rev","fields":{key:value,...}}` |
| `put` | `{"id","rev","commit","changed_fields":["x-persona","body"]}`; `commit` is the head after the write, `changed_fields` lists header keys (`rev` excluded) in canonical order, then `"body"`; `[]` for a no-op |
| `move` | `{"id","stage","from","rev"}` |

## Id allocation

`add` computes N = highest existing number of the prefix + 1 on the fresh
head, inside the precondition of the write above. If two adds race, one
fast-forward publication wins; the other recomputes N on its next attempt and
takes the next number. Collisions are impossible; gaps can happen.

## Configuration (dricket.toml)

`dricket.toml` lives on the protected `main` branch and is maintained by
admins through pull requests — never through `dk`. Local non-bare stores read
the local main ref directly. Remote and bare stores fetch `main` into the
private local ref `refs/dricket/config` (with `--no-write-fetch-head`, so the
ticket protocol's `FETCH_HEAD` is never touched). Config is cached for the
process. Defaults apply when main or the file is absent; a broken file is a
hard error for every operation that needs limits, and a `config-invalid`
finding in `validate` (which keeps working with defaults).

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

[stages]                  # optional; replaces the built-in seven stages
values = ["inbox", "prioritized", "build", "commit", "done"]   # ordered, required
initial = "inbox"         # stage of a new ticket (default: first value)
terminal = ["done"]       # satisfy `depends`; `next` skips them
claim = ["build", "commit"]   # a claim is only valid here (required)
cancelled = "cancelled"   # optional reserved stage, always valid, terminal

[transitions.agent]       # legacy command policy; not an integrity rule
open = ["in-progress", "blocked", "needs-input"]
in-progress = ["review", "blocked", "needs-input", "open"]
blocked = ["in-progress", "open"]
needs-input = ["in-progress", "open"]
review = ["in-progress"]
```

Parsing is strict: unknown keys, unknown stages, non-positive durations
and a wrong `schema_version` are errors with the line named. Legacy
`[transitions.agent]` remains parseable for compatibility but does not
authorize or invalidate repository writes (ADR-0009). Priorities
replace the starting set as a whole; the default must be one of the
values, and the starting set (critical, high, normal, low, default
normal) applies without a `[priorities]` table (ADR-0005). The lease is
the size value raised to `lease_min` and capped at `lease_max`; a claim
made before a tightening keeps its old lease until the next claim or
extend, and `validate` then reports `lease-too-long`.

Without `[stages]` the stages are `open, in-progress, review, done, blocked,
needs-input, cancelled` and `in-progress` means claimed. With it, a stage name
is a lower-case word of letters, digits and inner dashes, every stage must be
listed in `values` (except `cancelled`), a claim moves a ticket into the first
`claim` stage when it is elsewhere, an unclaimed ticket may sit in a claim
stage (that is what `next` offers), `release` needs `--stage`, and the default
agent table lets an agent move between non-terminal stages only. `validate`
checks the current snapshot against the list but not the stage names inside
replayed history (ADR-0011).

`grace` is deliberately not configurable: it is part of the fencing
model and changes only with an ADR. See ADR-0004 for the decisions
behind this file and the TOML library.

## Resolving the store and project

Inside the ticket repository no configuration is needed: the tool walks up to
the actual Git working-tree root and reads committed `main:dricket.toml`. The
current-checkout file is only a fallback when main has no config. This works on
main, on the ticket branch and from nested ticket directories. Discovery takes
the origin URL when present, or the directory itself otherwise; the ticket
branch comes from `branch` (default `tickets`), and a single registered prefix
resolves the project. An origin deliberately selects remote CAS; a discovered
non-bare directory without one selects local CAS. Explicit bare paths always
keep push CAS (ADR-0010).

In a code repository, `dk use` writes the bindings into a `.dricket`
file of that directory:

```console
dk use --store /path/to/store#tickets   # bind the ticket store
dk use app                              # bind the project prefix
dk use                                  # print the resolution and its sources
```

The file is local state: commit it for a team convention, or git-ignore
it when it carries machine-specific paths.

Precedence — store: `--store`, then `DK_STORE`, then the nearest
`.dricket` store key, then the nearest store root. Prefix (only `add`
needs one): `--prefix`/`-p`, then `DK_PROJECT`, then the `.dricket`
prefix key, then the single registered prefix of `prefixes.toml`;
without any of them `add` fails with a hint (ADR-0008).

## Store setup and profiles

- `dk init [dir-or-url]` sets up a store the way `git init` sets up a
  repository: in a directory it commits the scaffold on `main`, seeds the
  ticket branch and leaves that branch checked out. Commands then work there
  without flags or bindings. A remote URL gets both branches pushed. Existing
  repositories and existing branches are refused — the scaffold never
  overwrites anything. The scaffold carries dricket.toml with the defaults,
  prefixes.toml, README.md, AGENTS.md, CLAUDE.md and a `.gitattributes` that
  normalizes line endings.
- `dk setup-agent` writes the store/guide pointer into the current project's
  AGENTS.md and installs the embedded canonical skill at
  `.agents/skills/dricket/SKILL.md`. It works offline, is idempotent and
  preflights both outputs. A differing existing skill is refused without any
  write; user content is never overwritten. JSON retains `file`/`written` and
  adds `skill_file`/`skill_written`.
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

- `extend <id> -m <reason>` lengthens the lease of a ticket its owner still
  holds: `lease_until = now + lease(size)`, `extensions` grows by one,
  `rev` by one, the reason lands in the log. At most 3 extends per claim
  (starting value); the reason is required.
- `note <id> -m <text>` appends a log entry and keeps the claim with its owner.
  Whole-ticket content editing uses the claim → sync → editor → commit
  workflow.
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
| 15 | definite network/push failure or `outcome-unknown` after reconciliation remains inconclusive |
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
  id its path says; comments carry matching name, ticket and author (plus
  matching `number` for legacy comments); `depends` and `parent` exist; the
  dependency graph has no cycles; the granted lease of every claim is within
  `limits.lease_max` from dricket.toml;
- history replay: identity fields never changed, `rev` rose by exactly one
  per ticket-file successor, `epoch` never decreased, claims and takeovers
  raised it by one, same-owner edits kept it, releases did not reset it,
  takeovers recorded a time after the previous lease plus grace, comments
  stayed add-only, nothing was deleted, and v1 writes touched one ticket
  per commit. Stage enums are validated, but role and command origin are
  not integrity properties. Recorded timestamps provide consistency
  evidence, not proof of real time or actor identity.

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
- **force-release.** `dk force-release <id> -m "..."` frees a
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
  expired past the grace period (a takeover candidate), with every entry in
  `depends` in `done` or `cancelled`. `parent` is hierarchy only and never
  blocks eligibility. Highest priority first (the `[priorities].values`
  order), then lowest number, and exit
  10 when nothing qualifies (ADR-0006). `--claim` takes it
  directly, `--size` sets the lease size, `-p` restricts the prefix.
- `search <query>` is a fixed-string, case-insensitive full-text search
  over the ticket files of the fetched head (`git grep`; `ls` filters by
  field, search reads the text). `-p` restricts the prefix. Exit 1 when
  nothing matches, like grep.

## Environment

- `DK_STORE`: the store as `remote[#branch]`; `--store` overrides
  discovery.
- `DK_OWNER`: the owner id of this process (default `user@host`).
- `DK_PROJECT`: the project prefix for `add` (overrides bindings and
  the single registered prefix; `--prefix` wins over the env).
- `DK_DATA_DIR`: where private working copies live (default: the user
  cache directory).
- `DK_PROFILE`: profile selection (with stage v2 profiles).
- `DK_CONFIG`: path of the user config file with the profiles (default:
  `<user config dir>/dricket/config.toml`).
