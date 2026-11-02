# Changelog

Newest first. Ids are `R-<n>`, unique across roadmap and changelog.
This is the internal work log. User-facing release notes are in the
top-level CHANGELOG.md and are written at release time from this file.

## Awaiting verification

- Skeleton: R-0 — handed over 2026-11-02
- Infrastructure and Phase 0: public repository published (CI green, rulesets active, Pages live), roadmap and architecture refined — handed over 2026-11-02
- Spike: R-1 — handed over 2026-11-02
- Foundations: R-2 — handed over 2026-11-02

## Shipped

### R-2 Foundations (2026-11-02)
Behaviour: Go module `github.com/HenrikPuls/dricket` (go 1.27); `cmd/dricket`
CLI skeleton with global flags (`--json`, `-q`, `--no-color`, `-y`, `-n`,
`--store`, `--profile`), command registry (`version` implemented), the
documented exit codes as a typed error mapping, and the JSON envelope
`{"schema_version":1,"data":...}` / `{"schema_version":1,"error":{kind,
exit_code,message}}` (stdout is data, stderr is messages; `-q` never silences
errors). `internal/frontmatter`: strict flat parser whose errors name line
and expectation; header lines tolerate CRLF, the body stays verbatim, Parse
and Render are exactly inverse. `internal/ticket`: field classes
(identity/state/free), typed model with FromDoc/ToDoc, unknown keys returned
as warnings, lint (required fields, enums, id shape, owner/lease/epoch
consistency) and stage transitions per role (agent up to review, human
everything). Lint config and CI pin moved to golangci-lint v2.15.0 / action
v9 (v1.64 predates go 1.27).
Tests: CLI exit codes and JSON shapes (cmd/dricket); parser table, CRLF,
round trip and FuzzParse (33.6M execs clean; the once-failing lone-\r input
is a committed seed); model round trip, lint table, transitions
(internal/ticket).

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
