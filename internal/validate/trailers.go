// SPDX-License-Identifier: GPL-3.0-or-later

package validate

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/HenrikPuls/dricket/internal/frontmatter"
	"github.com/HenrikPuls/dricket/internal/service"
	"github.com/HenrikPuls/dricket/internal/ticket"
	"github.com/HenrikPuls/dricket/internal/trace"
)

// The wrapper writes the trailers Dricket-Ticket, Dricket-Epoch,
// Dricket-Op and Dricket-Actor into code commits (docs/CONCEPT.md,
// "Epoch in code"). Writing them is the wrapper's job, outside the core;
// the check below is the gate a CI can run on a pull request range.
// Without a host-side gate it stays advisory.

// TrailerFinding is one violation in a checked commit.
type TrailerFinding struct {
	Commit  string `json:"commit"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
}

func (f TrailerFinding) String() string {
	return fmt.Sprintf("%s: %s: %s", f.Commit, f.Kind, f.Message)
}

// TrailerCheck has the epoch and actor a commit claims.
type TrailerCheck struct {
	Ticket string
	Epoch  int
	Op     string
	Actor  string
}

var trailerLine = regexp.MustCompile(`^Dricket-(Ticket|Epoch|Op|Actor):\s*(.*)$`)

// Trailers checks every commit of a git range in a code repository for
// the dricket trailers and compares them with the ticket store:
// the ticket must exist, the epoch must not be older than the ticket's
// current epoch (stale work after a takeover), and while the ticket is
// claimed the actor must be its owner. Every commit in the range is
// expected to belong to a ticket; branch ranges created by the wrapper
// satisfy that.
func Trailers(ctx context.Context, readTicket func(id string) (ticket.Ticket, error), repoDir, gitRange string) ([]TrailerFinding, error) {
	log, err := gitLog(ctx, repoDir, gitRange)
	if err != nil {
		return nil, err
	}
	var findings []TrailerFinding
	for _, rec := range log {
		check := parseTrailers(rec.message)
		commit := rec.commit
		switch check.Ticket {
		case "":
			findings = append(findings, TrailerFinding{Commit: commit, Kind: "missing-trailer",
				Message: "no Dricket-Ticket trailer; every commit in the range must carry one"})
			continue
		default:
		}
		tk, err := readTicket(check.Ticket)
		if err != nil {
			findings = append(findings, TrailerFinding{Commit: commit, Kind: "unknown-ticket",
				Message: fmt.Sprintf("ticket %s: %v", check.Ticket, err)})
			continue
		}
		if check.Epoch < tk.Epoch {
			findings = append(findings, TrailerFinding{Commit: commit, Kind: "stale-epoch",
				Message: fmt.Sprintf("commit epoch %d, ticket %s is at epoch %d (claim was taken over)",
					check.Epoch, check.Ticket, tk.Epoch)})
		}
		if tk.Owner != "" && check.Actor != "" && tk.Owner != check.Actor {
			findings = append(findings, TrailerFinding{Commit: commit, Kind: "actor-mismatch",
				Message: fmt.Sprintf("commit actor %s, ticket %s is claimed by %s",
					check.Actor, check.Ticket, tk.Owner)})
		}
	}
	return findings, nil
}

// parseTrailers reads the Dricket-* trailer lines from the trailing block
// of non-empty lines of a commit message.
func parseTrailers(message string) TrailerCheck {
	var c TrailerCheck
	lines := strings.Split(strings.TrimRight(message, "\r\n"), "\n")
	start := len(lines)
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			break
		}
		start = i
	}
	for _, l := range lines[start:] {
		m := trailerLine.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		switch m[1] {
		case "Ticket":
			c.Ticket = strings.ToLower(strings.TrimSpace(m[2]))
		case "Epoch":
			c.Epoch, _ = strconv.Atoi(strings.TrimSpace(m[2]))
		case "Op":
			c.Op = strings.TrimSpace(m[2])
		case "Actor":
			c.Actor = strings.TrimSpace(m[2])
		}
	}
	return c
}

// logRecord is one commit of the range.
type logRecord struct {
	commit  string
	message string
}

// gitLog lists commit/message pairs of a range in a repository, oldest
// first. Arguments are passed as a list; no shell is involved.
func gitLog(ctx context.Context, dir, gitRange string) ([]logRecord, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", dir, // #nosec G204 -- tool-built arguments; the range comes from the user's own invocation
		"log", "--reverse", "--format=%H%x00%B%x00", gitRange)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := trace.Run(cmd); err != nil {
		return nil, fmt.Errorf("git log %s: %s", gitRange, firstLineOf(errb.String()))
	}
	var records []logRecord
	parts := strings.Split(out.String(), "\x00")
	for i := 0; i+1 < len(parts); i += 2 {
		commit := strings.TrimSpace(parts[i])
		if commit == "" {
			continue
		}
		records = append(records, logRecord{commit: commit, message: parts[i+1]})
	}
	return records, nil
}

func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// ReadTicketFromStore builds the ticket lookup the trailer check needs
// against a refreshed store.
func ReadTicketFromStore(readFile func(path string) (string, error)) func(id string) (ticket.Ticket, error) {
	return func(id string) (ticket.Ticket, error) {
		path, err := service.PathFor(id)
		if err != nil {
			return ticket.Ticket{}, err
		}
		content, err := readFile(path)
		if err != nil {
			return ticket.Ticket{}, fmt.Errorf("not found")
		}
		doc, err := frontmatter.Parse(content)
		if err != nil {
			return ticket.Ticket{}, err
		}
		tk, _, err := ticket.FromDoc(doc)
		if err != nil {
			return ticket.Ticket{}, err
		}
		return tk, nil
	}
}
