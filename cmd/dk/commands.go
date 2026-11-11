// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/lariscus/dricket/internal/profile"
	"github.com/lariscus/dricket/internal/service"
	"github.com/lariscus/dricket/internal/store"
	"github.com/lariscus/dricket/internal/ticket"
)

// serviceCmds are the ticket commands; each maps to one service call.
var serviceCmds = []command{
	{name: "add", usage: "add [options]", group: "Work", synopsis: "create a ticket", flags: newAddFlagSet, run: runAdd},
	{name: "claim", usage: "claim <id> [options]", group: "Work", synopsis: "take a ticket", flags: newClaimFlagSet, run: runClaim},
	{name: "sync", usage: "sync", group: "Work", synopsis: "fast-forward a clean ticket checkout", run: runSync},
	{name: "restore", usage: "restore <path>", group: "Work", synopsis: "discard local changes to one path", run: runRestore},
	{name: "commit", usage: "commit <id> [options]", group: "Work", synopsis: "validate and publish an edited ticket", flags: newCommitFlagSet, run: runCommit},
	{name: "release", usage: "release <id> [options]", group: "Work", synopsis: "publish and free a ticket", flags: newReleaseFlagSet, run: runRelease},
	{name: "extend", usage: "extend <id> -m <reason>", group: "Work", synopsis: "extend the lease of a ticket", flags: newExtendFlagSet, run: runExtend},
	{name: "note", usage: "note <id> -m <text>", group: "Work", synopsis: "add a progress note, keep the claim", flags: newNoteFlagSet, run: runNote},
	{name: "comment", usage: "comment <id> (-m <text> | -F <file>)", group: "Work", synopsis: "append a comment", flags: newCommentFlagSet, run: runComment},
	{name: "put", usage: "put <id> (-F <file> | -) [options]", group: "Work", synopsis: "write a whole ticket without a checkout", flags: newPutFlagSet, run: runPut},
	{name: "ls", usage: "ls [options]", group: "Inspect", synopsis: "list tickets", flags: newLsFlagSet, run: runLs},
	{name: "next", usage: "next [options]", group: "Work", synopsis: "next suitable ticket", flags: newNextFlagSet, run: runNext},
	{name: "search", usage: "search [options] <query>", group: "Inspect", synopsis: "full-text search", flags: newSearchFlagSet, run: runSearch},
	{name: "validate", usage: "validate [--trailers <range>]", group: "Check", synopsis: "deep checks over store or code range", flags: newValidateFlagSet, run: runValidate},
	{name: "leases", usage: "leases [options]", group: "Inspect", synopsis: "show leases and their status", flags: newLeasesFlagSet, run: runLeases},
	{name: "inbox", usage: "inbox <id>", group: "Inspect", synopsis: "what is new for a ticket", run: runInbox},
	{name: "show", usage: "show <id>", group: "Inspect", synopsis: "display a ticket", run: runShow},
	{name: "force-release", usage: "force-release <id> -m <reason> [options]", group: "Work", synopsis: "admin release with a reason", flags: newForceReleaseFlagSet, run: runForceRelease},
	{name: "lint", usage: "lint", group: "Check", synopsis: "check tickets", run: runLint},
	{name: "doctor", usage: "doctor", group: "Check", synopsis: "check the local setup", run: runDoctor},
}

