// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/HenrikPuls/dricket/internal/service"
)

// --------------------------------------------------------------- extend --

func runExtend(e *env, args []string) (any, *cliError) {
	fs := newFlagSet("extend")
	reason := fs.String("reason", "", "why the lease is extended (required)")
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	if fs.NArg() != 1 {
		return nil, usageError("extend: exactly one ticket id expected")
	}
	if *reason == "" {
		return nil, usageError("extend: --reason is required")
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	res, err := s.Extend(context.Background(), fs.Arg(0), *reason)
	if err != nil {
		return nil, wrapErr(err)
	}
	if !e.json {
		e.outf("extended %s (extensions %d/%d, lease until %s)\n",
			res.ID, res.Extensions, res.MaxExtend, res.LeaseUntil)
	}
	return res, nil
}

// --------------------------------------------------------------- update --

// update keeps the claim and records progress; in stage v1 it carries the
// note, body editing through the tool comes later.
func runUpdate(e *env, args []string) (any, *cliError) {
	fs := newFlagSet("update")
	note := fs.String("note", "", "progress note (required in stage v1)")
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	if fs.NArg() != 1 {
		return nil, usageError("update: exactly one ticket id expected")
	}
	if *note == "" {
		return nil, usageError("update: --note is required in stage v1")
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	res, err := s.Note(context.Background(), fs.Arg(0), *note)
	if err != nil {
		return nil, wrapErr(err)
	}
	if !e.json {
		e.outf("updated %s (rev %d, claim kept)\n", res.ID, res.Rev)
	}
	return res, nil
}

// ----------------------------------------------------------------- note --

func runNote(e *env, args []string) (any, *cliError) {
	fs := newFlagSet("note")
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	if fs.NArg() < 2 {
		return nil, usageError("note: a ticket id and the text are required")
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	res, err := s.Note(context.Background(), fs.Arg(0), strings.Join(fs.Args()[1:], " "))
	if err != nil {
		return nil, wrapErr(err)
	}
	if !e.json {
		e.outf("noted on %s (rev %d)\n", res.ID, res.Rev)
	}
	return res, nil
}

// --------------------------------------------------------------- leases --

func runLeases(e *env, args []string) (any, *cliError) {
	fs := newFlagSet("leases")
	expiring := fs.Bool("expiring", false, "only leases with less than 20 percent left")
	stale := fs.Bool("stale", false, "only expired leases (dead claims)")
	check := fs.Bool("check", false, "exit with 20 when expired leases exist")
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	if fs.NArg() != 0 {
		return nil, usageError("leases: no positional arguments expected")
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	infos, err := s.Leases(context.Background(), service.LeasesFilter{Expiring: *expiring, Stale: *stale})
	if err != nil {
		return nil, wrapErr(err)
	}
	if !e.json {
		for _, l := range infos {
			quiet := ""
			if l.Quiet {
				quiet = " quiet"
			}
			e.outf("%-14s %-20s %-4s %-20s %8s  %s%s\n",
				l.ID, l.Owner, l.Size, l.LeaseUntil, formatMinutes(l.RemainingMin), l.Status, quiet)
		}
		if len(infos) == 0 {
			e.msg("no matching leases\n")
		}
	}
	if *check && service.HasExpired(infos) {
		var expired []string
		for _, l := range infos {
			if l.Status == service.LeaseExpired {
				expired = append(expired, fmt.Sprintf("%s (owner %s, lease ended %s)", l.ID, l.Owner, l.LeaseUntil))
			}
		}
		return nil, &cliError{
			code: exitLeasesExpired,
			kind: "leases-expired",
			msg: fmt.Sprintf("%d expired lease(s): %s", len(expired),
				strings.Join(expired, "; ")),
		}
	}
	return map[string]any{"leases": infos}, nil
}

// ----------------------------------------------------------------- inbox --

func runInbox(e *env, args []string) (any, *cliError) {
	fs := newFlagSet("inbox")
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	if fs.NArg() != 1 {
		return nil, usageError("inbox: exactly one ticket id expected")
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	res, err := s.Inbox(context.Background(), fs.Arg(0))
	if err != nil {
		return nil, wrapErr(err)
	}
	if !e.json {
		if res.FirstSeen {
			e.outf("%s: first sight (rev %d, owner %s); marker set\n", res.ID, res.Rev, res.Owner)
			return nil, nil
		}
		for _, l := range res.NewLog {
			e.outf("log %s\n", strings.TrimPrefix(l, "- "))
		}
		for _, c := range res.NewComments {
			e.outf("comment %s %s: %s\n", c.CreatedAt, c.Author, c.Excerpt)
		}
		if len(res.NewLog) == 0 && len(res.NewComments) == 0 {
			e.outf("%s: nothing new (rev %d)\n", res.ID, res.Rev)
		}
	}
	return res, nil
}

// formatMinutes renders a remaining time compactly; minutes resolution is
// enough for a status column.
func formatMinutes(minutes int) string {
	return (time.Duration(minutes) * time.Minute).String()
}
