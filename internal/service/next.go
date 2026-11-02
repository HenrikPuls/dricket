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
	ID    string       `json:"id"`
	Title string       `json:"title"`
	Stage string       `json:"stage"`
	Epoch int          `json:"epoch"`
	Claim *ClaimResult `json:"claim,omitempty"`
}

// eligible reports whether a ticket can be worked on now: free and open,
// or its lease expired past the grace period (a takeover candidate), with
// every dependency and the parent done or cancelled.
func eligible(tk ticket.Ticket, stages map[string]ticket.Stage, now time.Time) bool {
	free := tk.Owner == "" && tk.Stage == ticket.StageOpen
	expired := tk.Owner != "" && tk.LeaseUntil.Add(Grace).Before(now)
	if !free && !expired {
		return false
	}
	for _, dep := range tk.Depends {
		s, ok := stages[dep]
		if !ok || (s != ticket.StageDone && s != ticket.StageCancelled) {
			return false
		}
	}
	if tk.Parent != "" {
		s, ok := stages[tk.Parent]
		if !ok || (s != ticket.StageDone && s != ticket.StageCancelled) {
			return false
		}
	}
	return true
}

// Next picks the next suitable ticket: lowest number first within the
// lowest prefix. Kind not-found (exit 10) when nothing qualifies.
func (s *Service) Next(ctx context.Context, opts NextOptions) (*NextResult, error) {
	if err := s.Store.Refresh(ctx); err != nil {
		return nil, wrapRead(err)
	}
	paths, err := s.Store.List(ctx, "tickets/")
	if err != nil {
		return nil, wrapRead(err)
	}

	stages := map[string]ticket.Stage{}
	var all []ticket.Ticket
	for _, p := range paths {
		if !strings.HasSuffix(p, "/ticket.md") {
			continue
		}
		content, err := s.Store.ReadFile(ctx, p)
		if err != nil {
			continue
		}
		doc, err := frontmatter.Parse(content)
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
		if eligible(tk, stages, s.Now()) {
			candidates = append(candidates, tk)
		}
	}
	if len(candidates) == 0 {
		return nil, errf(KindNotFound, "no suitable ticket (open and free, or lease expired, dependencies done)")
	}
	sort.Slice(candidates, func(i, j int) bool {
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

	res := &NextResult{ID: best.ID, Title: best.Title, Stage: string(best.Stage), Epoch: best.Epoch}
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
		res.Stage = string(ticket.StageInProgress)
	}
	return res, nil
}
