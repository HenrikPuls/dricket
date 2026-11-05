// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/HenrikPuls/dricket/internal/frontmatter"
	"github.com/HenrikPuls/dricket/internal/ticket"
)

// NextOptions narrows `dk next`; Claim takes the chosen ticket directly.
type NextOptions struct {
	Claim  bool
	Size   ticket.Size // claim size when Claim is set
	Prefix string
}

// NextResult reports the chosen ticket and, with --claim, the claim.
type NextResult struct {
	ID       string       `json:"id"`
	Title    string       `json:"title"`
	Stage    string       `json:"stage"`
	Priority string       `json:"priority,omitempty"`
	Epoch    int          `json:"epoch"`
	Claim    *ClaimResult `json:"claim,omitempty"`
}

// eligible reports whether a ticket can be worked on now: free and open,
// or its lease expired past the grace period (a takeover candidate), with
// every scheduling dependency done or cancelled. Parent is hierarchy only.
func eligible(tk ticket.Ticket, stages map[string]ticket.Stage, set ticket.StageSet, now time.Time) bool {
	free := tk.Owner == "" && set.Pickable(tk.Stage)
	expired := tk.Owner != "" && tk.LeaseUntil.Add(Grace).Before(now)
	if !free && !expired {
		return false
	}
	for _, dep := range tk.Depends {
		s, ok := stages[dep]
		if !ok || !set.IsTerminal(s) {
			return false
		}
	}
	return true
}

// Next picks the next suitable ticket: highest configured priority first
// (the order of [priorities].values defines the rank), then the lowest
// number within the lowest prefix. Kind not-found (exit 10) when nothing
// qualifies.
func (s *Service) Next(ctx context.Context, opts NextOptions) (*NextResult, error) {
	if err := s.Store.Refresh(ctx); err != nil {
		return nil, wrapRead(err)
	}
	cfg, cerr := s.config(ctx)
	if cerr != nil {
		return nil, cerr
	}
	rank := map[string]int{}
	for i, v := range cfg.PriorityValues {
		rank[v] = i
	}
	// Unset or unknown priorities rank last.
	rankOf := func(p string) int {
		if r, ok := rank[p]; ok {
			return r
		}
		return len(cfg.PriorityValues)
	}
	files, err := s.Store.ReadAll(ctx, "tickets/", "/ticket.md")
	if err != nil {
		return nil, wrapRead(err)
	}

	stages := map[string]ticket.Stage{}
	var all []ticket.Ticket
	for _, file := range files {
		doc, err := frontmatter.Parse(file.Content)
		if err != nil {
			continue
		}
		tk, _, err := ticket.FromDoc(doc)
		if err != nil {
			continue
		}
		stages[tk.ID] = tk.Stage
		if opts.Prefix != "" {
			if prefix, _, err := splitID(tk.ID); err != nil || prefix != strings.ToLower(opts.Prefix) {
				continue
			}
		}
		all = append(all, tk)
	}

	var candidates []ticket.Ticket
	for _, tk := range all {
		if eligible(tk, stages, cfg.Stages, s.Now()) {
			candidates = append(candidates, tk)
		}
	}
	if len(candidates) == 0 {
		return nil, errf(KindNotFound, "no suitable ticket (open and free, or lease expired, dependencies done)")
	}
	sort.Slice(candidates, func(i, j int) bool {
		r1, r2 := rankOf(candidates[i].Priority), rankOf(candidates[j].Priority)
		if r1 != r2 {
			return r1 < r2
		}
		p1, n1, e1 := splitID(candidates[i].ID)
		p2, n2, e2 := splitID(candidates[j].ID)
		if e1 != nil || e2 != nil {
			return candidates[i].ID < candidates[j].ID
		}
		if p1 != p2 {
			return p1 < p2
		}
		return n1 < n2
	})
	best := candidates[0]

	res := &NextResult{ID: best.ID, Title: best.Title, Stage: string(best.Stage), Priority: best.Priority, Epoch: best.Epoch}
	if opts.Claim {
		size := opts.Size
		if size == "" {
			size = ticket.SizeM
		}
		claim, err := s.Claim(ctx, best.ID, size, ticket.KindAgent)
		if err != nil {
			return nil, err
		}
		res.Claim = &claim
		res.Epoch = claim.Epoch
		res.Stage = string(cfg.Stages.AfterClaim(best.Stage, claim.Takeover))
	}
	return res, nil
}
