// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// actor is one runner with its own private clone of the ticket repository.
// Every write follows the protocol of docs/CONCEPT.md: fetch, check
// preconditions on the fresh head, commit on top, push, and on a rejected
// push retry with exponential backoff and jitter. Never rebase; after every
// rejected push the preconditions are re-checked on the fresh head.
type actor struct {
	id   string
	dir  string
	cfg  *config
	m    *metrics
	acc  *accepted
	rng  *rand.Rand
	env  []string
	mu   sync.Mutex
	owns map[string]int // ticket path -> epoch of my claim
}

func newActor(i int, dir string, cfg *config, m *metrics, acc *accepted, seed uint64) *actor {
	id := strconv.Itoa(i + 1)
	return &actor{
		id:   id,
		dir:  dir,
		cfg:  cfg,
		m:    m,
		acc:  acc,
		rng:  rand.New(rand.NewPCG(seed, uint64(i)+1)),
		env:  gitEnv(id),
		owns: map[string]int{},
	}
}

func (a *actor) run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		a.step()
		pause := 100*time.Millisecond + time.Duration(a.rng.Int64N(int64(300*time.Millisecond)))
		select {
		case <-ctx.Done():
			return
		case <-time.After(pause):
		}
	}
}

// step picks the next operation by weighted random choice.
func (a *actor) step() {
	owned := a.ownedList()
	switch {
	case len(owned) == 0:
		if a.rng.Float64() < 0.6 {
			a.opClaim()
		} else {
			a.opAdd()
		}
	default:
		r := a.rng.Float64()
		switch {
		case r < a.cfg.CrashP:
			a.opAbandon(owned)
		case r < 0.50:
			a.opUpdate(owned)
		case r < 0.70:
			a.opRelease(owned)
		case r < 0.90:
			a.opClaim()
		default:
			a.opAdd()
		}
	}
}

func (a *actor) ownedList() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]string, 0, len(a.owns))
	for p := range a.owns {
		out = append(out, p)
	}
	return out
}

func (a *actor) remember(path string, epoch int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.owns[path] = epoch
}

// forget drops a ticket from my working set: after a release, after
// self-fencing, or when simulating a crashed runner that simply stops
// touching its claim (the lease then has to expire).
func (a *actor) forget(path string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.owns, path)
}

// opAbandon simulates a dead runner: it keeps the claim (nothing is pushed)
// and never touches the ticket again.
func (a *actor) opAbandon(owned []string) {
	a.m.attempt("abandon")
	path := owned[a.rng.IntN(len(owned))]
	a.forget(path)
	a.m.success("abandon", 0)
}

// ---------------------------------------------------------------- add ----

// opAdd allocates the next free number on the fresh head, creates the ticket
// and pushes. On a rejected push it starts over from the fresh head with the
// then-highest number, as docs/CONCEPT.md prescribes.
func (a *actor) opAdd() {
	const op = "add"
	start := time.Now()
	a.m.attempt(op)
	deadline := start.Add(a.cfg.TotalTimeout)
	for attempt := 0; attempt < a.cfg.MaxAttempts; attempt++ {
		if r := git(a.dir, nil, "fetch", "origin", "tickets"); r.err != nil {
			a.m.finalFail(op)
			return
		}
		n := maxTicketNumber(a.dir) + 1
		// A small random spread makes deliberate collisions part of the mix.
		n += a.rng.IntN(3)
		path := ticketPath(n)
		if r := git(a.dir, nil, "cat-file", "-e", "FETCH_HEAD:"+path); r.err == nil {
			continue // number taken on this head; retry from the fresh max
		}
		if r := git(a.dir, nil, "reset", "--hard", "FETCH_HEAD"); r.err != nil {
			a.m.finalFail(op)
			return
		}
		t := newTicket(fmt.Sprintf("app-%d", n), a.id, time.Now())
		switch a.pushTicket(op, path, t) {
		case pushOK:
			a.m.success(op, elapsedMs(start))
			return
		case pushRejected:
			if time.Now().After(deadline) {
				a.m.finalFail(op)
				return
			}
			time.Sleep(backoff(a.cfg, attempt, a.rng))
		case pushError:
			return
		}
	}
	a.m.finalFail(op) // out of attempts (timeout)
}

// ------------------------------------------------------- claim / take ----

