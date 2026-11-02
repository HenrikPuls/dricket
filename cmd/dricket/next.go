// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"strings"

	"github.com/HenrikPuls/dricket/internal/service"
	"github.com/HenrikPuls/dricket/internal/ticket"
)

// ----------------------------------------------------------------- next --

func runNext(e *env, args []string) (any, *cliError) {
	fs := newFlagSet("next")
	claim := fs.Bool("claim", false, "claim the chosen ticket directly")
	size := fs.String("size", "M", "claim size S, M or L (with --claim)")
	prefix := fs.String("p", "", "restrict to a project prefix")
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	if fs.NArg() != 0 {
		return nil, usageError("next: no positional arguments expected")
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	res, err := s.Next(context.Background(), service.NextOptions{
		Claim:  *claim,
		Size:   ticket.Size(strings.ToUpper(*size)),
		Prefix: *prefix,
	})
	if err != nil {
		return nil, wrapErr(err)
	}
	if !e.json {
		if res.Claim != nil {
			e.outf("claimed %s (epoch %d, lease until %s)\n", res.ID, res.Claim.Epoch, res.Claim.LeaseUntil)
		} else {
			e.outf("%-14s %-11s %s\n", res.ID, res.Stage, res.Title)
		}
	}
	return res, nil
}

// --------------------------------------------------------------- search --

func runSearch(e *env, args []string) (any, *cliError) {
	fs := newFlagSet("search")
	prefix := fs.String("p", "", "restrict to a project prefix")
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	if fs.NArg() < 1 {
		return nil, usageError("search: a query is required")
	}
	query := strings.Join(fs.Args(), " ")
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	hits, err := s.Search(context.Background(), query, *prefix)
	if err != nil {
		return nil, wrapErr(err)
	}
	if !e.json {
		for _, h := range hits {
			e.outf("%s: %s\n", h.ID, h.Line)
		}
	}
	if len(hits) == 0 {
		return nil, &cliError{code: exitError, kind: "no-matches", msg: "no matches for " + query}
	}
	return map[string]any{"hits": hits}, nil
}
