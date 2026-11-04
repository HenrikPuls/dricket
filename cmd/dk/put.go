// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"strings"

	"github.com/HenrikPuls/dricket/internal/service"
)

// repeatable is a string flag that may be given several times.
type repeatable []string

func (r *repeatable) String() string     { return strings.Join(*r, ",") }
func (r *repeatable) Set(v string) error { *r = append(*r, v); return nil }
func (r *repeatable) Get() any           { return []string(*r) }

// versionFlag returns the value of --if-version when it was given. Zero is a
// real version (an unclaimed ticket that was never written again), so
// "given" cannot be read from the value.
func versionFlag(fs *flag.FlagSet) *int {
	var out *int
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "if-version" {
			v := intFlag(fs, "if-version")
			out = &v
		}
	})
	return out
}

// parsePairs splits key=value arguments; the value may be empty (an empty
// extension value removes the key).
func parsePairs(cmd string, args []string) ([]service.FieldValue, *cliError) {
	pairs := make([]service.FieldValue, 0, len(args))
	for _, a := range args {
		key, value, ok := strings.Cut(a, "=")
		if !ok || key == "" {
			return nil, usageError("%s: expected key=value, got %q", cmd, a)
		}
		pairs = append(pairs, service.FieldValue{Key: key, Value: value})
	}
	return pairs, nil
}

// ------------------------------------------------------------------ put --

func newPutFlagSet() *flag.FlagSet {
	fs := newFlagSet("put")
	fs.String("F", "", "read the complete ticket.md from a file (or pass '-' for stdin)")
	fs.Int("if-version", 0, "expected rev of the ticket (required unless you hold the claim)")
	fs.Bool("force", false, "admin: overwrite a ticket that someone else has claimed")
	fs.Bool("claim", false, "take and release a short claim inside the one commit (required on unclaimed tickets)")
	return fs
}

func runPut(e *env, args []string) (any, *cliError) {
	fs := newPutFlagSet()
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	file := stringFlag(fs, "F")
	stdin := fs.NArg() == 2 && fs.Arg(1) == "-"
	if fs.NArg() < 1 || fs.NArg() > 2 || (fs.NArg() == 2 && !stdin) || (file == "" && !stdin) || (file != "" && stdin) {
		return nil, usageError("put: expected <id> and the document from -F <file> or '-' (stdin)")
	}
	var content string
	var derr *cliError
	if stdin {
		content, derr = readText("-", "")
	} else {
		content, derr = readText("", file)
	}
	if derr != nil {
		return nil, derr
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	res, err := s.Put(context.Background(), fs.Arg(0), content, service.PutOptions{
		IfVersion: versionFlag(fs),
		Force:     boolFlag(fs, "force"),
		Claim:     boolFlag(fs, "claim"),
	})
	if err != nil {
		return nil, wrapErr(err)
	}
	if !e.json {
		if len(res.ChangedFields) == 0 {
			e.outf("%s unchanged (rev %d)\n", res.ID, res.Rev)
		} else {
			e.outf("%s rev %d (%s)\n", res.ID, res.Rev, strings.Join(res.ChangedFields, ", "))
		}
	}
	return res, nil
}


