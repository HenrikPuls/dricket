// SPDX-License-Identifier: GPL-3.0-or-later

// Command dricket is the Git-native ticketing CLI for humans and AI agents
// (alias dk). This file is the CLI skeleton: global flags, command
// registry, exit-code mapping and the --json envelope. Commands are added
// in later roadmap items; stdout is data, stderr is messages.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/HenrikPuls/dricket/internal/service"
	"github.com/HenrikPuls/dricket/internal/store"
	"github.com/HenrikPuls/dricket/internal/trace"
)

// jsonSchemaVersion is the version of the CLI JSON envelope. It is
// independent of the on-disk ticket schema_version.
const jsonSchemaVersion = 1

// Exit codes are a public interface (docs/CONCEPT.md, "Exit codes";
// 17-19 added for stage v2, ADR-0004).
const (
	exitOK            = 0  // ok
	exitError         = 1  // general error
	exitUsage         = 2  // usage error
	exitNotFound      = 10 // not found
	exitClaimed       = 11 // already claimed by someone else
	exitConflict      = 12 // race lost or version conflict (--if-version)
	exitClaimLost     = 13 // claim lost (lease taken over)
	exitInvalid       = 14 // invalid transition or validation error
	exitNetwork       = 15 // network or push failure after all attempts
	exitTooOld        = 16 // tool too old or unknown schema version
	exitForbidden     = 17 // permission denied (e.g. force-release by a non-admin)
	exitQuota         = 18 // quota exceeded (new tickets per owner and period)
	exitSecret        = 19 // refused write: the secret scan found likely credentials
	exitLeasesExpired = 20 // leases --check found expired leases
)

// cliError carries an exit code, a machine-readable kind for --json and a
// human-readable message. The message goes to stderr; with --json the error
// object goes to stdout as data.
type cliError struct {
	code int
	kind string
	msg  string
}

func (e *cliError) Error() string { return e.msg }

func usageError(format string, a ...any) *cliError {
	return &cliError{code: exitUsage, kind: "usage", msg: fmt.Sprintf(format, a...)}
}

// env is the command environment: output writers and global flags.
type env struct {
	stdout   io.Writer
	stderr   io.Writer
	json     bool
	trace    bool
	store    string
	profile  string
	svcCache *service.Service
	probe    *store.Probe // repository facts found by discovery, shared with store.Open

	// Project resolution (git-like walk-up), computed once per process.
	projMu        sync.Mutex
	projDone      bool
	projErr       *cliError
	projSpec      string
	projSpecSrc   string
	projPrefix    string
	projPrefixSrc string
}

// command is one subcommand of the registry.
type command struct {
	name     string
	usage    string
	group    string
	synopsis string
	flags    func() *flag.FlagSet
	run      func(e *env, args []string) (any, *cliError)
}

// registry holds the implemented commands; rootCmds and serviceCmds are
// appended in commands.go. Planned commands from docs/CONCEPT.md are
// added by their roadmap items, not before.
var registry []command

// version is set at build time (GoReleaser ldflags). A `go install
// module/cmd@vX` build instead records the module version in its build
// info; versionData() falls back to it, so released installs report
// their version too.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// versionData returns the version information to report. Release builds
// carry ldflags-injected values; a `go install ...@vX` build carries the
// module version in its build info; a local checkout build stays "dev".
func versionData() map[string]any {
	v := version
	if v == "dev" || v == "" {
		if bi, ok := debug.ReadBuildInfo(); ok &&
			bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			v = bi.Main.Version
		}
	}
	return map[string]any{"version": v, "commit": commit, "date": date}
}

