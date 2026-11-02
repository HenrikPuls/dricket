# ADR-0004: Stage v2 — dricket.toml, TOML library, and new exit codes

- Status: accepted
- Date: 2026-11-02

## Context

Stage v2 (R-4) introduces the declarative configuration `dricket.toml`
on the protected `main` branch, quotas, a secret scan and admin-only
`force-release`. Three spec-level decisions follow from that work; the
concept v0.1 predates them.

## Decisions

### 1. TOML parsing uses BurntSushi/toml

The repository had zero external dependencies. Hand-rolling a TOML
subset would keep it that way, but the config is a protected file that
admins write in a well-known format; a subset parser silently
misbehaving on valid TOML is worse than one vetted dependency.
`github.com/BurntSushi/toml` (MIT) is GPLv3-compatible and checked by
the licences CI job. The module stays the only dependency.

### 2. dricket.toml carries limits, admins, quota, the secret scan
switch, `min_version` and the agent transition table

Stages themselves remain the fixed set of the concept (open,
in-progress, review, done, blocked, needs-input, cancelled); what the
config governs is which stage moves each role may perform. Human and
admin roles stay fixed (they may move anything), as the concept fixes
them. The per-field schema stays in code (`internal/ticket`); making it
data-driven has no user-visible benefit today. `grace` is deliberately
not configurable — it is part of the fencing model and changes only
with an ADR.

### 3. Three exit codes are added

| Code | Meaning |
| --- | --- |
| 17 | forbidden (permission denied, e.g. force-release by a non-admin) |
| 18 | quota exceeded (new tickets per owner and period) |
| 19 | refused write: the secret scan found likely credentials |

The concept's exit-code table grows by these rows; 0 to 16 and 20 keep
their meaning. Consumers match on codes, not on their count.

## Consequences

- A broken `dricket.toml` blocks writes through the tool (hard error)
  and is reported by `validate` as a finding with the defaults applied,
  so monitoring keeps working.
- The config is read from `main` through a private local ref
  (`refs/dricket/config`) fetched with `--no-write-fetch-head`, so the
  ticket protocol's `FETCH_HEAD` is never disturbed.
- The minimum supported git version stays 2.32.
