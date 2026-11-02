# Roadmap

Open work only, in order, worked top to bottom. An item leaves this file
when it enters changelog.md or decisions.md. Ids are `R-<n>`, unique
across roadmap and changelog. The stages v0 to v2 in docs/CONCEPT.md map to
the items below; milestone numbers are not used.

## Now

Nothing. Stage v1 is shipped; next is stage v2 (R-4 below).

## Next
Stage v2 (stage v0 and v1 are shipped, see specs/changelog.md):
- R-4 Protection and roles (stage v2) — declarative schema in dricket.toml, path rules, role transitions, force-release, guide/init/setup-agent, profiles, quotas, secret scan.
- R-5 First release — checksums, build provenance, README with a recorded session.

## Later
Each has a trigger; without it nothing is built. See "Deliberately deferred" in docs/CONCEPT.md.
- R-6 Lamport or hybrid clocks — trigger: offline writing or multiple refs.
- R-7 Local cache or daemon — trigger: measured slowness.
- R-8 Messages between agents (`messages/<recipient>/`) — trigger: cases that comments do not cover.
- R-9 Lease service as a hybrid — trigger: Git contention not tolerable in the spike.

## Open risks
- Contention measured locally at ~100x assumed load (spike passed); repeat a shorter run against the real host.
- Merge gate for epoch trailers may not exist on the chosen host.
- Credentials for non-developer frontend users are undecided.
- Name and trademark checks beyond npm, PyPI and crates.io are open.
- GPL and bundling inside a proprietary frontend needs counsel (ADR-0003).