// stdout and stderr are variables so tests can capture the output.
// stdout is data, stderr is messages. Write errors are deliberately
// ignored: a closed pipe ends the output, it is not a tool failure.
var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	start := time.Now()
	e := &env{stdout: stdout, stderr: stderr}
	// The trace report is written to stderr only, after the command ended,
	// so stdout and the exit code are the same with and without it (ADR-0014).
	defer func() {
		if e.trace || trace.EnvEnabled() {
			trace.Write(e.stderr, e.json)
		}
		trace.Disable()
	}()
	if trace.EnvEnabled() {
		trace.Enable(start)
	}
	rest, data, err := parseGlobals(e, args)
	if e.trace && !trace.Enabled() {
		trace.Enable(start)
	}
	if err != nil {
		return e.fail(err)
	}
	if data != nil { // --version handled during flag parsing
		e.printData(data)
		return exitOK
	}
	if len(rest) == 0 {
		_, _ = io.WriteString(e.stderr, usageText())
		return exitUsage
	}
	name, cmdArgs := rest[0], rest[1:]
	if name == "-h" || name == "--help" {
		_, _ = io.WriteString(e.stdout, usageText())
		return exitOK
	}
	if name == "help" {
		if len(cmdArgs) == 0 {
			_, _ = io.WriteString(e.stdout, usageText())
			return exitOK
		}
		if len(cmdArgs) != 1 {
			return e.fail(usageError("help: at most one command expected"))
		}
		for _, c := range registry {
			if c.name == cmdArgs[0] {
				_, _ = io.WriteString(e.stdout, commandHelp(c))
				return exitOK
			}
		}
		return e.fail(usageError("unknown help topic %q", cmdArgs[0]))
	}
	for _, c := range registry {
		if c.name == name {
			if helpRequested(c, cmdArgs) {
				_, _ = io.WriteString(e.stdout, commandHelp(c))
				return exitOK
			}
			data, cerr := c.run(e, cmdArgs)
			if cerr != nil {
				return e.fail(cerr)
			}
			if data != nil && e.json {
				e.printData(data)
			}
			return exitOK
		}
	}
	return e.fail(usageError("unknown command %q (see --help)", name))
}

func helpRequested(c command, args []string) bool {
	var fs *flag.FlagSet
	if c.flags != nil {
		fs = c.flags()
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			return false
		}
		if arg == "-h" || arg == "--help" {
			return true
		}
		if fs != nil && optionValueRequired(fs, arg) && i+1 < len(args) {
			i++
		}
	}
	return false
}

// parseGlobals extracts global options without consuming command-option values
// or anything after --. A non-nil result handles --version immediately.
func parseGlobals(e *env, args []string) ([]string, any, *cliError) {
	var rest []string
	var commandFlags *flag.FlagSet
	commandSeen := false
	for i := 0; i < len(args); i++ {
		a := args[i]
		if commandSeen && a == "--" {
			rest = append(rest, args[i:]...)
			break
		}
		if commandSeen && commandFlags != nil && optionValueRequired(commandFlags, a) {
			rest = append(rest, a)
			if i+1 < len(args) {
				i++
				rest = append(rest, args[i])
			}
			continue
		}
		switch {
		case a == "--json":
			e.json = true
		case a == "--trace":
			e.trace = true
		case a == "--store" || a == "--profile":
			if i+1 >= len(args) {
				return nil, nil, usageError("%s requires a value", a)
			}
			i++
			if a == "--store" {
				e.store = args[i]
			} else {
				e.profile = args[i]
			}
		case strings.HasPrefix(a, "--store="):
			e.store = strings.TrimPrefix(a, "--store=")
		case strings.HasPrefix(a, "--profile="):
			e.profile = strings.TrimPrefix(a, "--profile=")
		case a == "--version":
			return nil, versionData(), nil
		default:
			rest = append(rest, a)
			if !commandSeen {
				commandSeen = true
				for _, c := range registry {
					if c.name == a && c.flags != nil {
						commandFlags = c.flags()
						break
					}
				}
			}
		}
	}
	return rest, nil, nil
}

// runVersion implements `dricket version`.
func runVersion(e *env, _ []string) (any, *cliError) {
	data := versionData()
	if !e.json {
		for k, v := range data {
			e.outf("%s: %v\n", k, v)
		}
	}
	return data, nil
}

