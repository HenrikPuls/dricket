# Roadmap

Open work only, in order, worked top to bottom. An item leaves this file
when it enters changelog.md or decisions.md. Ids are `R-<n>`, unique
across roadmap and changelog. The stages v0 to v2 in docs/CONCEPT.md map to
the items below; milestone numbers are not used.

## Now
- R-1 Concurrency spike — throwaway harness, 8 to 10 clones against a local bare repo for 10 minutes; four invariants must be zero; write docs/SPIKE.md. Stop on any violation.

## Next
- R-2 Core CLI (concept stage v0) — add, claim, release, ls, show, comment, set, lint; prefix IDs, stable path, parent/depends, schema_version, epoch/rev, exit codes, JSON.
- R-3 Leases and checks (stage v1) — sizes, extend, leases, validate, next, search, inbox, epoch trailers, `dk doctor`.
- R-4 Protection and roles (stage v2) — declarative schema in dricket.toml, path rules, role transitions, force-release, guide/init/setup-agent, profiles, quotas, secret scan.
- R-5 First release — checksums, build provenance, README with a recorded session, public repository after "publish".

## Later
Each has a trigger; without it nothing is built. See "Deliberately deferred" in docs/CONCEPT.md.
- R-6 Lamport or hybrid clocks — trigger: offline writing or multiple refs.
- R-7 Local cache or daemon — trigger: measured slowness.
- R-8 Messages between agents (`messages/<recipient>/`) — trigger: cases that comments do not cover.
- R-9 Lease service as a hybrid — trigger: Git contention not tolerable in R-1.

## Open risks
- Contention under real load is not measured (R-1, then against the real host).
- Merge gate for epoch trailers may not exist on the chosen host.
- Credentials for non-developer frontend users are undecided.
- Name and trademark checks beyond npm, PyPI and crates.io are open.
- GPL and bundling inside a proprietary frontend needs counsel (ADR-0003).
