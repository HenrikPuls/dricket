// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/HenrikPuls/dricket/internal/store"
)

// --------------------------------------------------------------- init --

// scaffoldFiles is what `dk init` writes onto the protected main branch.
// The defaults are the concept's starting values; admins tune them by
// pull request.
func scaffoldFiles() map[string]string {
	return map[string]string{
		"dricket.toml": `# dricket.toml - the protected configuration of this ticket store.
# Changes go through pull requests on this branch (main), never through dk.
# See docs/CONCEPT.md ("Declarative schema") and ADR-0004.

schema_version = 1
# min_version = "0.2.0"      # refuse older tools with exit 16

[limits]
lease_s = "2h"               # starting leases by size
lease_m = "8h"
lease_l = "24h"
lease_min = "15m"            # clamps every granted lease
lease_max = "72h"            # clamps claims and bounds validate
max_extensions = 3

[security]
admins = []                  # owner ids allowed to force-release; empty = nobody
quota_tickets_per_day = 50   # new tickets per identity and day; 0 disables
secret_scan = true           # refuse likely credentials before they land

[priorities]
values = ["critical", "high", "normal", "low"]
default = "normal"

[transitions.agent]          # agent stage moves; human/admin may move anything
open = ["in-progress", "blocked", "needs-input"]
in-progress = ["review", "blocked", "needs-input", "open"]
blocked = ["in-progress", "open"]
needs-input = ["in-progress", "open"]
review = ["in-progress"]
`,
		"prefixes.toml": `# The prefix register: prefixes that belong to this store. Admins add
# entries; prefixes are never renamed (docs/CONCEPT.md, "Prefix register").
prefixes = []
`,
		".gitattributes": `# Ticket files are plain text and compared byte by byte; normalize
# line endings so the same file hashes on every platform.
* text=auto eol=lf
`,
		"README.md": `# Tickets

This repository holds the tickets. The ticket branch is written only
through dk, the dricket CLI; this branch (main) is protected and holds
the configuration. Read AGENTS.md before working on tickets, then run
dk guide.
`,
		"AGENTS.md": `# Agents

Tickets in this repository are managed by dricket. Never edit ticket
files directly.

Run dk guide and follow it: the workflow, the exit-code rules and the
fencing rules (exit 13 means stop) are printed by the tool itself, so
this file and the tool cannot drift apart.

Set DK_OWNER to a unique id per claim-capable process before claiming.
`,
		"CLAUDE.md": `# Claude Code

Read AGENTS.md first. It points at dk guide, which carries the rules.
`,
	}
}

// runInit implements `dk init [dir-or-url]`: like git init, it sets up a
// repository in a directory - main checked out with the scaffold, the
// ticket branch seeded - or pushes both branches to a remote URL.
func runInit(e *env, args []string) (any, *cliError) {
	fs := newFlagSet("init")
	branch := fs.String("branch", "tickets", "name of the ticket branch")
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	if fs.NArg() > 1 {
		return nil, usageError("init: at most one directory or URL expected")
	}
	target := "."
	if fs.NArg() == 1 {
		target = fs.Arg(0)
	}
	prof, err := e.identity()
	if err != nil {
		return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
	}
	opts := store.Options{Remote: target, Branch: *branch, Profile: prof}
	if err := store.Init(context.Background(), opts, scaffoldFiles()); err != nil {
		return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
	}
	if !e.json {
		if strings.Contains(target, "://") || strings.HasPrefix(target, "git@") {
			e.outf("scaffolded %s (pushed main and %s)\n", target, *branch)
		} else {
			abs, _ := filepath.Abs(target)
			e.outf("initialized ticket store in %s\n", abs)
			e.outf("  main:        checked out, holds dricket.toml (edit by pull request)\n")
			e.outf("  %s:  seeded; point the tool at it:\n", *branch)
			e.outf("  dk --store %s#%s add -c \"First ticket\"\n", abs, *branch)
		}
	}
	return map[string]any{"remote": target, "branch": *branch}, nil
}

// ---------------------------------------------------------- setup-agent --

// runSetupAgent writes one pointer line into the AGENTS.md of the current
// code repository, so agents find the store without duplicated rules.
func runSetupAgent(e *env, args []string) (any, *cliError) {
	if len(args) != 0 {
		return nil, usageError("setup-agent: no arguments expected")
	}
	spec := e.store
	if spec == "" {
		spec = os.Getenv("DK_STORE")
	}
	if spec == "" {
		return nil, usageError("setup-agent: no store configured; use --store <remote>[#branch] or DK_STORE")
	}
	remote, branch, _ := strings.Cut(spec, "#")
	if branch == "" {
		branch = "tickets"
	}
	line := fmt.Sprintf("Tickets live in the dricket store %s (branch %s). Run `dk guide` before working on tickets.", remote, branch)

	const file = "AGENTS.md"
	existing, err := os.ReadFile(file) // #nosec G304 -- the command's documented purpose is this file in the cwd
	switch {
	case err == nil:
		if strings.Contains(string(existing), line) {
			if !e.json {
				e.msg("AGENTS.md already points at this store\n")
			}
			return map[string]any{"file": file, "written": false}, nil
		}
		content := strings.TrimRight(string(existing), "\n") + "\n\n" + line + "\n"
		if werr := os.WriteFile(file, []byte(content), 0o600); werr != nil { // #nosec G703 -- file is the constant AGENTS.md
			return nil, &cliError{code: exitError, kind: "error", msg: werr.Error()}
		}
	case os.IsNotExist(err):
		if werr := os.WriteFile(file, []byte(line+"\n"), 0o600); werr != nil { // #nosec G703 -- file is the constant AGENTS.md
			return nil, &cliError{code: exitError, kind: "error", msg: werr.Error()}
		}
	default:
		return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
	}
	if !e.json {
		e.outf("wrote the store pointer into %s\n", file)
	}
	return map[string]any{"file": file, "written": true}, nil
}
