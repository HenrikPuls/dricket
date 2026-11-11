# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project follows [Semantic Versioning](https://semver.org/). Versions
below 1.0.0 may change the on-disk format and the CLI; such changes are always
listed here.

## [0.6.3] - 2026-11-11

### Changed
- The repository moved to the lariscus organization: the Go module path
  and all links changed from `github.com/HenrikPuls/dricket` to
  `github.com/lariscus/dricket`. No behaviour, protocol, exit-code or
  JSON change. `go install` requires the new path from this release on
  (the old path fails with a module-path mismatch); existing binaries
  keep working, and the GitHub redirect keeps old links and clones
  working.

## [0.6.2] - 2026-11-05

### Fixed
- Windows long paths: dk's git calls run with `core.longpaths=true`, so a data
  or store directory of up to about 190 characters works (before, `fetch` and
  `worktree add` failed with "Filename too long" from about 156). Beyond that
  git itself cannot enter the directory; the README now says to keep
  `DK_DATA_DIR` and local store paths short.
- `dk init` writes a `dricket.toml` that stays valid with a custom `[stages]`
  table: the redundant `[transitions.agent]` table (the built-in defaults) is
  now only a comment. Existing stores are unchanged.

## [0.6.1] - 2026-11-05

### Added
- `dk --trace` / `DK_TRACE=1`: prints the steps of a call and every git
  process it starts (arguments, run time) on stderr, ending in a summary such
  as `git: 4 processes, 82 ms of 83 ms`; with `--json` one JSON object on
  stderr. Stdout and exit codes do not change. Commit messages, search
  patterns and URL credentials are redacted (ADR-0014).

### Changed
- Read commands no longer touch the private working copy: on a local store
  `dk show` starts 4 git processes instead of 16 and `dk ls --json` 5 instead
  of 107; a remote store keeps its fetch and loses only `reset` and `clean`.
  Parallel reads no longer fail with `reset to local head: exit status 128`.
  The private working copy of a local store is created by the first write.
  Writes, claims, epoch fencing and the compare-and-set on the ticket ref are
  unchanged; stdout, JSON and exit codes are byte-identical (ADR-0014).
- A command that reads in several steps can see two successive heads when
  another process publishes in between; `validate` and `inbox` pin one commit.

## [0.6.0] - 2026-11-04

### Added
- `x-<name>` extension header fields carry a frontend's own metadata:
  preserved in order on every write, validated for shape and size,
  secret-scanned; `add --set x-key=value` creates tickets with them
  (ADR-0011).
- Configurable stage vocabulary: a `[stages]` table in `dricket.toml`
  replaces the built-in stages (`values`, `initial`, `terminal`, `claim`,
  optional `cancelled`); claims, releases, `next`, lint and the transition
  table follow it (ADR-0011).
- `dk put <id> (-F <file> | -)`: whole-ticket write without a checkout,
  guarded by `--if-version`, with the checks of `commit` (ADR-0011).
- `dk put --claim`: takes and releases the claim inside the one commit (a
  "short claim") — epoch unchanged, no lease times written, every kind may
  write, a foreign claim stays exit 11 (ADR-0012).
- `dk commit --release [--stage S] [-m note]`: writes the edited ticket
  and frees the claim in one commit, with the checks of `commit` and the
  release transition policy; the JSON result gains `stage` (ADR-0013).

### Changed
- The promise "no write without a claim" now holds without exception:
  `dk put` on an unclaimed ticket requires `--claim` (exit 17 otherwise);
  the human/admin permission exception and `put --kind` are gone. An
  admin's `--force` remains the only emergency path (ADR-0013).
- `dk put` validates the priority value like every other write.

### Removed
- `dk set` and `dk move`: field and stage changes are small whole-ticket
  `dk put --claim` documents (ADR-0013).

## [0.5.0] - 2026-11-04

### Added
- `dk setup-agent` installs the embedded dricket Agent Skill at
  `.agents/skills/dricket/SKILL.md`, offline and without overwriting a modified
  skill. `dk guide` remains the version-matched runtime reference.
- Single-repository local operation: non-bare filesystem stores publish through
  atomic fast-forward ref updates, while remote and bare stores keep push CAS
  (ADR-0010).

### Changed
- `dk init` leaves the ticket branch checked out, and discovery reads committed
  configuration from `main` even from nested ticket directories.
- Local writes preserve unrelated and staged edits; a pre-existing same-file
  checkout conflict reports `conflict` with exit 12.
- Existing text/reason inputs use `-m/--message`; `add -c`, positional text on
  `note`/`comment`, `--note`, `--result` and `--reason` are removed. The
  redundant `update` command is removed in favor of `note`.