// rootCmds are the commands that work without a store behind them.
var rootCmds = []command{
	{name: "version", usage: "version", group: "Check", synopsis: "print the tool version", run: runVersion},
	{name: "init", usage: "init [--branch <name>] [dir-or-url] [prefix]", group: "Setup", synopsis: "set up a ticket store in a directory or at a URL", flags: newInitFlagSet, run: runInit},
	{name: "use", usage: "use [--store <remote>#<branch>] [prefix]", group: "Setup", synopsis: "bind store and project prefix for this directory", run: runUse},
	{name: "setup-agent", usage: "setup-agent", group: "Setup", synopsis: "point AGENTS.md at this store", run: runSetupAgent},
	{name: "guide", usage: "guide", group: "Setup", synopsis: "print the agent guide", run: runGuide},
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
	opts.Probe = e.probe
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
		options = append(options, args[i]) // #nosec G602 -- the bounds check immediately above covers i+1
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

func stringFlag(fs *flag.FlagSet, name string) string {
	return fs.Lookup(name).Value.(flag.Getter).Get().(string)
}

func textFlag(fs *flag.FlagSet) string { return stringFlag(fs, "message") }

func stringsFlag(fs *flag.FlagSet, name string) []string {
	return fs.Lookup(name).Value.(flag.Getter).Get().([]string)
}

func boolFlag(fs *flag.FlagSet, name string) bool {
	return fs.Lookup(name).Value.(flag.Getter).Get().(bool)
}

func intFlag(fs *flag.FlagSet, name string) int {
	return fs.Lookup(name).Value.(flag.Getter).Get().(int)
}

func optionValueRequired(fs *flag.FlagSet, arg string) bool {
	name := arg
	if eq := strings.IndexByte(name, '='); eq >= 0 {
		return false
	}
	registered := fs.Lookup(strings.TrimLeft(name, "-"))
	if registered == nil {
		return false
	}
	if boolean, ok := registered.Value.(interface{ IsBoolFlag() bool }); ok && boolean.IsBoolFlag() {
		return false
	}
	return true
}

func newAddFlagSet() *flag.FlagSet {
	fs := newFlagSet("add")
	var message string
	fs.StringVar(&message, "m", "", "ticket title")
	fs.StringVar(&message, "message", "", "ticket title")
	fs.String("d", "", "description ('-' for stdin)")
	fs.String("F", "", "read the description from a file")
	fs.String("l", "", "comma-separated labels")
	fs.String("depends", "", "comma-separated ticket ids")
	fs.String("parent", "", "parent ticket id")
	fs.String("prefix", "", "project prefix (default: DK_PROJECT, bindings, or the store's only registered prefix)")
	fs.Bool("unclaimed", false, "create the ticket without claiming it")
	fs.String("size", "M", "claim size S, M or L")
	fs.String("kind", "agent", "creator kind agent or human")
	fs.String("priority", "", "priority from the store's configured values (default from dricket.toml)")
	fs.Var(&repeatable{}, "set", "extension field x-<name>=value (repeatable)")
	return fs
}

func runAdd(e *env, args []string) (any, *cliError) {
	fs := newAddFlagSet()
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	if fs.NArg() != 0 {
		return nil, usageError("add: no positional arguments expected")
	}
	description, derr := readText(stringFlag(fs, "d"), stringFlag(fs, "F"))
	if derr != nil {
		return nil, derr
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	// Project prefix: flag, then DK_PROJECT, then bindings, then the
	// store's single registered prefix.
	prefixValue, prefixSrc, perr := e.prefixForAdd(context.Background(), stringFlag(fs, "prefix"), s)
	if perr != nil {
		return nil, perr
	}
	if prefixValue == "" {
		return nil, usageError("add: " + noPrefixHint)
	}
	if !e.json {
		e.msg("project: %s (%s)\n", prefixValue, prefixSrc)
	}
	extensions, perr2 := parsePairs("add", stringsFlag(fs, "set"))
	if perr2 != nil {
		return nil, perr2
	}
	id, err := s.Add(context.Background(), service.AddRequest{
		Extensions:  extensions,
		Prefix:      prefixValue,
		Title:       textFlag(fs),
		Description: description,
		Labels:      splitComma(stringFlag(fs, "l")),
		Depends:     splitComma(stringFlag(fs, "depends")),
		Parent:      stringFlag(fs, "parent"),
		Unclaimed:   boolFlag(fs, "unclaimed"),
		Size:        ticket.Size(strings.ToUpper(stringFlag(fs, "size"))),
		Kind:        ticket.Kind(stringFlag(fs, "kind")),
		Priority:    stringFlag(fs, "priority"),
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

func newClaimFlagSet() *flag.FlagSet {
	fs := newFlagSet("claim")
	fs.String("size", "M", "lease size S, M or L")
	fs.String("kind", "agent", "actor kind agent or human")
	return fs
}

func runClaim(e *env, args []string) (any, *cliError) {
	fs := newClaimFlagSet()
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
	res, err := s.Claim(context.Background(), fs.Arg(0), ticket.Size(strings.ToUpper(stringFlag(fs, "size"))), ticket.Kind(stringFlag(fs, "kind")))
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

func newReleaseFlagSet() *flag.FlagSet {
	fs := newFlagSet("release")
	fs.String("stage", "", "target stage (default review with the built-in stages; required with [stages])")
	var message string
	fs.StringVar(&message, "m", "", "result note")
	fs.StringVar(&message, "message", "", "result note")
	return fs
}

func runRelease(e *env, args []string) (any, *cliError) {
	fs := newReleaseFlagSet()
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
	res, err := s.Release(context.Background(), fs.Arg(0), ticket.Stage(stringFlag(fs, "stage")), textFlag(fs))
	if err != nil {
		return nil, wrapErr(err)
	}
	if !e.json {
		e.outf("released %s -> %s\n", res.ID, res.Stage)
	}
	return res, nil
}

// ------------------------------------------------------------- comment --

func newCommentFlagSet() *flag.FlagSet {
	fs := newFlagSet("comment")
	var message string
	fs.StringVar(&message, "m", "", "comment text")
	fs.StringVar(&message, "message", "", "comment text")
	fs.String("F", "", "read the comment from a file")
	return fs
}

func runComment(e *env, args []string) (any, *cliError) {
	fs := newCommentFlagSet()
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	if fs.NArg() != 1 || (textFlag(fs) == "" && stringFlag(fs, "F") == "") {
		return nil, usageError("comment: a ticket id and -m/--message or -F file are required")
	}
	id := fs.Arg(0)
	text := textFlag(fs)
	if file := stringFlag(fs, "F"); file != "" {
		b, err := os.ReadFile(file) // #nosec G304 -- the -F flag reads exactly the named file
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

// ------------------------------------------------------------------ ls --

func newLsFlagSet() *flag.FlagSet {
	fs := newFlagSet("ls")
	fs.String("s", "", "filter by stage")
	fs.String("o", "", "filter by owner (@me)")
	fs.String("p", "", "filter by project prefix")
	return fs
}

func runLs(e *env, args []string) (any, *cliError) {
	fs := newLsFlagSet()
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, cerr
	}
	items, warnings, err := s.List(context.Background(), service.ListFilter{
		Stage: stringFlag(fs, "s"), Owner: stringFlag(fs, "o"), Prefix: stringFlag(fs, "p"),
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
