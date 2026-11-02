# ADR-0006: next picks by priority first

- Status: accepted
- Date: 2026-11-02

## Context

ADR-0005 introduced the configurable `priority` field and explicitly
left `next`'s ordering untouched (lowest number first). With priorities
in place, the owner decided that agents should pick urgent work first:
a ticketing tool that tells agents the priority but then hands out
tickets by number ignores its own field.

## Decision

`next` (and `next --claim`) picks the highest-ranked suitable ticket:
the order of `[priorities].values` defines the rank (first value =
highest priority), and within one rank the lowest number in the lowest
prefix wins, as before. Unset or unknown priorities rank last.

## Consequences

- The values order in dricket.toml is meaningful: `["critical", "high",
  "normal", "low"]` ranks critical highest. Reordering the list changes
  what agents pick up next.
- Priority is still informational metadata for everything else; only
  `next` orders by it.
