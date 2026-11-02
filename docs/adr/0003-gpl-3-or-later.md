# ADR-0003: License GPL-3.0-or-later

- Status: accepted
- Date: 2026-11-02

## Context
dricket is published as a public repository. Frontends such as drey call it as
a separate process.

## Options considered
1. GPL-3.0-or-later — strong copyleft for the tool itself.
2. Apache-2.0 or MIT — permissive, easier to bundle anywhere.

## Decision
GPL-3.0-or-later, DCO sign-off instead of a CLA. Every dependency must be
GPLv3-compatible (checked in CI).

## Consequences
Distributing a binary brings the duty to offer its source. Programs that only
call `dk` as a separate process are not part of dricket in general. This is
not legal advice; counsel confirms it before a GPL binary is bundled in a
proprietary application. Revisit if embedding as a library becomes a goal.
