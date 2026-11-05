// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/HenrikPuls/dricket/internal/service"
	"github.com/HenrikPuls/dricket/internal/validate"
)

// runValidate implements `dk validate`: the store check by default, and
// with --trailers <range> the epoch trailer gate for a code repository's
// commit range (run inside that repository).
func newValidateFlagSet() *flag.FlagSet {
	fs := newFlagSet("validate")
	fs.String("trailers", "", "check a git range in the current code repository for dricket trailers")
	return fs
}

func runValidate(e *env, args []string) (any, *cliError) {
	fs := newValidateFlagSet()
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	if fs.NArg() != 0 {
		return nil, usageError("validate: no positional arguments expected")
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	ctx := context.Background()

	if trailers := stringFlag(fs, "trailers"); trailers != "" {
		if err := s.Store.Refresh(ctx); err != nil {
			return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
		}
		if err := s.Store.Pin(ctx); err != nil {
			return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
		}
		readFile := func(path string) (string, error) { return s.Store.ReadFile(ctx, path) }
		findings, err := validate.Trailers(ctx, validate.ReadTicketFromStore(readFile), ".", trailers)
		if err != nil {
			return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
		}
		if !e.json {
			for _, f := range findings {
				e.outf("%s\n", f)
			}
		}
		if len(findings) > 0 {
			return nil, &cliError{
				code: exitInvalid,
				kind: service.KindInvalid,
				msg:  fmt.Sprintf("%d trailer violation(s)", len(findings)),
			}
		}
		if !e.json {
			e.msg("trailers ok\n")
		}
		return map[string]any{"violations": 0}, nil
	}

	findings, err := validate.Store(ctx, s.Store)
	if err != nil {
		return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
	}
	errs, warns := 0, 0
	for _, f := range findings {
		if f.Warning {
			warns++
		} else {
			errs++
		}
	}
	if !e.json {
		for _, f := range findings {
			prefix := ""
			if f.Warning {
				prefix = "warn "
			}
			e.outf("%s%s\n", prefix, f)
		}
		if len(findings) == 0 {
			e.msg("validate ok\n")
		}
	}
	if errs > 0 {
		return nil, &cliError{
			code: exitInvalid,
			kind: service.KindInvalid,
			msg:  fmt.Sprintf("validate found %d error(s) and %d warning(s)", errs, warns),
		}
	}
	return map[string]any{"findings": findings, "errors": 0, "warnings": warns}, nil
}
