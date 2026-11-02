// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"sort"
	"sync"
	"time"
)

// opStats counts the outcomes of one operation type.
type opStats struct {
	Attempts   int
	Success    int
	Idempotent int
	PushReject int
	Precond    map[string]int `json:",omitempty"`
	FinalFail  int
}

// pct holds latency percentiles in milliseconds.
type pct struct {
	N   int     `json:"n"`
	P50 float64 `json:"p50_ms"`
	P95 float64 `json:"p95_ms"`
	P99 float64 `json:"p99_ms"`
	Max float64 `json:"max_ms"`
}

// metrics collects counters and latencies across all actors.
type metrics struct {
	mu      sync.Mutex
	ops     map[string]*opStats
	lat     map[string][]float64
	pushed  int
	started time.Time
}

func newMetrics() *metrics {
	return &metrics{
		ops:     map[string]*opStats{},
		lat:     map[string][]float64{},
		started: time.Now(),
	}
}

func (m *metrics) stat(op string) *opStats {
	if s, ok := m.ops[op]; ok {
		return s
	}
	s := &opStats{Precond: map[string]int{}}
	m.ops[op] = s
	return s
}

func (m *metrics) attempt(op string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stat(op).Attempts++
}

func (m *metrics) precond(op, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.stat(op)
	if s.Precond == nil {
		s.Precond = map[string]int{}
	}
	s.Precond[reason]++
}

func (m *metrics) reject(op string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stat(op).PushReject++
}

func (m *metrics) finalFail(op string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stat(op).FinalFail++
}

func (m *metrics) idempotent(op string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stat(op).Idempotent++
}

func (m *metrics) success(op string, durMs float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stat(op).Success++
	m.lat[op] = append(m.lat[op], durMs)
}

// push records one accepted push (one commit on the remote).
func (m *metrics) push() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pushed++
}

// snapshot returns copies for reporting.
func (m *metrics) snapshot() (map[string]opStats, map[string]pct, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	ops := map[string]opStats{}
	for k, v := range m.ops {
		ops[k] = *v
	}
	lat := map[string]pct{}
	for k, v := range m.lat {
		lat[k] = percentiles(v)
	}
	return ops, lat, m.pushed
}

func percentiles(vals []float64) pct {
	if len(vals) == 0 {
		return pct{}
	}
	s := append([]float64(nil), vals...)
	sort.Float64s(s)
	at := func(p float64) float64 {
		idx := int(p*float64(len(s))) - 1
		if idx < 0 {
			idx = 0
		}
		if idx >= len(s) {
			idx = len(s) - 1
		}
		return s[idx]
	}
	return pct{
		N:   len(s),
		P50: at(0.50),
		P95: at(0.95),
		P99: at(0.99),
		Max: s[len(s)-1],
	}
}

// accepted remembers the log tag of every successful push so the verifier
// can prove that no accepted update was lost (invariant 3).
type accepted struct {
	mu   sync.Mutex
	tags map[string]map[string]struct{}
}

func newAccepted() *accepted {
	return &accepted{tags: map[string]map[string]struct{}{}}
}

func (a *accepted) add(path, tag string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.tags[path] == nil {
		a.tags[path] = map[string]struct{}{}
	}
	a.tags[path][tag] = struct{}{}
}

func (a *accepted) snapshot() map[string][]string {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := map[string][]string{}
	for p, set := range a.tags {
		lst := make([]string, 0, len(set))
		for t := range set {
			lst = append(lst, t)
		}
		sort.Strings(lst) // stable output for the report
		out[p] = lst
	}
	return out
}
