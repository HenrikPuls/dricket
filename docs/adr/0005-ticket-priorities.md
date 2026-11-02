# ADR-0005: Ticket priorities (stage v2)

- Status: accepted
- Date: 2026-11-02

## Context

Tickets need a priority so humans and agents can tell urgent work from
routine work. The set of priorities differs per store, so it belongs in
the protected configuration, not in the tool.

## Decision

The free field `priority` joins the field classes of docs/CONCEPT.md
(stored lower case, like every enum in the ticket format). Its allowed
values come from dricket.toml:

```toml
[priorities]
values = ["critical", "high", "normal", "low"]
default = "normal"
```

The starting set is critical, high, normal, low with default normal.
Without a config file the same starting set applies. The configured
default fills tickets created without `--priority`. `set priority=...`
and `add --priority` validate against the configured set (exit 14);
`validate` reports unknown values as errors and unset priorities as
warnings, because hand-written tickets may omit the field.

## Consequences

- `next` keeps its lowest-number-first ordering; making it
  priority-aware is a separate decision, because it changes which
  ticket agents pick up.
- The priority is informational metadata: nothing enforces that
  critical tickets are worked first. Ordering by priority is the
  consumer's (frontend's) job today.
