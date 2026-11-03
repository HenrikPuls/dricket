<h1 align="center">dricket</h1>

<p align="center"><strong>Tickets that live in Git. Claimed by fast-forward. Safe for humans and agents.</strong></p>

<p align="center">
  <a href="https://github.com/HenrikPuls/dricket/actions/workflows/ci.yml"><img alt="CI" src="https://github.com/HenrikPuls/dricket/actions/workflows/ci.yml/badge.svg"></a>
  <a href="LICENSE"><img alt="License: GPL v3 or later" src="https://img.shields.io/badge/license-GPL--3.0--or--later-blue"></a>
  <img alt="Release: v0.4.0" src="https://img.shields.io/badge/release-v0.4.0-blue">
</p>

dricket is a ticketing system with no server. Every ticket is a Markdown file in
a Git repository, and the one thing that matters when many people and AI agents
work at once, *who is working on this right now*, is decided by Git itself: only
one fast-forward publication lands.

A person can read and edit a ticket in any editor. An agent gets a small,
strict command line that keeps it from stepping on anyone else's work. Both
use the same files.

> **Status: early release (v0.4.0).** The design is specified in
> [docs/CONCEPT.md](docs/CONCEPT.md); stages v0 to v2 are implemented and
> released. Later stages (messages, skills, lease services) are deliberately
> deferred, each with the trigger that would build them. See [Status](#status).

## Why dricket

- **No server to run.** Your Git host is the database and the lock manager.
- **One atomic claim.** Claiming a ticket is a fast-forward publication. Exactly
  one claimant wins; the others are told so, immediately.
- **Plain Markdown you can edit as a whole file.** Claim, sync, use any editor,
  and publish with `dk commit`.
- **Collaboration without ticket conflicts.** Anyone can add a separate comment
  during a claim; `dk inbox` shows it between work steps or afterwards.
- **Structural guardrails.** Leases, cooperative fencing, immutable identity,
  and revision/epoch transition checks keep the shared state coherent.

## See it work

This is the verified whole-ticket workflow. Timestamps and commit IDs are
shortened. Inside the ticket store, discovery needs no flags or environment.

```console
$ dk add --prefix app -m "Login error on empty password" -l bug --unclaimed
app-1
$ dk next --claim --size M
claimed app-1 (epoch 1, lease until …)
$ dk sync
synced tickets
$ $EDITOR tickets/app/0000/app-1/ticket.md
$ dk comment app-1 -m "please check the empty-password case"
tickets/app/0000/app-1/comments/…-henrik.md
$ dk inbox app-1
comment … henrik: please check the empty-password case
$ dk commit app-1
committed app-1 (rev 2, commit …)
$ dk release --stage review -m "Fix on dk/app-1" app-1
released app-1 -> review
$ dk validate
validate ok
```

`dk restore <path>` discards a local edit without fetching. `dk sync` refuses
a dirty checkout, and `dk commit` preserves local work when the same ticket
changed remotely. Everything remains ordinary Git history and Markdown.

## Status

| Area | State |
|---|---|
| Design and protocol | specified, see [docs/CONCEPT.md](docs/CONCEPT.md) and [docs/protocol.md](docs/protocol.md) |
| Concurrency spike (many clones, invariants) | passed, see [docs/SPIKE.md](docs/SPIKE.md) |
| Stage v0 (add, claim, release, ls, show, comment, set, lint) | released |
| Stage v1 (extend, update/note, leases, validate, next, search, inbox, doctor) | released |
| Stage v2 (dricket.toml, quotas, secret scan, force-release, init, setup-agent, profiles, priorities) | released |
| Releases | [v0.3.0](https://github.com/HenrikPuls/dricket/releases/tag/v0.3.0) |
| Discovery: commands inside the ticket store need no flags; `dk use` binds code repositories | released |
| Collaborative editing: sync, restore, whole-ticket commit and exact-base CAS | implemented in the development branch; see Unreleased changelog |

Deliberately **not** built, each with the trigger that would change that, is
listed under "Deliberately deferred" in the concept.

## Install

One static binary, shipped under one name: **`dk`**.

**Release archive** (recommended): download from the
[releases page](https://github.com/HenrikPuls/dricket/releases) — archives
for linux, macOS and Windows on amd64 and arm64, plus `checksums.txt`
(SHA-256) and a build provenance attestation.

**`go install`** builds from the module and installs `dk` directly:

```console
go install github.com/HenrikPuls/dricket/cmd/dk@v0.4.0
```

Make sure the Go bin directory (`go env GOPATH` + `/bin`, on Windows
`%USERPROFILE%\go\bin`) is on your `PATH`. `git` (2.32 or newer) must be
installed.

Set up a local store with one project prefix and work directly in its ticket
checkout:

```console
dk init tickets-store app
cd tickets-store
dk add -m "First ticket"
```

No store flag or binding is needed inside that repository. Configuration stays
on `main`; discovery reads it from Git while `tickets` is checked out.

Use `dk --help` for the grouped command overview and `dk help <command>` or
`dk <command> --help` for its exact options.

## How it works

```
          runner A                    Git remote                   runner B
             |  fetch                     |                            |
             |<---------------------------|                            |
             |  claim app-6842 (commit)   |                            |
             |--------------------------->|  accepted: fast-forward    |
             |                            |  claim app-6842 (commit)   |
             |                            |<---------------------------|
             |                            |  rejected: not fast-forward|
             |                            |--------------------------->|
             |                            |  B fetches, sees A owns it |
```

- **IDs** look like `app-6842`: a prefix per project and a growing number.
- **Leases** have a size (S, M, L) instead of a guessed duration; an owner can
  extend with a reason.
- **Cooperative fencing** uses an `epoch` that rises on every claim or takeover.
  A compliant writer with a stale epoch is refused and keeps local work.
- **Whole-ticket integrity** keeps identity immutable and validates every
  revision, epoch and ownership transition. Content and workflow fields are
  editable by the claim holder.

Details: [docs/CONCEPT.md](docs/CONCEPT.md); the exact format and write
protocol are in [docs/protocol.md](docs/protocol.md).

## For people building agents

- Edit only your own claimed ticket: `claim` → `sync` → editor → `commit` →
  `release`. Comment on another claimed ticket instead of editing its file.
- Inside the ticket repository (and its subdirectories) `dk` resolves
  store, branch and project by itself — no flags, no environment. In a
  code repository, `dk use --store <path>#tickets` and `dk use <prefix>`
  write the binding once.
- Exit codes are part of the contract: `11` already claimed, `12` same-ticket
  or local checkout conflict with local work preserved, `13` claim lost (**stop immediately**),
  `14` invalid document or transition, `15` definite network/push failure or
  `outcome-unknown`, `16` tool too old.
- `dk guide` prints version-matched instructions; `dk setup-agent` adds a
  pointer to a repository's `AGENTS.md`.
- Ticket text is untrusted input. Comments are asynchronous review input, not
  an authorization or interruption channel.
- Several agents on one machine can use separate Git accounts through profiles.

## Security and trust model

- **Trusted collaborators:** everyone allowed to push the ticket repository can
  edit tickets. The Git host owns authentication and branch authorization.
- **Integrity:** dricket rejects malformed documents and illegal identity,
  revision, epoch and claim transitions; `dk validate` also checks direct
  writes afterwards or in a host-side gate.
- **Cooperative fencing:** stale compliant writers stop at the next write.
  External side effects and arbitrary shell access are outside this guarantee.
- Report vulnerabilities privately, see [SECURITY.md](SECURITY.md).

## Related projects

dricket was designed after reading these. Descriptions are our reading of their
documentation, not a benchmark; see "Prior art" in the concept for sources.

| Project | Approach | Where dricket differs |
|---|---|---|
| [tkt](https://docs.rs/crate/tkt/latest/source/README.md) | git push as claim protocol | leases by size, fencing, roles, validation (design) |
| [Beads](https://pkg.go.dev/github.com/steveyegge/beads) | hash IDs, SQLite and a daemon | numeric IDs, no daemon, no database |
| [Backlog.md](https://github.com/MrLesk/Backlog.md) | title in the file name | stable path per ticket |
| [git-bug](https://www.github.com/MichaelMure/git-bug/blob/master/doc/model.md) | data in Git refs | plain files readable on any Git host |

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md). Contributions need a DCO sign-off
(`git commit -s`) and, for coding agents, [AGENTS.md](AGENTS.md).

## License

dricket is free software: GNU General Public License v3.0 or later
(`GPL-3.0-or-later`). See [LICENSE](LICENSE). Other programs that call `dk` as a
separate process and read its output do not become part of dricket.
*(This sentence describes the intent, it is not legal advice.)*

dricket was designed for the ticket needs of [drey](docs/integrations/drey.md)
and works with any frontend or none.
