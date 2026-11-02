# Contributing

Thanks for helping. dricket is early; the design is in
[docs/CONCEPT.md](docs/CONCEPT.md) and is the specification. Read it first
along with [CLAUDE.md](CLAUDE.md) (the working rules), especially "Deliberately deferred": every deferred item has a trigger, and
without the trigger it is not built.

## Ground rules

- **License.** The project is GPL-3.0-or-later. By contributing you agree your
  contribution is licensed the same way. There is no CLA; we use the
  [Developer Certificate of Origin](https://developercertificate.org/).
  Sign every commit off: `git commit -s`.
- **SPDX header** in every source file: `// SPDX-License-Identifier: GPL-3.0-or-later`.
- **Dependencies** must be GPLv3-compatible (MIT, BSD, Apache-2.0, MPL-2.0).
  CI checks this. Prefer the standard library.
- **Protocol changes** (on-disk format, write protocol, exit codes) need an
  issue first, and must update `docs/protocol.md` and the changelog.

## Workflow

1. Open an issue for anything larger than a small fix.
2. Fork, branch, commit with sign-off.
3. `go build ./... && go vet ./... && golangci-lint run && go test -race -count=1 ./... && python scripts/check_docs.py`
4. Open a pull request. The `ci` check must pass.

## Tests that matter most

The invariants below must stay at zero in every concurrency test: duplicate
IDs, tickets with two owners, lost updates, identity violations. A change that
makes one of them non-zero is rejected, however good the rest looks.

## Using coding agents

Agents are welcome contributors. They must follow [AGENTS.md](AGENTS.md), and
a human is responsible for every pull request.
