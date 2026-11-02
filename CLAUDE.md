# dricket — Rules for agents

Keep this file under ~150 lines. It holds rules and toolchain only.
Architecture lives in docs/architecture.md, status in specs/changelog.md.

## Project

dricket (CLI `dricket`, alias `dk`) is a Git-native ticketing system for humans
and AI agents: one Markdown folder per ticket in a Git repository, claims
decided by a push, no server. Users are runners (agents), people and
frontends that call `dk --json`. Stack: Go, static binary, `git` invoked
through the git CLI. License GPL-3.0-or-later. The specification is
docs/CONCEPT.md.

## Toolchain

- Run tests: `go test -race -count=1 ./...`   Typecheck: `go vet ./...`   Lint: `golangci-lint run`
- Full gate (before a batch is handed over):
  `go build ./... && go vet ./... && golangci-lint run && go test -race -count=1 ./... && python scripts/check_docs.py`
- `git` must be on the PATH. The concurrency invariant tests spawn real clones
  against a local bare repo and must stay green. `-race` covers in-process
  races only; the protocol races are between processes.

## Rules by priority

### MUST (breaking one is a defect)

1. Never lose or corrupt user data. Writes are atomic; destructive actions
   (delete, overwrite, migration) are asked first and have a way back.
2. No secrets in code, logs, commits or tests. No user content in logs.
3. Never force-push, never rewrite shared history, never commit to the
   default branch directly. Work on a branch, merge only after sign-off.
4. A bug fix starts with a failing regression test. New logic ships with
   a unit test. "Tests green" means nothing without this.
5. Language.
   - Chat with the owner: German.
   - Everything in the repository is English: code, comments,
     identifiers, file names, commit messages, docs, ADRs, specs, tests.
   - Everything the UI displays is English (labels, errors, dialogs,
     tooltips). A German request describes behaviour, never labels:
     translate them.
   - Content users write at runtime keeps its own language. Fixtures and
     screenshots use English sample data.
   - Exception for this project: none.
6. Do not silently deviate from specs/ or docs/. If you deviate, say so
   and update the document in the same change.

### SHOULD (default; deviate only with a stated reason)

7. Explain meaningful decisions briefly in the conversation (why this
   option, what the trade-off is). Not in code comments.
8. For real alternatives with different trade-offs: give the options and
   a recommendation, then wait. For choices the plan or repo already
   settles, decide and log it.
9. If a tool restriction makes the work contorted, stop and say what is
   blocked and which access fixes it. Do not work around it silently.
10. Strict typing, no untyped boundaries. Types for every data shape
    crossing a process or network boundary, defined once.
11. Business logic in services, not in the CLI layer.
12. Keep context lean: delegate broad searches to a subagent, read line
    ranges instead of whole large files, treat file names in a plan as
    hints to verify.
13. New dependency, new data format or new public interface = ask first.
    Exit codes, flags and the on-disk format are public interfaces.

### NOTE (context, not enforcement)

14. A rule that is regularly broken is a bad rule. At each batch review,
    name rules that were ignored; the owner drops or fixes them.

## Project rules (MUST, specific to dricket)

- Every write is: fetch, check preconditions on the fresh head, commit, push,
  bounded retry with jitter. Never rebase.
- stdout is data, stderr is messages. `--json` carries `schema_version`. No
  prompts without a TTY. Honour `NO_COLOR`.
- Exit codes exactly as in docs/CONCEPT.md.
- Identity fields are immutable, state changes only through commands, and
  lint runs before every tool commit.
- Ticket content is untrusted input: no shell strings, validate ref names
  with `git check-ref-format`, normalise paths, enforce size limits.
- No telemetry, no network calls except to the configured Git remote.
- SPDX header `GPL-3.0-or-later` in every source file; dependencies must be
  GPLv3-compatible (checked in CI); commits carry a DCO sign-off (`git commit -s`).
- These invariants must stay at zero in every concurrency test: duplicate
  IDs, tickets with two owners, lost updates, identity violations.
- drey is one consumer. It appears only in docs/integrations/drey.md and one
  README sentence. Use neutral example prefixes such as `app`.

## Workflow

- Work outside the roadmap: add it to specs/roadmap.md first (next free
  id, note "owner request"), then build it. Nothing is built that was
  never listed.
- Work in **batches** of related items (a theme, not a fixed count).
  Independent items go in separate batches; dependent ones in the same.
  Risky changes (data format, migration, deletion) are their own batch
  and stop for sign-off before the next one starts.
- Within a batch, do not pause per item. Prove each item yourself with
  tests, typecheck and lint. Run the full gate once at the end.
- At the end of a batch hand over an **acceptance checklist**: 5-15
  concrete click/CLI paths, grouped by feature, each with the expected
  result. The owner walks it in one go.

## Definition of done (same change as the code)

- [ ] Tests for the change pass; full gate passed at batch end.
- [ ] specs/changelog.md: entry at the top (id, behaviour, tests added).
      While the owner has not signed off the batch, list it under
      "Awaiting verification" at the top of the file. Sign-off removes it
      from there; entries themselves carry no marker.
- [ ] specs/roadmap.md: item removed (follow-ups added).
- [ ] specs/decisions.md: updated if something was rejected or dropped.
- [ ] docs/architecture.md (or an ADR) updated if a building block, an
      interface, or a cross-component flow changed. Internal changes need
      no doc update.
- [ ] `python scripts/check_docs.py` passes.

## Documentation upkeep

- Static view and dynamic view in docs/architecture.md, Mermaid in the
  source (no exported images).
- ADR only for structural, lasting decisions with real trade-offs; not
  for package choices or naming. Unsure: ask.
- Code that embodies an ADR decision references it in a short comment
  (`// ADR-0007`).
- docs/CONCEPT.md is the specification of v0.1. Changes after it are
  recorded as ADRs and reflected in docs/architecture.md.
- The top-level CHANGELOG.md holds user-facing release notes. It is written
  at release time from specs/changelog.md, not edited per change.
