// SPDX-License-Identifier: GPL-3.0-or-later

// Command dricket is the Git-native ticketing CLI for humans and AI agents
// (alias dk). This file is the CLI skeleton: global flags, command
// registry, exit-code mapping and the --json envelope. Commands are added
// in later roadmap items; stdout is data, stderr is messages.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/HenrikPuls/dricket/internal/service"
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
	quiet    bool
	noColor  bool
	assume   bool // -y
	dryRun   bool // -n
	store    string
	profile  string
	svcCache *service.Service

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
	synopsis string
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
	e := &env{stdout: stdout, stderr: stderr}
	rest, data, err := parseGlobals(e, args)
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
	switch name {
	case "-h", "--help", "help":
		_, _ = io.WriteString(e.stdout, usageText())
		return exitOK
	}
	for _, c := range registry {
		if c.name == name {
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

// parseGlobals extracts global flags, which may appear before the command.
// A non-nil result means the flag set already produced the output
// (--version) and the caller exits successfully.
func parseGlobals(e *env, args []string) ([]string, any, *cliError) {
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--json":
			e.json = true
		case a == "-q" || a == "--quiet":
			e.quiet = true
		case a == "--no-color":
			e.noColor = true
		case a == "-y" || a == "--assume-yes":
			e.assume = true
		case a == "-n" || a == "--dry-run":
			e.dryRun = true
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
	b.WriteString("Usage: dricket [global flags] <command> [flags] [args]\n\n")
	b.WriteString("Command flags come before positional arguments.\n")
	b.WriteString("Global flags:\n")
	b.WriteString("  --json            data output as JSON with schema_version\n")
	b.WriteString("  -q, --quiet       suppress non-essential messages\n")
	b.WriteString("  --no-color        never colorise output (NO_COLOR is honoured)\n")
	b.WriteString("  -y, --assume-yes  answer prompts with yes\n")
	b.WriteString("  -n, --dry-run     show what would be done\n")
	b.WriteString("  --store <path>    ticket store (remote, branch, path)\n")
	b.WriteString("  --profile <name>  identity profile\n")
	b.WriteString("  --version         print the version and exit\n")
	b.WriteString("  -h, --help        show this help\n\n")
	b.WriteString("Commands:\n")
	for _, c := range registry {
		fmt.Fprintf(&b, "  %-17s %s\n", c.name, c.synopsis)
	}
	b.WriteString("\nThe command set grows with the roadmap; see docs/CONCEPT.md.\n")
	return b.String()
}