- Root help is grouped by task and every command supports `--help` and
  `dk help <command>`. The previously ineffective global flags `-q/--quiet`,
  `--no-color`, `-y/--assume-yes` and `-n/--dry-run` are removed.

## [0.4.0] - 2026-11-03

### Added
- Collaborative whole-ticket editing: `claim` → `sync` → editor → `commit`;
  `restore` discards selected local edits (ADR-0009).
- Exact-base CAS for edited tickets, preserving local work on conflicts and
  allowing unrelated commits and separate comments during a claim.
- Unknown-push reconciliation using the recorded commit; inconclusive results
  report `outcome-unknown` with exit 15.

### Changed
- Validation enforces document, identity, revision, epoch and ownership
  integrity rather than per-field authorization or role-based history policy.
- Unknown front-matter fields survive tool writes; `parent` is hierarchy only,
  while `depends` controls scheduling.

### Fixed
- Checkout adoption refuses unrelated dirty files and unpushed or divergent
  local commits instead of discarding them.

## [0.3.0] - 2026-11-03

### Added
- Git-like discovery: commands inside the ticket repository work
  without `--store` or `--prefix` — the tool walks up to the store
  root (git working tree with `dricket.toml`) and takes remote,
  branch (`branch` key, new and additive) and a single registered
  prefix from there (ADR-0008).
- `dk use`: binds store (`--store <remote>#<branch>`) and project
  prefix in a `.dricket` file of the current directory, so code
  repositories resolve their ticket store like the store itself does;
  plain `dk use` prints the resolution with its sources.
- `dk init <dir> [prefix]` registers an initial prefix, making `add`
  work without `--prefix` in a single-project store.

### Changed
- `add` resolves the project prefix: `--prefix`/`-p`, `DK_PROJECT`,
  `.dricket`, the single registered prefix, else exit 2 with a hint
  (the outdated "stage v1" message is gone). `add`'s parent flag is
  `--parent` now; `-p` is the prefix, matching the concept's CLI
  table.
- `setup-agent` and `doctor` resolve the store through the same chain.

## [0.2.0] - 2026-11-03

### Changed
- Comment files are named `<UTC-timestamp>-<author>.md` (for example
  `2026-11-03T10-04-24Z-henrik.md`) and carry no `number` field
  anymore; the front matter is `ticket`, `author`, `created_at`
  (ADR-0007). Same-author-same-second collisions resolve inside the
  write precondition. v0.1 numbered names stay valid in older stores
  and `validate` accepts them when the number matches.
- The inbox marker stores the comment file names already seen instead
  of a count (local, disposable state; a stale numeric marker resets
  once).

### Changed (packaging)
- The program ships under the single name `dk`: GoReleaser builds one
  binary, and the entry point is now `cmd/dk`, so
  `go install github.com/HenrikPuls/dricket/cmd/dk@vX` installs `dk`
  directly (the former `cmd/dricket` path is gone).

## [0.1.1] - 2026-11-03

### Fixed
- `version` reported "dev" for binaries installed through
  `go install ...@vX`; the module version from the build info is
  reported now (release builds keep their ldflags values, injected
  as `v{{.Version}}` for a uniform display).
- Install documentation: `go install` installs the binary under its
  package name `dricket`; the `dk` alias ships in the release
  archives or can be copied/symlinked.

## [0.1.0] - 2026-11-02

First release. Stages v0 to v2 of docs/CONCEPT.md are implemented.

### Added
- Ticket store in Git: one Markdown folder per ticket, `PREFIX-N` ids,
  stable paths, linear fast-forward-only history on the `tickets` branch,
  protected `main` for configuration.
- Claims by push with leases by size (S/M/L), `extend --reason`, takeover
  after `lease_until` plus grace, and `epoch` as fencing token (exit 13).
- Commands: `init`, `add`, `claim`, `release`, `force-release`, `update`,
  `note`, `extend`, `comment`, `set`, `ls`, `next`, `search`, `show`,
  `inbox`, `leases`, `lint`, `validate`, `guide`, `setup-agent`, `doctor`,
  `version`.
- Validation levels: `lint` before every write, `validate` as a deep,
  read-only check over the whole store including a replay of the complete
  history, and the epoch-trailer gate `validate --trailers <range>` for
  code repositories.
- `dricket.toml` on `main`: lease limits with clamping, `max_extensions`,
  admins, ticket quota, secret scan, minimum tool version, ticket
  priorities and the stage transitions per role.
- Protection: ticket quota per identity (exit 18), secret scan before
  every write and over the whole store (exit 19), admin-only
  `force-release` (exit 17).
- Profiles with per-actor git identity and optional SSH key, isolated
  from the user's global git configuration.
- `--json` envelope with `schema_version`, documented exit codes 0-2,
  10-20 (docs/CONCEPT.md).