// opClaim tries to claim one ticket: a free one, or one whose lease expired
// (takeover). Claims on a ticket I already own are idempotent.
func (a *actor) opClaim() {
	const op = "claim"
	cands := a.claimable()
	if len(cands) == 0 {
		return
	}
	path := cands[a.rng.IntN(len(cands))]

	// Idempotency: claiming a ticket I already own succeeds without a push.
	if t := a.headTicket(path); t != nil && t.get(fOwner) == a.id {
		a.m.attempt(op)
		a.m.idempotent(op)
		a.remember(path, t.geti(fEpoch))
		return
	}

	takeover := false
	heldEpoch := 0
	start := time.Now()
	a.m.attempt(op)
	ok := a.writeLoop(op, path,
		func(t *ticket) (string, bool) {
			now := time.Now()
			switch {
			case t.get(fOwner) == "":
				return "", true
			case t.get(fOwner) == a.id:
				return "idempotent-race", false
			default:
				if lu, okLU := t.time(fLeaseUntil); okLU && lu.Add(a.cfg.Grace).Before(now) {
					takeover = true
					return "", true
				}
				return "claimed-by-other", false
			}
		},
		func(t *ticket, now time.Time) {
			t.set(fOwner, a.id)
			t.set(fKind, "agent")
			t.set(fSize, "M")
			t.stamp(fClaimedAt, now)
			t.stamp(fLeaseUntil, now.Add(a.cfg.Lease))
			t.seti(fEpoch, t.geti(fEpoch)+1)
			t.seti(fRev, t.geti(fRev)+1)
			t.seti(fExtensions, 0)
			if t.get(fStage) == "open" || takeover {
				t.set(fStage, "in-progress")
			}
			name := "claim"
			if takeover {
				name = "takeover"
			}
			t.appendLogEvent(a.id, name, now)
			heldEpoch = t.geti(fEpoch)
		})
	if ok {
		a.m.success(op, elapsedMs(start))
		a.remember(path, heldEpoch)
		// Dead-runner simulation: the process dies right after winning the
		// claim; the lease has to expire before anyone else can take over.
		if a.rng.Float64() < a.cfg.CrashP {
			a.forget(path)
		}
	}
}

// ------------------------------------------------------------- update ----

func (a *actor) opUpdate(owned []string) {
	const op = "update"
	path := owned[a.rng.IntN(len(owned))]
	a.mu.Lock()
	epoch := a.owns[path]
	a.mu.Unlock()

	fenced := false
	start := time.Now()
	a.m.attempt(op)
	ok := a.writeLoop(op, path,
		func(t *ticket) (string, bool) {
			if t.get(fOwner) != a.id {
				fenced = true
				return "claim-lost", false
			}
			if t.geti(fEpoch) != epoch {
				fenced = true
				return "epoch-mismatch", false
			}
			return "", true
		},
		func(t *ticket, now time.Time) {
			t.seti(fRev, t.geti(fRev)+1)
			t.appendLogEvent(a.id, "update", now)
		})
	if ok {
		a.m.success(op, elapsedMs(start))
	}
	if fenced {
		a.forget(path) // self-fencing: stop touching this ticket
	}
}

// ------------------------------------------------------------ release ----

func (a *actor) opRelease(owned []string) {
	const op = "release"
	path := owned[a.rng.IntN(len(owned))]
	a.mu.Lock()
	epoch := a.owns[path]
	a.mu.Unlock()

	start := time.Now()
	a.m.attempt(op)
	ok := a.writeLoop(op, path,
		func(t *ticket) (string, bool) {
			if t.get(fOwner) != a.id {
				return "claim-lost", false
			}
			if t.geti(fEpoch) != epoch {
				return "epoch-mismatch", false
			}
			return "", true
		},
		func(t *ticket, now time.Time) {
			t.set(fOwner, "")
			t.set(fClaimedAt, "")
			t.set(fLeaseUntil, "")
			t.seti(fExtensions, 0)
			if t.get(fStage) == "in-progress" {
				t.set(fStage, "review")
			}
			t.seti(fRev, t.geti(fRev)+1)
			t.appendLogEvent(a.id, "release", now)
		})
	if ok {
		a.m.success(op, elapsedMs(start))
	}
	a.forget(path)
}

// --------------------------------------------------------- write loop ----

type precondFunc func(*ticket) (string, bool)
type mutateFunc func(*ticket, time.Time)

type pushOutcome int

const (
	pushOK pushOutcome = iota
	pushRejected
	pushError
)

// writeLoop is the generic write protocol: fetch, check preconditions on
// the fresh head, mutate, commit, push; on a rejected push back off and
// start over from a fresh fetch. Preconditions are re-checked on every
// attempt, so a claim lost in between is noticed, not overwritten.
func (a *actor) writeLoop(op, path string, precond precondFunc, mutate mutateFunc) bool {
	deadline := time.Now().Add(a.cfg.TotalTimeout)
	for attempt := 0; attempt < a.cfg.MaxAttempts; attempt++ {
		if r := git(a.dir, nil, "fetch", "origin", "tickets"); r.err != nil {
			a.m.finalFail(op)
			return false
		}
		if r := git(a.dir, nil, "reset", "--hard", "FETCH_HEAD"); r.err != nil {
			a.m.finalFail(op)
			return false
		}
		content, err := os.ReadFile(filepath.Join(a.dir, filepath.FromSlash(path)))
		if err != nil {
			a.m.precond(op, "not-found")
			return false
		}
		t, err := parseTicket(string(content))
		if err != nil {
			a.m.finalFail(op)
			return false
		}
		if reason, ok := precond(t); !ok {
			a.m.precond(op, reason)
			return false
		}
		mutate(t, time.Now())
		switch a.pushTicket(op, path, t) {
		case pushOK:
			return true
		case pushRejected:
			if time.Now().After(deadline) {
				a.m.finalFail(op)
				return false
			}
			time.Sleep(backoff(a.cfg, attempt, a.rng))
		case pushError:
			return false
		}
	}
	a.m.finalFail(op)
	return false
}

