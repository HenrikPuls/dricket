# Concurrency spike (R-1)

Run on 2 November 2026 by the throwaway harness under [`spike/`](../spike/).
Verdict: **PASS**. All four invariants stayed at zero in both runs; Git held
up as the lock manager at roughly a hundred times the write load the concept
assumes. No decision of the concept changed, so no new ADR was needed;
[ADR-0001](adr/0001-claim-by-push.md) is confirmed by measurement.

## Question

Does the push-as-compare-and-swap protocol of [CONCEPT.md](CONCEPT.md) keep
its four invariants under concurrent load, and what do rejects and latency
look like?

1. duplicate IDs (no ticket path ever created twice)
2. tickets with two owners (every ownership transition legal)
3. lost updates (every accepted push still present in the final state)
4. identity violations (id, created_at, created_by, schema_version immutable)

## Setup

- Local bare repository, **10 concurrent clones**, **10 minutes** per run,
  Windows 11, git 2.55.0, one machine.
- Random operations per actor, weighted: update 45 %, release 20 %, claim
  20 %, add 10 %, plus a 5 % dead-runner simulation (a fresh claim is
  abandoned without release; also 5 % of owned tickets are abandoned per
  step). Actors pause 100–400 ms between operations.
- Write protocol exactly as in the concept: fetch, `reset --hard FETCH_HEAD`
  (never rebase), preconditions on the fresh head (free ticket, owner + epoch
  for updates, lease + grace expired for takeover), commit, push. On a
  rejected push: exponential backoff, 200 ms base, ×2 per attempt, 5 s cap,
  jitter ×0.5–1.5, at most 8 attempts within 30 s.
- Time is scaled so lease expiry fits into minutes: **run A** lease 4 min ≙
  4 h, grace 20 s (factor 1:60); **run B** lease 2 min ≙ 2 h, grace 10 s
  (factor 1:120). Grace is lease ÷ 12 in both runs, mirroring the concept's
  "15 to 30 minutes" against 4 h.
- ID allocation deliberately races: actors pick max + 1 plus a random spread
  of 0–2, so collided adds are part of the mix.
- The verifier replays the complete history afterwards (one snapshot per
  commit) and checks the invariants plus protocol rules (one ticket per
  commit, rev +1 per write, id = path).

## Results

| | Run A (lease ≙ 4 h) | Run B (lease ≙ 2 h) |
|---|---|---|
| Commits | 798 | 800 |
| Commits per hour | 4721 | 4728 |
| add: attempts / ok / push-rejects / final-fail | 373 / 172 / 809 / 201 | 345 / 145 / 787 / 200 |
| add p50 / p95 | 1558 ms / 6090 ms | 1273 ms / 5928 ms |
| claim: attempts / ok / push-rejects / final-fail | 589 / 274 / 629 / 250 | 598 / 291 / 554 / 209 |
| claim p50 / p95 | 654 ms / 3928 ms | 639 ms / 3951 ms |
| claimed-by-other (≙ exit 11) | 55 | 79 |
| idempotent claims (≙ exit 0, no commit) | 10 | 19 |
| update: attempts / ok / push-rejects / final-fail | 483 / 241 / 584 / 241 | 478 / 253 / 607 / 223 |
| update p50 / p95 | 620 ms / 5400 ms | 593 ms / 4318 ms |
| claim lost to fencing (≙ exit 13) | 1 | 2 |
| release: attempts / ok / push-rejects / final-fail | 213 / 111 / 194 / 102 | 227 / 111 / 275 / 116 |
| release p50 / p95 | 704 ms / 4190 ms | 655 ms / 4704 ms |
| abandoned claims (dead runners) | 39 | 53 |
| **Invariants** | **0 / 0 / 0 / 0** | **0 / 0 / 0 / 0** |

Raw data: [`spike/results/`](../spike/results/) (JSON reports and run logs).

## Reading

- **Throughput is identical in both runs.** The lease length does not drive
  commit volume; it sets how long a dead claim blocks a ticket (lease +
  grace ≙ 4 m 20 s versus 2 m 10 s here). The concept's "commits per hour
  with lease 4 h versus 2 h" question therefore has a clear answer: no
  measurable difference at this load.
- **Fencing works under races.** Actors whose claim was taken over after
  lease expiry were caught by the epoch check on their next write
  (claim-lost, the exit-13 analogue) and stopped touching the ticket.
- **Idempotency works.** Claims on already-owned tickets succeeded without a
  commit.
- **Rejects are frequent but bounded.** Most rejects come from the
  deliberately amplified ID-allocation races (add: 809/787 rejects) and from
  simultaneous claims. About 200 adds per run exhausted the retry budget
  (the exit-15 analogue) — at ~4700 writes per hour across 10 runners,
  roughly a hundred times the concept's assumed load of "a few dozen writes
  per hour". Operations stayed bounded throughout: p95 at or below 6.1 s,
  no livelock, adds kept succeeding for the whole run.
- **The real product will see fewer add races** than this harness: `dk add`
  takes max + 1 and only collides when two adds start at the same moment,
  while the spike adds a random spread on purpose.

## Limitations, stated plainly

- **A local repository underestimates the latency of a real host.** There is
  no network round trip, no authentication, no rate limiting and no
  server-side hooks here. The fast-forward check itself is equally atomic on
  a real host, but every retry costs a full network cycle there, so p95 will
  be higher. The concept's plan to repeat a shorter version of this test
  against the real host stands.
- One machine, one clock: clock drift between runners is not tested (the
  concept's grace period exists for it). Ten minutes is no long-tail
  observation. Verification runs after the fact and proves the recorded
  history, not the absence of future bugs.

## Consequences

- [ADR-0001](adr/0001-claim-by-push.md) stands, now with measured evidence.
- The retry starting values (8 attempts, 200 ms base, ×2, 5 s cap, 30 s
  budget) kept everything bounded; they stay configuration values as the
  concept prescribes.
- Dead claims stop blocking a ticket after lease + grace; 4 h versus 2 h is
  a recovery-time choice, not a throughput choice.
- Green light for stage v0 (R-2 foundations, R-10 store, R-11 core
  commands).
