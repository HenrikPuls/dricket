# Changelog

All notable changes to this project are documented here.
The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project follows [Semantic Versioning](https://semver.org/). Versions
below 1.0.0 may change the on-disk format and the CLI; such changes are always
listed here.

## [Unreleased]

### Added
- Design concept (docs/CONCEPT.md) and repository skeleton.

## [0.1.1] - 2026-11-03

### Fixed
- `version` reported "dev" for binaries installed through
  `go install ...@vX`; the module version from the build info is
  reported now (release builds keep their ldflags values, injected
  as `v{{.Version}}` for a uniform display).
- Install documentation: `go install` installs the binary under its
  package name `dricket`; the `dk` alias ships in the release
  archives or can be copied/symlinked.

## [0.1.0] - 2026-11-02

First release. Stages v0 to v2 of docs/CONCEPT.md are implemented.

### Added
- Ticket store in Git: one Markdown folder per ticket, `PREFIX-N` ids,
  stable paths, linear fast-forward-only history on the `tickets` branch,
  protected `main` for configuration.
- Claims by push with leases by size (S/M/L), `extend --reason`, takeover
  after `lease_until` plus grace, and `epoch` as fencing token (exit 13).
- Commands: `init`, `add`, `claim`, `release`, `force-release`, `update`,
  `note`, `extend`, `comment`, `set`, `ls`, `next`, `search`, `show`,
  `inbox`, `leases`, `lint`, `validate`, `guide`, `setup-agent`, `doctor`,
  `version`.
- Validation levels: `lint` before every write, `validate` as a deep,
  read-only check over the whole store including a replay of the complete
  history, and the epoch-trailer gate `validate --trailers <range>` for
  code repositories.
- `dricket.toml` on `main`: lease limits with clamping, `max_extensions`,
  admins, ticket quota, secret scan, minimum tool version, ticket
  priorities and the stage transitions per role.
- Protection: ticket quota per identity (exit 18), secret scan before
  every write and over the whole store (exit 19), admin-only
  `force-release` (exit 17).
- Profiles with per-actor git identity and optional SSH key, isolated
  from the user's global git configuration.
- `--json` envelope with `schema_version`, documented exit codes 0-2,
  10-20 (docs/CONCEPT.md).
