# ADR-0002: Go, with git invoked through the git CLI

- Status: accepted
- Date: 2026-11-02

## Context
The tool runs on many runner machines and in containers and holds credentials
to the ticket repository. It must be easy to distribute and to verify.

## Options considered
1. Go, static binary, git via the `git` CLI — single file per platform, no
   runtime, fast start; needs `git` on the PATH.
2. Python — quick iteration and a rich standard library; needs an interpreter
   and environment on every runner, heavier supply chain.
3. Shell script — not portable (quoting, Windows), fragile parsing.

## Decision
Go with a static binary. Git is called as a subprocess so the user's
authentication, hooks and configuration apply unchanged; no Git library.

## Consequences
Easier: distribution, checksums, provenance, cold start.
Harder: `git` must be installed (checked at startup, `dk doctor`); the minimum
git version is determined by testing. A TOML dependency is needed.