// outf writes data to stdout; write errors are deliberately ignored (a
// closed pipe ends the output, it is not a tool failure).
func (e *env) outf(format string, a ...any) {
	_, _ = fmt.Fprintf(e.stdout, format, a...)
}

// msg writes a message to stderr.
func (e *env) msg(format string, a ...any) {
	_, _ = fmt.Fprintf(e.stderr, format, a...)
}

// printData writes command data: JSON on stdout with --json, plain
// key/value lines otherwise (commands with their own plain formatting
// return nil data and print themselves).
func (e *env) printData(data any) {
	if e.json {
		out := struct {
			SchemaVersion int `json:"schema_version"`
			Data          any `json:"data"`
		}{SchemaVersion: jsonSchemaVersion, Data: data}
		enc := json.NewEncoder(e.stdout)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(out)
		return
	}
	switch v := data.(type) {
	case map[string]any:
		for k, val := range v {
			_, _ = fmt.Fprintf(e.stdout, "%s: %v\n", k, val)
		}
	default:
		_, _ = fmt.Fprintln(e.stdout, v)
	}
}

// fail reports a cliError: the message goes to stderr; with --json the
// error object additionally goes to stdout as data. Errors are always
// reported; -q only silences non-essential output.
func (e *env) fail(err *cliError) int {
	if err == nil {
		err = &cliError{code: exitError, kind: "error", msg: "unexpected nil error"}
	}
	if e.json {
		out := struct {
			SchemaVersion int `json:"schema_version"`
			Error         any `json:"error"`
		}{
			SchemaVersion: jsonSchemaVersion,
			Error: struct {
				Kind     string `json:"kind"`
				ExitCode int    `json:"exit_code"`
				Message  string `json:"message"`
			}{Kind: err.kind, ExitCode: err.code, Message: err.msg},
		}
		enc := json.NewEncoder(e.stdout)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(out)
	}
	_, _ = fmt.Fprintln(e.stderr, "dricket:", err.msg)
	return err.code
}

// usageText is the help text, one string so tests can match it as a whole.
func usageText() string {
	var b strings.Builder
	fmt.Fprintf(&b, "dricket (%s) - Git-native ticketing for humans and AI agents\n\n", version)
	b.WriteString("Usage: dk [global options] <command> [options] [args]\n\n")
	b.WriteString("Global options:\n")
	b.WriteString("  --json            data output as JSON with schema_version\n")
	b.WriteString("  --store <path>    ticket store (remote, branch, path)\n")
	b.WriteString("  --profile <name>  identity profile\n")
	b.WriteString("  --version         print the version and exit\n")
	b.WriteString("  -h, --help        show this help\n\n")
	for _, group := range []string{"Setup", "Work", "Inspect", "Check"} {
		b.WriteString(group + ":\n")
		for _, c := range registry {
			if c.group == group {
				fmt.Fprintf(&b, "  %-17s %s\n", c.name, c.synopsis)
			}
		}
		b.WriteString("\n")
	}
	b.WriteString("Use dk help <command> for command options.\n")
	return b.String()
}

func commandHelp(c command) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Usage: dk %s\n\n%s\n", c.usage, c.synopsis)
	if c.flags == nil {
		return b.String()
	}
	fs := c.flags()
	b.WriteString("\nOptions:\n")
	fs.VisitAll(func(f *flag.Flag) {
		name := "--" + f.Name
		if len(f.Name) == 1 {
			name = "-" + f.Name
		}
		if boolean, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok || !boolean.IsBoolFlag() {
			name += " <value>"
		}
		fmt.Fprintf(&b, "  %-24s %s", name, f.Usage)
		if f.DefValue != "" && f.DefValue != "false" && f.DefValue != "0" {
			fmt.Fprintf(&b, " (default %s)", f.DefValue)
		}
		b.WriteString("\n")
	})
	return b.String()
}
