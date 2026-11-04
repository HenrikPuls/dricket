# Git-like CLI and Structured Help Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the existing dricket CLI internally consistent and Git-like, with no new ticket features, and replace the flat/incomplete help with accurate grouped and per-command help.

**Architecture:** The command registry owns command usage, group and flag-set construction. Parsing accepts registered options on either side of positional arguments, while one shared help renderer consumes the same metadata. Old spellings and dead global options are removed in one documented pre-1.0 migration.

**Tech Stack:** Go standard library `flag`, existing CLI tests, Markdown and static HTML documentation.

**Spec:** `docs/superpowers/specs/2026-11-03-cli-and-agent-skill-design.md`

## Global Constraints

- No new ticket command, service operation, dependency, data format or exit code.
- Keep `add`, `claim`, `release`, `sync`, `restore`, `commit` and other domain names unless this plan explicitly removes `update`.
- Use `-m, --message` for existing single text/reason values; do not add new file-input capabilities.
- Remove the old spellings rather than maintaining aliases.
- Help writes to stdout and exits 0 without store resolution; usage errors write to stderr and exit 2.
- All content is English; commits carry DCO sign-off.

## Review Focus

- A text value beginning with `-` after `--` must remain positional data; Task 1 tests the terminator.
- A Boolean flag must not consume the following ticket ID as its value; Task 1 uses registered flag metadata.
- Per-command help must not execute the command or open a store; Task 3 uses an unset/invalid store environment.
- Every documented example must use the new syntax; Task 4 runs documentation checks and searches for removed spellings.
- The redundant `update` and dead global flags must fail as usage errors rather than remain hidden aliases; Task 2 pins removal.

---

### Task 1: Parse command options consistently around positional arguments

**Files:**
- Modify: `cmd/dk/commands.go`
- Create: `cmd/dk/flags_test.go`

**Interfaces:**
- Consumes: existing `newFlagSet` and command run functions.
- Produces: unchanged `parseFlags(fs *flag.FlagSet, args []string) *cliError` signature with interspersed-option behavior and `--` termination.

- [ ] **Step 1: Write failing parser and CLI tests**

