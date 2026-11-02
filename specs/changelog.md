# Changelog

Newest first. Ids are `R-<n>`, unique across roadmap and changelog.
This is the internal work log. User-facing release notes are in the
top-level CHANGELOG.md and are written at release time from this file.

## Awaiting verification

- Skeleton: R-0 — handed over 2026-11-02
- Infrastructure and Phase 0: public repository published (CI green, rulesets active, Pages live), roadmap and architecture refined — handed over 2026-11-02
- Spike: R-1 — handed over 2026-11-02
- Foundations: R-2 — handed over 2026-11-02
- Store: R-10 — handed over 2026-11-02
- Core commands: R-11 — handed over 2026-11-02
- Leases and checks: R-3 — handed over 2026-11-02
- Configuration core of R-4 (dricket.toml: lease limits with clamping,
  extend limit, transition table, min_version; validate lease_max and
  config findings; release role fixed to come from the ticket's kind;
  first dependency BurntSushi/toml, ADR-0004) — handed over 2026-11-02
- Protection of R-4 (exit codes 17/18/19 per ADR-0004: force-release
  with admins fail-closed, ticket quota, secret scan before writes and
  in validate; prefix-register warnings; `dk guide`) — handed over
  2026-11-02
- Store setup and profiles of R-4 (`dk init` like git init: local
  directory with main checked out and seeded ticket branch, or remote
  URL; `dk setup-agent`; user-config profiles with ssh_key and git
  config isolation; doctor shows the profile) — handed over 2026-11-02
- Priorities (stage v2, owner addition): free field `priority` with the
  configurable starting set critical/high/normal/low (default normal) in
  dricket.toml `[priorities]`; `add --priority`, `set priority=...`,
  columns in ls/next, lint and validate checks; scaffold carries the
  table; ADR-0005 — handed over 2026-11-02
- next orders by priority (owner decision, ADR-0006): the
  `[priorities].values` order ranks tickets, lowest number wins within
  one rank — handed over 2026-11-02

## Shipped

### R-3 Leases and checks (2026-11-02)
Behaviour: stage v1 complete, no on-disk format change, no new exit code
(20 was reserved). `extend --reason` (owner-only, limit 3 as a starting
value, lease_until = now + lease(size), reason in the log); `note` and
`update --note` append log entries and keep the claim; `leases` with
statuses ok / expiring (under 20 percent left) / expired (past lease plus
grace), the separate quiet hint from the ticket's last commit time, and
`--expiring`, `--stale`, `--check` (exit 20 for cron and CI); `ls
--stale` for dead claims; `inbox <id>` with a local, disposable marker
per store and owner in `<workdir>.state`; `doctor` (git at least 2.32,
identity, store, reachability, dry-run push with its honest limits,
writable data dir); `next` (free and open or lease expired, dependencies
done or cancelled, lowest number first, `--claim`, exit 10 when nothing
qualifies); `search` (git grep, fixed string, case-insensitive, exit 1
on no match); `validate` — current state (parse, lint, references,
cycles, comment shape) plus a full history replay (identity immutable,
rev +1 per write, epoch never decreasing, legal ownership transitions
including takeover only after lease plus grace, stage moves per role,
comments add-only, nothing deleted, one ticket per commit) with exit 14;
`validate --trailers <range>` is the epoch-trailer gate for code
repositories (missing trailer, unknown ticket, stale epoch, actor
mismatch) — advisory without a host-side merge gate.
Tests: leases statuses with a controllable clock (expiring, expired,
quiet, filters, exit 20), extend limit and ownership, inbox marker
lifecycle across owners, next eligibility (blocked dependencies, done
dependencies, takeover candidates, prefix filter, exit 10), search hits
in titles and comments; validate against a busy legal history (including
a takeover) stays silent and detects identity rewrites, rev jumps,
illegal agent transitions, tampered and deleted comments (deleted with
plain git, exactly like writing around the tool), dangling dependencies
and cycles; trailer gate with clean, stale, unknown, unattributed and
trailerless commits; CLI end-to-end for all new commands.

