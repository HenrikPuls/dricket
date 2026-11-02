# Security policy

## Supported versions

dricket is pre-1.0. Only the latest release receives security fixes.

## Reporting a vulnerability

Please report vulnerabilities **privately** through GitHub's private
vulnerability reporting: open the repository's *Security* tab and choose
*Report a vulnerability*. Do not open a public issue.

Include the version (`dk --version`), your Git version and host, and steps to
reproduce. Remove any secrets from logs.

This is a volunteer-run project. Reports are handled on a best-effort basis;
the maintainer will acknowledge a report as soon as possible and keep you
informed about the fix and a coordinated disclosure date.

## Scope

In scope: the `dricket` binary, its on-disk format and write protocol, and the
release artifacts.

Known and documented limits (see "Access and security" in docs/CONCEPT.md) are
not vulnerabilities by themselves: an agent with shell access in the same
operating-system user context can bypass the tool; dricket detects, but cannot
prevent, edits made around it.

## Supply chain

Release artifacts come with SHA-256 checksums and a build provenance
attestation. Verify them before running a downloaded binary.
