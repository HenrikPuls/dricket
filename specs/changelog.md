# Changelog

Newest first. Ids are `R-<n>`, unique across roadmap and changelog.
This is the internal work log. User-facing release notes are in the
top-level CHANGELOG.md and are written at release time from this file.

## Awaiting verification

- Skeleton: R-0 — handed over 2026-11-02
- Infrastructure and Phase 0: public repository published (CI green, rulesets active, Pages live), roadmap and architecture refined — handed over 2026-11-02
- Spike: R-1 — handed over 2026-11-02

## Shipped

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
