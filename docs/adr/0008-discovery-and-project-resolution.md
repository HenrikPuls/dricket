# ADR-0008: Git-like store discovery and project resolution

- Status: accepted
- Date: 2026-11-03

## Context

`dk add` demanded `--prefix` for every call and every command needed
`--store` or `DK_STORE`, even inside the ticket repository that
`dk init` created. The concept promised project resolution (full id,
`-p`, `DK_PROJECT`, remote mapping, `dk use`) but stage v0 to v2 shipped
none of it; the error message still read "project resolution comes with
stage v1".

## Decision

**Store discovery walks up like git.** From the working directory, the
tool climbs to the actual Git working-tree root. ADR-0010 amends the config
lookup: committed `main:dricket.toml` is authoritative, with the current
checkout file only as fallback when main has no config. Discovery therefore
works from the ticket branch too. Remote is the origin URL when the repository
has one and the directory itself when it does not (the local stores `dk init`
creates are used in place and survive being moved); the branch comes from the
new `branch` key in `dricket.toml` (default `tickets`, written by `dk init`).

**Bindings for code repositories.** A `.dricket` file written by
`dk use` carries `store = "<remote>#<branch>"` and/or
`prefix = "<prefix>"`. `dk use --store <remote>#<branch>` binds the
store (the global `--store` flag feeds the binding),
`dk use <prefix>` binds the project, plain `dk use` prints the current
resolution with its sources. `.dricket` is local state: commit it for a
team convention or git-ignore it when it carries machine-specific paths.

**Precedence.** Store: `--store` > `DK_STORE` > nearest `.dricket`
store key > nearest store root. Prefix (`add` only): `--prefix`/`-p` >
`DK_PROJECT` > nearest `.dricket` prefix key > the single registered
prefix of `prefixes.toml` > error with a hint. A store with exactly one
registered prefix needs no configuration at all.

Inside the ticket repository every command works without any argument —
including from subdirectories — exactly like git.

## Consequences

- The concept's "mapping from the code repo's Git remote to a prefix"
  step is replaced by discovery plus bindings; the heuristic is not
  built.
- Walking up trusts the found `dricket.toml` (protected config) and the
  local `.dricket` (user-owned). Ticket content stays untrusted input;
  nothing here weakens that.
- `add`'s parent flag is `--parent` now; `-p` is the project prefix,
  matching the concept's CLI table.
- The `branch` key is additive: configs without it keep the default.