// pushTicket writes the ticket file, commits it and pushes once. Retry
// decisions belong to the callers.
func (a *actor) pushTicket(op, path string, t *ticket) pushOutcome {
	full := filepath.Join(a.dir, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		a.m.finalFail(op)
		return pushError
	}
	if err := os.WriteFile(full, []byte(t.String()), 0o644); err != nil {
		a.m.finalFail(op)
		return pushError
	}
	if r := git(a.dir, a.env, "add", "--", path); r.err != nil {
		a.m.finalFail(op)
		return pushError
	}
	if r := git(a.dir, a.env, "commit", "-m",
		fmt.Sprintf("spike: %s %s by %s", op, t.get(fID), a.id)); r.err != nil {
		a.m.finalFail(op)
		return pushError
	}
	push := git(a.dir, nil, "push", "origin", "HEAD:refs/heads/tickets")
	switch {
	case push.err == nil:
		a.m.push()
		a.acc.add(path, lastLogTag(t))
		return pushOK
	case nonFastForward(push):
		a.m.reject(op)
		return pushRejected
	default:
		a.m.finalFail(op)
		return pushError
	}
}

// ------------------------------------------------------------ helpers ----

// claimable returns ticket paths that may be claimed right now: unowned, or
// owned with an expired lease (takeover candidates).
func (a *actor) claimable() []string {
	if r := git(a.dir, nil, "fetch", "origin", "tickets"); r.err != nil {
		return nil
	}
	paths := lsTreeTickets(a.dir)
	a.rng.Shuffle(len(paths), func(i, j int) { paths[i], paths[j] = paths[j], paths[i] })
	now := time.Now()
	var out []string
	for _, p := range paths {
		if len(out) >= 3 {
			break
		}
		r := git(a.dir, nil, "show", "FETCH_HEAD:"+p)
		if r.err != nil {
			continue
		}
		t, err := parseTicket(r.stdout)
		if err != nil {
			continue
		}
		if t.get(fOwner) == "" {
			out = append(out, p)
			continue
		}
		if lu, ok := t.time(fLeaseUntil); ok && lu.Add(a.cfg.Grace).Before(now) {
			out = append(out, p)
		}
	}
	return out
}

// headTicket fetches and returns the current remote state of one ticket.
func (a *actor) headTicket(path string) *ticket {
	if r := git(a.dir, nil, "fetch", "origin", "tickets"); r.err != nil {
		return nil
	}
	r := git(a.dir, nil, "show", "FETCH_HEAD:"+path)
	if r.err != nil {
		return nil
	}
	t, err := parseTicket(r.stdout)
	if err != nil {
		return nil
	}
	return t
}

func lsTreeTickets(dir string) []string {
	r := git(dir, nil, "ls-tree", "-r", "--name-only", "FETCH_HEAD", "--", "tickets/")
	var out []string
	for _, l := range lines(r.stdout) {
		if strings.HasSuffix(l, "/ticket.md") {
			out = append(out, l)
		}
	}
	return out
}

// maxTicketNumber scans the ticket paths and returns the highest number in
// use, for ID allocation.
func maxTicketNumber(dir string) int {
	max := 0
	for _, p := range lsTreeTickets(dir) {
		parts := strings.Split(p, "/")
		if len(parts) != 5 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimPrefix(parts[3], "app-"))
		if err == nil && n > max {
			max = n
		}
	}
	return max
}

// ticketPath builds tickets/app/<shard>/app-<n>/ticket.md; the shard is
// n/1000 in four digits, the path never changes.
func ticketPath(n int) string {
	return fmt.Sprintf("tickets/app/%04d/app-%d/ticket.md", n/1000, n)
}

func backoff(cfg *config, attempt int, rng *rand.Rand) time.Duration {
	d := cfg.BackoffBase << attempt
	if d > cfg.BackoffCap || d <= 0 {
		d = cfg.BackoffCap
	}
	jitter := 0.5 + rng.Float64()
	return time.Duration(float64(d) * jitter)
}

func elapsedMs(start time.Time) float64 {
	return float64(time.Since(start).Nanoseconds()) / 1e6
}
