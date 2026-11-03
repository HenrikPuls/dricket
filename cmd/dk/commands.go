// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/HenrikPuls/dricket/internal/profile"
	"github.com/HenrikPuls/dricket/internal/service"
	"github.com/HenrikPuls/dricket/internal/store"
	"github.com/HenrikPuls/dricket/internal/ticket"
)

// serviceCmds are the stage v0 and v1 commands; each maps to one service call.
var serviceCmds = []command{
	{name: "add", synopsis: "create a ticket", run: runAdd},
	{name: "claim", synopsis: "take a ticket", run: runClaim},
	{name: "sync", synopsis: "fast-forward a clean ticket checkout", run: runSync},
	{name: "restore", synopsis: "discard local changes to one path", run: runRestore},
	{name: "commit", synopsis: "validate and publish an edited ticket", run: runCommit},
	{name: "release", synopsis: "publish and free a ticket", run: runRelease},
	{name: "extend", synopsis: "extend the lease of a ticket", run: runExtend},
	{name: "note", synopsis: "add a progress note, keep the claim", run: runNote},
	{name: "comment", synopsis: "append a comment", run: runComment},
	{name: "set", synopsis: "change a free field", run: runSet},
	{name: "ls", synopsis: "list tickets", run: runLs},
	{name: "next", synopsis: "next suitable ticket", run: runNext},
	{name: "search", synopsis: "full-text search", run: runSearch},
	{name: "validate", synopsis: "deep checks over store or code range", run: runValidate},
	{name: "leases", synopsis: "show leases and their status", run: runLeases},
	{name: "inbox", synopsis: "what is new for a ticket", run: runInbox},
	{name: "show", synopsis: "display a ticket", run: runShow},
	{name: "force-release", synopsis: "admin release with a reason", run: runForceRelease},
	{name: "lint", synopsis: "check tickets", run: runLint},
	{name: "doctor", synopsis: "check the local setup", run: runDoctor},
}

// rootCmds are the commands that work without a store behind them.
var rootCmds = []command{
	{name: "version", synopsis: "print the tool version", run: runVersion},
	{name: "init", synopsis: "set up a ticket store in a directory or at a URL", run: runInit},
	{name: "use", synopsis: "bind store and project prefix for this directory", run: runUse},
	{name: "setup-agent", synopsis: "point AGENTS.md at this store", run: runSetupAgent},
	{name: "guide", synopsis: "print the agent guide", run: runGuide},
}

func init() {
	registry = append(rootCmds, serviceCmds...)
}

// identity resolves the profile for this invocation: --profile or
// DK_PROFILE selects an entry from the user's config.toml; without one,
// the default profile (config entry or the OS identity). DK_OWNER
// overrides the owner id.
func (e *env) identity() (profile.Profile, error) {
	name := e.profile
	if name == "" {
		name = os.Getenv("DK_PROFILE")
	}
	prof, err := profile.Resolve(name)
	if err != nil {
		return profile.Profile{}, err
	}
	if owner := os.Getenv("DK_OWNER"); owner != "" {
		prof.OwnerID = strings.ToLower(owner)
	}
	return prof, nil
}

// svc opens the ticket store for this invocation. The store spec comes
// from --store or DK_STORE, or - git-like - from walking up the current
// directory: the first store root (git repo with dricket.toml) or the
// nearest `.dricket` binding is used.
func (e *env) svc() (*service.Service, *cliError) {
	if e.svcCache != nil {
		return e.svcCache, nil
	}
	spec, _, cerr := e.storeResolution(context.Background())
	if cerr != nil {
		return nil, cerr
	}
	if spec == "" {
		return nil, usageError(noStoreHint)
	}
	remote, branch, found := strings.Cut(spec, "#")
	opts := store.Options{Remote: remote, Branch: branch}
	if found && branch == "" {
		return nil, usageError("store %q: empty branch after '#'", spec)
	}
	prof, err := e.identity()
	if err != nil {
		return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
	}
	opts.Profile = prof
	// Runner profiles carry their own key: nothing may leak in from the
	// user's global git configuration (docs/CONCEPT.md, profiles).
	opts.IsolateGitConfig = prof.SSHKey != ""
	// DK_DATA_DIR overrides where private working copies live (tests and
	// machines with several checkouts).
	if d := os.Getenv("DK_DATA_DIR"); d != "" {
		opts.DataDir = d
	}
	st, err := store.Open(context.Background(), opts)
	if err != nil {
		return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
	}
	e.svcCache = service.New(st, prof.OwnerID)
	// "dev" skips the min_version check of dricket.toml; released builds
	// carry their real version via ldflags.
	e.svcCache.ToolVersion = version
	return e.svcCache, nil
}

// wrapErr maps a service error to the CLI error with its exit code.
func wrapErr(err error) *cliError {
	if err == nil {
		return nil
	}
	return &cliError{code: service.ExitCode(err), kind: service.KindOf(err), msg: err.Error()}
}