### R-11 Core commands (2026-11-02)
Behaviour: `internal/service` implements the stage v0 use cases on top of
store and ticket model: `add` (id allocation by push retry on the fresh
head, claimed or `--unclaimed`, labels/depends/parent), `claim` (sizes
S/M/L with the concept's starting leases, idempotent re-claim, takeover
after lease plus grace with a log line), `release` (stage target, result
into the Result section, idempotent on free tickets), `comment`
(add-only numbered files, allocation by push retry), `set` (free fields
only, `--if-version` conflict check, state fields rejected), `ls`
(stage/owner/@me/prefix filters, warnings for unknown keys), `show`
(raw ticket), `lint` (parse + model findings, exit 14 on errors). Service
errors map to the documented exit codes; epoch never resets after release
(fencing token). CLI wiring for all eight commands with the JSON envelope;
`DK_STORE`, `DK_OWNER`, `DK_DATA_DIR` environment. Working-copy directory
names gained an fnv hash suffix after a real collision between long temp
paths; WriteSpec lost its Paths field (staging tracks what the mutation
wrote) after a real by-value bug left `git add` without paths.
docs/protocol.md specifies the on-disk format and the write protocol
independently of the Go code.
Tests: service integration against local bare repos (lifecycle with id
potent claim/release, sequential ids, exit codes 11/12/14/10, claim race
with one winner, concurrent comments with distinct files, takeover after
lease expiry with fenced previous owner, filters); CLI end-to-end via
DK_STORE (happy path, JSON envelopes, exit-code table, help).

### R-10 Store (2026-11-02)
Behaviour: `internal/store` owns a private working copy per profile outside
any project directory (user cache dir, named after remote, branch and
owner), runs git as a subprocess with the profile identity in every call
(ADR-0002), validates the branch ref with git check-ref-format, and
implements the write protocol: fetch, preconditions on the fresh head,
mutate, commit, push; on a rejected push exponential backoff with jitter
and a fresh fetch, bounded by the concept's starting values (8 attempts,
200 ms base, 5 s cap, 30 s budget — options, not constants). Push
classification treats non-fast-forward, fetch-first and the server-side
"cannot lock ref" race as retryable and everything else as permanent
(rights, protection, network), so an agent never waits on an error that
never goes away. Reads (Has/ReadFile/List) work against the fetched head;
an empty remote branch is supported and the first push creates it. All
paths through the working copy are normalised and checked (`..`, absolute,
control characters rejected). `internal/profile` carries owner id and git
identity; Default() is user@host in lower case.
Tests: integration against local bare repos — write/read round trip with
author identity, first push on an empty remote, precondition failure
leaves the remote untouched, forced non-fast-forward retry, 8-way
concurrent claim race with exactly one winner (permanent regression test
for the claim invariant), concurrent distinct paths, unreachable remote
fails fast, unsafe paths rejected, private working copy outside cwd;
unit tests for path safety, push classification and profile identity.

### R-2 Foundations (2026-11-02)
Behaviour: Go module `github.com/HenrikPuls/dricket` (go 1.27); `cmd/dricket`
CLI skeleton with global flags (`--json`, `-q`, `--no-color`, `-y`, `-n`,
`--store`, `--profile`), command registry (`version` implemented), the
documented exit codes as a typed error mapping, and the JSON envelope
`{"schema_version":1,"data":...}` / `{"schema_version":1,"error":{kind,
exit_code,message}}` (stdout is data, stderr is messages; `-q` never silences
errors). `internal/frontmatter`: strict flat parser whose errors name line
and expectation; header lines tolerate CRLF, the body stays verbatim, Parse
and Render are exactly inverse. `internal/ticket`: field classes
(identity/state/free), typed model with FromDoc/ToDoc, unknown keys returned
as warnings, lint (required fields, enums, id shape, owner/lease/epoch
consistency) and stage transitions per role (agent up to review, human
everything). Lint config and CI pin moved to golangci-lint v2.15.0 / action
v9 (v1.64 predates go 1.27).
Tests: CLI exit codes and JSON shapes (cmd/dricket); parser table, CRLF,
round trip and FuzzParse (33.6M execs clean; the once-failing lone-\r input
is a committed seed); model round trip, lint table, transitions
(internal/ticket).

### R-1 Concurrency spike (2026-11-02)
Behaviour: throwaway harness under `spike/`; 10 clones against a local bare
repo, 10 minutes per run, scaled leases (≙ 4 h and 2 h); random add, claim,
update, release with the concept's retry rules and dead-runner simulation;
full history replay verifying the four invariants plus protocol rules.
Result: PASS, both runs zero violations; `docs/SPIKE.md` written.
Tests: `go test ./...` in `spike/` (parser round-trip, duplicate keys,
legal and illegal ownership transitions, tag accounting).

### R-0 Design concept and repository skeleton (2026-11-02)
Behaviour: documentation only; no code yet. Tests: `scripts/check_docs.py`.