Cover `claim --size M app-1` and `claim app-1 --size M`, Boolean `--unclaimed` followed by another option, `--store`-independent command parsing, missing option values, unknown options and `--` followed by literal dash-prefixed positional data. Assert Boolean flags never consume the next argument.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test -count=1 ./cmd/dk -run 'Flags|Interspersed|Terminator'`
Expected: options after the first positional argument currently remain unparsed.

- [ ] **Step 3: Implement metadata-driven argument ordering**

In `parseFlags`, use `fs.Lookup` and `flag.Value`/`IsBoolFlag` to distinguish value-taking and Boolean options. Support long `--flag=value` forms, preserve option/value pairs, stop option recognition at `--`, and let the existing `FlagSet` report unknown or malformed flags. Do not hard-code a second list of command flags.

- [ ] **Step 4: Run all CLI tests and commit**

Run: `go test -count=1 ./cmd/dk`
Expected: PASS, including all pre-existing old-syntax tests; those are migrated only in Task 2.

```bash
git add cmd/dk/commands.go cmd/dk/flags_test.go
git commit -s -m "feat: accept command options around positional arguments"
```

### Task 2: Migrate text flags and remove redundant/dead CLI surface

**Files:**
- Modify: `cmd/dk/commands.go`, `cmd/dk/monitor.go`, `cmd/dk/protection.go`, `cmd/dk/main.go`
- Modify: affected `cmd/dk/*_test.go`
- Modify: `cmd/dk/protection.go` guide text

**Interfaces:**
- Consumes: Task 1 parser and existing service calls.
- Produces: `-m/--message` on `add`, `note`, `comment`, `extend`, `release`, `force-release`; removal of `update`, `-c`, `--note`, `--result`, `--reason`, positional note/comment text and dead global options.

- [ ] **Step 1: Write failing contract tests**

For each changed command, invoke the new `-m` spelling before and after the ticket ID and assert the existing service result. Assert `add -c`, `update`, `note <id> <text>`, `comment <id> <text>`, `--note`, `--result`, `--reason`, `-q`, `--no-color`, `-y` and `-n` return exit 2. Keep existing `add -d`, `add -F` and `comment -F` coverage.

- [ ] **Step 2: Run focused tests and verify RED**

Run: `go test -count=1 ./cmd/dk -run 'TextFlags|Removed|Global|HappyPath'`
Expected: old spellings still work and/or new `-m` forms are unknown.

- [ ] **Step 3: Implement the CLI migration**

Bind short and long message flags to the same variable with `StringVar`. Change only the specified spellings, remove `update` from the registry and its run function, and remove dead env fields/parser cases/help entries for quiet, color, assume-yes and dry-run. Convert `note` and `comment` to the new message flag; preserve comment's existing `-F`. Do not change service signatures or JSON payloads.

- [ ] **Step 4: Update guide examples and run all affected tests**

Use valid new forms throughout `guideText`, including option placement covered by Task 1. Run: `go test -count=1 ./cmd/dk ./internal/service`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/dk
git commit -s -m "refactor: align the CLI text and global flags"
```

### Task 3: Generate grouped root help and per-command help

**Files:**
- Modify: `cmd/dk/main.go`, `cmd/dk/commands.go`
- Modify: command files only where flag-set construction is still embedded
- Create: `cmd/dk/help_test.go`

**Interfaces:**
- Consumes: existing `command` registry and per-command flag constructors from Task 2.
- Produces: `command` fields `usage string`, `group string`, `flags func() *flag.FlagSet`; internal `commandHelp(c command) string`; unchanged command execution signatures.

- [ ] **Step 1: Write failing help tests**

Assert root help says `Usage: dk`, contains `Setup`, `Work`, `Inspect`, `Check`, places every command in exactly one group and omits the roadmap note. For every registry command, assert both `dk <command> --help` and `dk help <command>` exit 0, print its usage/synopsis and actual flag defaults, and do not resolve a store. Assert unknown help topics exit 2.

- [ ] **Step 2: Run tests and verify RED**

Run: `go test -count=1 ./cmd/dk -run 'Help'`
Expected: grouped and per-command help are absent.

- [ ] **Step 3: Move flag construction behind registry metadata**

Give each command a usage string and a flag-set constructor used both by its run function and help rendering. Read parsed values through typed `flag.Getter` helpers or command-specific flag structs; do not maintain a separate help-only flag list. Commands without flags return an empty configured set.

- [ ] **Step 4: Implement help dispatch and rendering**

Handle `-h/--help` through `flag.ErrHelp` without turning it into exit 2. Add `help <command>` dispatch and grouped root rendering. Per-command help contains only usage, synopsis and actual options; it never calls the command's run function. Remove the obsolete quiet-help test replaced by dead-flag rejection coverage.

- [ ] **Step 5: Run the CLI suite and commit**

Run: `go test -count=1 ./cmd/dk`
Expected: PASS.

```bash
git add cmd/dk
git commit -s -m "feat: add structured command help"
```

### Task 4: Migrate all user-facing examples and verify the CLI contract

**Files:**
- Modify: `README.md`, `site/index.html`, `CHANGELOG.md`
- Modify: `docs/CONCEPT.md`, `docs/protocol.md`, `docs/architecture.md` where CLI syntax/help is described
- Modify: `specs/changelog.md`

**Interfaces:**
- Consumes: Tasks 1-3.
- Produces: documented R-15 migration and verified help/CLI behavior.

- [ ] **Step 1: Update examples and migration notes**

Replace removed flags and `update` with the new syntax. Document grouped/per-command help and removed global options. Add R-15 under Awaiting verification with tests actually run and an Unreleased Changed entry explicitly naming every removed spelling.

- [ ] **Step 2: Search for stale user-facing syntax**

Use content searches for `add -c`, `--note`, `--result`, `--reason`, `dk update`, positional comment/note examples and dead global flags. Historical ADRs/changelogs may remain as historical records; current guides, README, site and concept examples must not present them as current behavior.

- [ ] **Step 3: Run the complete local gate**

Run: `go build ./... && go vet ./... && golangci-lint run && go test -count=1 ./... && python scripts/check_docs.py`
Expected: all commands exit 0.

- [ ] **Step 4: Exercise help and a real workflow**

Build `dk`; run root help, `dk help add`, `dk commit --help` outside a store, then create/claim/comment/note/edit/commit/release a ticket using options both before and after IDs. Expected: accurate help without store errors and a complete successful workflow.

- [ ] **Step 5: Commit**

```bash
git add README.md site/index.html CHANGELOG.md docs specs/changelog.md
git commit -s -m "docs: describe the Git-like CLI"
```
