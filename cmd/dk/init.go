// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lariscus/dricket/internal/store"
	"github.com/lariscus/dricket/internal/ticket"
	agentskills "github.com/lariscus/dricket/skills"
)

// --------------------------------------------------------------- init --

// scaffoldFiles is what `dk init` writes onto the protected main branch.
// The defaults are the concept's starting values; admins tune them by
// pull request. The branch key feeds store discovery; an initial prefix
// makes `add` work without --prefix.
func scaffoldFiles(branch, prefix string) map[string]string {
	prefixesToml := `# The prefix register: prefixes that belong to this store. Admins add
# entries; prefixes are never renamed (docs/CONCEPT.md, "Prefix register").
`
	if prefix != "" {
		prefixesToml += fmt.Sprintf("prefixes = [%q]\n", prefix)
	} else {
		prefixesToml += "prefixes = []\n"
	}
	return map[string]string{
		"dricket.toml": fmt.Sprintf(`# dricket.toml - the protected configuration of this ticket store.
# Changes go through pull requests on this branch (main), never through dk.
# See docs/CONCEPT.md ("Declarative schema") and ADR-0004.

schema_version = 1
branch = %[1]q                  # the ticket branch of this store (discovery)
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

# [transitions.agent] is legacy and left out on purpose: the built-in rules are the
# default, and a table naming the built-in stages would make the file invalid as
# soon as a custom [stages] table is added. Add one only for your own stage names.
#   [stages]
#   values = ["inbox", "build", "done"]
#   terminal = ["done"]
#   claim = ["build"]
`, branch),
		"prefixes.toml": prefixesToml,
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

Tickets in this repository are managed by dricket. Claim a ticket, sync the
checkout, edit the Markdown file and publish it with dk commit. Comment on
other claimed tickets instead of editing their ticket files.

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
// repository in a directory - the ticket branch checked out, the scaffold
// committed on main - or pushes both branches to a remote URL.
func newInitFlagSet() *flag.FlagSet {
	fs := newFlagSet("init")
	fs.String("branch", "tickets", "name of the ticket branch")
	return fs
}

func runInit(e *env, args []string) (any, *cliError) {
	fs := newInitFlagSet()
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	branch := stringFlag(fs, "branch")
	if fs.NArg() > 2 {
		return nil, usageError("init: at most one directory or URL and one prefix expected")
	}
	target := "."
	prefix := ""
	if fs.NArg() >= 1 {
		target = fs.Arg(0)
	}
	if fs.NArg() == 2 {
		prefix = strings.ToLower(strings.TrimSpace(fs.Arg(1)))
		if !ticket.ValidPrefix(prefix) {
			return nil, usageError("init: %q is not a valid prefix (lower case letters, digits, dashes)", fs.Arg(1))
		}
	}
	prof, err := e.identity()
	if err != nil {
		return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
	}
	opts := store.Options{Remote: target, Branch: branch, Profile: prof}
	if err := store.Init(context.Background(), opts, scaffoldFiles(branch, prefix)); err != nil {
		return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
	}
	if !e.json {
		if strings.Contains(target, "://") || strings.HasPrefix(target, "git@") {
			e.outf("scaffolded %s (pushed main and %s)\n", target, branch)
		} else {
			abs, _ := filepath.Abs(target)
			e.outf("initialized ticket store in %s\n", abs)
			e.outf("  checked out branch: %s\n", branch)
			if prefix != "" {
				e.outf("  registered prefix: %s\n", prefix)
			}
			e.outf("  work inside the store: dk add -m \"First ticket\"\n")
			e.outf("  bind it from a code repo: dk use --store %s#%s && dk use %s\n", abs, branch, prefix)
		}
	}
	return map[string]any{"remote": target, "branch": branch, "prefix": prefix}, nil
}

// ---------------------------------------------------------- setup-agent --

// runSetupAgent installs the store pointer and the embedded dricket skill in
// the current project. It preflights both outputs before writing either.
func runSetupAgent(e *env, args []string) (any, *cliError) {
	if len(args) != 0 {
		return nil, usageError("setup-agent: no arguments expected")
	}
	spec, _, cerr := e.storeResolution(context.Background())
	if cerr != nil {
		return nil, cerr
	}
	if spec == "" {
		return nil, usageError("setup-agent: " + noStoreHint)
	}
	remote, branch, _ := strings.Cut(spec, "#")
	if branch == "" {
		branch = "tickets"
	}
	line := fmt.Sprintf("Tickets live in the dricket store %s (branch %s). Run `dk guide` before working on tickets.", remote, branch)

	const file = "AGENTS.md"
	agentsWritten := false
	agentsContent := line + "\n"
	existing, err := os.ReadFile(file) // #nosec G304 -- the command's documented purpose is this file in the cwd
	switch {
	case err == nil:
		if !strings.Contains(string(existing), line) {
			agentsContent = strings.TrimRight(string(existing), "\n") + "\n\n" + line + "\n"
			agentsWritten = true
		}
	case os.IsNotExist(err):
		agentsWritten = true
	default:
		return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
	}
	skillFile := filepath.Join(cwd, ".agents", "skills", "dricket", "SKILL.md")
	skillWritten := false
	skill, err := os.ReadFile(skillFile) // #nosec G304 -- fixed skill destination in the current project
	switch {
	case err == nil && string(skill) != agentskills.Dricket:
		return nil, &cliError{code: exitError, kind: "error", msg: "skill exists with different content: " + skillFile}
	case err == nil:
	case os.IsNotExist(err):
		skillWritten = true
	default:
		return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
	}

	if agentsWritten {
		if err := writeFileAtomic(file, agentsContent); err != nil {
			return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
		}
	}
	if skillWritten {
		if err := writeFileAtomic(skillFile, agentskills.Dricket); err != nil {
			return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
		}
	}
	if !e.json {
		if agentsWritten {
			e.outf("wrote the store pointer into %s\n", file)
		} else {
			e.msg("AGENTS.md already points at this store\n")
		}
		if skillWritten {
			e.outf("wrote dricket skill into %s\n", skillFile)
		} else {
			e.msg("dricket skill already present at %s\n", skillFile)
		}
	}
	return map[string]any{
		"file": file, "written": agentsWritten,
		"skill_file": skillFile, "skill_written": skillWritten,
	}, nil
}

func writeFileAtomic(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".dricket-write-")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() { _ = os.Remove(name) }()
	if _, err := tmp.WriteString(content); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
