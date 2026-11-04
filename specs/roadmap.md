# Roadmap

Open work only, in order, worked top to bottom. An item leaves this file
when it enters changelog.md or decisions.md. Ids are `R-<n>`, unique
across roadmap and changelog. The stages v0 to v2 in docs/CONCEPT.md map to
the items below; milestone numbers are not used.

## Now

Nothing committed. Pick the next item only when its trigger fires (below).

## Later
- R-21 `add` from a complete document (stage, extension fields, body in one commit) — owner request, follow-up of `put`; the name `add -F` is taken by the description file, so it needs another flag.
- R-22 `validate` replays history with the stage list as of each commit — follow-up of configurable stages; replay currently skips stage membership instead.
- R-23 Windows CI job is configured but unproven on a hosted runner; confirm green, then document the supported git range and CRLF/long-path handling (owner request, Drey list).

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