func newFlagSet(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parseFlags parses after the command has defined its flags.
func parseFlags(fs *flag.FlagSet, args []string) *cliError {
	var options, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if a == "-" || !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		name := a
		if eq := strings.IndexByte(name, '='); eq >= 0 {
			name = name[:eq]
		}
		registered := fs.Lookup(strings.TrimLeft(name, "-"))
		options = append(options, a)
		if registered == nil || strings.Contains(a, "=") {
			continue
		}
		if boolean, ok := registered.Value.(interface{ IsBoolFlag() bool }); ok && boolean.IsBoolFlag() {
			continue
		}
		if i+1 >= len(args) {
			return usageError("%s: %s requires a value (see --help)", fs.Name(), a)
		}
		i++
		options = append(options, args[i])
	}
	ordered := append(options, "--")
	ordered = append(ordered, positional...)
	if err := fs.Parse(ordered); err != nil {
		return usageError("%s: %v (see --help)", fs.Name(), err)
	}
	return nil
}

func readText(flagValue, fileValue string) (string, *cliError) {
	switch {
	case fileValue != "":
		// #nosec G304 -- the -F flag reads exactly the file the user named; that is its purpose.
		b, err := os.ReadFile(fileValue)
		if err != nil {
			return "", &cliError{code: exitError, kind: "error", msg: err.Error()}
		}
		return string(b), nil
	case flagValue == "-":
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", &cliError{code: exitError, kind: "error", msg: err.Error()}
		}
		return string(b), nil
	default:
		return flagValue, nil
	}
}

func splitComma(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ------------------------------------------------------------------ add --

func runAdd(e *env, args []string) (any, *cliError) {
	fs := newFlagSet("add")
	title := fs.String("m", "", "ticket title")
	fs.StringVar(title, "message", "", "ticket title")
	desc := fs.String("d", "", "description ('-' for stdin)")
	fromFile := fs.String("F", "", "read the description from a file")
	labels := fs.String("l", "", "comma-separated labels")
	depends := fs.String("depends", "", "comma-separated ticket ids")
	parent := fs.String("parent", "", "parent ticket id")
	prefix := fs.String("prefix", "", "project prefix (default: DK_PROJECT, bindings, or the store's only registered prefix)")
	unclaimed := fs.Bool("unclaimed", false, "create the ticket without claiming it")
	size := fs.String("size", "M", "claim size S, M or L")
	kind := fs.String("kind", "agent", "creator kind agent or human")
	priority := fs.String("priority", "", "priority from the store's configured values (default from dricket.toml)")
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	if fs.NArg() != 0 {
		return nil, usageError("add: no positional arguments expected")
	}
	description, derr := readText(*desc, *fromFile)
	if derr != nil {
		return nil, derr
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	// Project prefix: flag, then DK_PROJECT, then bindings, then the
	// store's single registered prefix.
	prefixValue, prefixSrc, perr := e.prefixForAdd(context.Background(), *prefix, s)
	if perr != nil {
		return nil, perr
	}
	if prefixValue == "" {
		return nil, usageError("add: " + noPrefixHint)
	}
	if !e.json {
		e.msg("project: %s (%s)\n", prefixValue, prefixSrc)
	}
	id, err := s.Add(context.Background(), service.AddRequest{
		Prefix:      prefixValue,
		Title:       *title,
		Description: description,
		Labels:      splitComma(*labels),
		Depends:     splitComma(*depends),
		Parent:      *parent,
		Unclaimed:   *unclaimed,
		Size:        ticket.Size(strings.ToUpper(*size)),
		Kind:        ticket.Kind(*kind),
		Priority:    *priority,
	})
	if err != nil {
		return nil, wrapErr(err)
	}
	if !e.json {
		e.outf("%s\n", id)
	}
	return map[string]any{"id": id}, nil
}

// --------------------------------------------------------------- claim --

func runClaim(e *env, args []string) (any, *cliError) {
	fs := newFlagSet("claim")
	size := fs.String("size", "M", "lease size S, M or L")
	kind := fs.String("kind", "agent", "actor kind agent or human")
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	if fs.NArg() != 1 {
		return nil, usageError("claim: exactly one ticket id expected")
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	res, err := s.Claim(context.Background(), fs.Arg(0), ticket.Size(strings.ToUpper(*size)), ticket.Kind(*kind))
	if err != nil {
		return nil, wrapErr(err)
	}
	if !e.json {
		what := "claimed"
		if res.Takeover {
			what = "took over"
		}
		e.outf("%s %s (epoch %d, lease until %s)\n", what, res.ID, res.Epoch, res.LeaseUntil)
	}
	return res, nil
}

// ------------------------------------------------------------- release --

func runRelease(e *env, args []string) (any, *cliError) {
	fs := newFlagSet("release")
	stage := fs.String("stage", "review", "target stage")
	result := fs.String("m", "", "result note")
	fs.StringVar(result, "message", "", "result note")
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	if fs.NArg() != 1 {
		return nil, usageError("release: exactly one ticket id expected")
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	res, err := s.Release(context.Background(), fs.Arg(0), ticket.Stage(*stage), *result)
	if err != nil {
		return nil, wrapErr(err)
	}
	if !e.json {
		e.outf("released %s -> %s\n", res.ID, res.Stage)
	}
	return res, nil
}

// ------------------------------------------------------------- comment --

func runComment(e *env, args []string) (any, *cliError) {
	fs := newFlagSet("comment")
	message := fs.String("m", "", "comment text")
	fs.StringVar(message, "message", "", "comment text")
	file := fs.String("F", "", "read the comment from a file")
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	if fs.NArg() != 1 || (*message == "" && *file == "") {
		return nil, usageError("comment: a ticket id and -m/--message or -F file are required")
	}
	id := fs.Arg(0)
	text := *message
	if *file != "" {
		b, err := os.ReadFile(*file) // #nosec G304 -- the -F flag reads exactly the named file
		if err != nil {
			return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
		}
		text = string(b)
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	f, err := s.Comment(context.Background(), id, text)
	if err != nil {
		return nil, wrapErr(err)
	}
	if !e.json {
		e.outf("%s\n", f)
	}
	return map[string]any{"id": id, "file": f}, nil
}

// ------------------------------------------------------------------ set --

func runSet(e *env, args []string) (any, *cliError) {
	fs := newFlagSet("set")
	ifVersion := fs.Int("if-version", 0, "expected rev of the ticket")
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	if fs.NArg() != 2 {
		return nil, usageError("set: expected <id> key=value")
	}
	key, value, ok := strings.Cut(fs.Arg(1), "=")
	if !ok {
		return nil, usageError("set: expected key=value, got %q", fs.Arg(1))
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	res, err := s.Set(context.Background(), fs.Arg(0), key, value, *ifVersion)
	if err != nil {
		return nil, wrapErr(err)
	}
	if !e.json {
		e.outf("%s %s = %s (rev %d)\n", res.ID, res.Key, res.Value, res.Rev)
	}
	return res, nil
}

// ------------------------------------------------------------------ ls --

func runLs(e *env, args []string) (any, *cliError) {
	fs := newFlagSet("ls")
	stage := fs.String("s", "", "filter by stage")
	owner := fs.String("o", "", "filter by owner (@me)")
	prefix := fs.String("p", "", "filter by project prefix")
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	items, warnings, err := s.List(context.Background(), service.ListFilter{
		Stage: *stage, Owner: *owner, Prefix: *prefix,
	})
	if err != nil {
		return nil, wrapErr(err)
	}
	for _, w := range warnings {
		e.msg("warning: %s\n", w)
	}
	if !e.json {
		for _, it := range items {
			e.outf("%-14s %-12s %-8s %-20s %s\n", it.ID, it.Stage, it.Priority, it.Owner, it.Title)
		}
		if len(items) == 0 {
			e.msg("no tickets\n")
		}
	}
	return map[string]any{"tickets": items}, nil
}

// ---------------------------------------------------------------- show --

func runShow(e *env, args []string) (any, *cliError) {
	fs := newFlagSet("show")
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	if fs.NArg() != 1 {
		return nil, usageError("show: exactly one ticket id expected")
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	content, err := s.Show(context.Background(), fs.Arg(0))
	if err != nil {
		return nil, wrapErr(err)
	}
	if !e.json {
		e.outf("%s", content)
	}
	return map[string]any{"id": strings.ToLower(fs.Arg(0)), "content": content}, nil
}

// ---------------------------------------------------------------- lint --

func runLint(e *env, args []string) (any, *cliError) {
	fs := newFlagSet("lint")
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	id := ""
	if fs.NArg() == 1 {
		id = fs.Arg(0)
	} else if fs.NArg() != 0 {
		return nil, usageError("lint: at most one ticket id expected")
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	results, err := s.Lint(context.Background(), id)
	if err != nil {
		return nil, wrapErr(err)
	}
	errs := 0
	for _, r := range results {
		for _, f := range r.Findings {
			if strings.HasPrefix(f, "error") {
				errs++
			}
		}
	}
	if errs > 0 {
		var b strings.Builder
		for _, r := range results {
			for _, f := range r.Findings {
				fmt.Fprintln(&b, r.ID+": "+f)
			}
		}
		return nil, &cliError{
			code: exitInvalid,
			kind: service.KindInvalid,
			msg:  fmt.Sprintf("lint found %d error(s):\n%s", errs, strings.TrimRight(b.String(), "\n")),
		}
	}
	if !e.json {
		for _, r := range results {
			for _, f := range r.Findings {
				e.outf("%s: %s\n", r.ID, f)
			}
		}
		if len(results) == 0 {
			e.msg("lint ok\n")
		}
	}
	return map[string]any{"tickets": results, "errors": errs}, nil
}
