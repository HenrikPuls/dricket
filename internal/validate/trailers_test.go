// SPDX-License-Identifier: GPL-3.0-or-later
package validate

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/HenrikPuls/dricket/internal/frontmatter"
	"github.com/HenrikPuls/dricket/internal/service"
	"github.com/HenrikPuls/dricket/internal/ticket"
)

// setupCodeRepo creates a git repository with commits that carry dricket
// trailers as configured per commit.
func setupCodeRepo(t *testing.T, commits []map[string]string) (dir string, base string) {
	t.Helper()
	dir = t.TempDir()
	runGit(t, dir, "init", "--quiet", "-b", "main")
	runGit(t, dir, "-c", "user.name=coder", "-c", "user.email=coder@invalid",
		"commit", "--quiet", "--allow-empty", "-m", "base")
	base = strings.TrimSpace(runGit(t, dir, "rev-parse", "HEAD"))
	for i, trailers := range commits {
		msg := fmt.Sprintf("work %d", i+1)
		args := []string{"-c", "user.name=coder", "-c", "user.email=coder@invalid",
			"commit", "--quiet", "--allow-empty", "-m", msg}
		var block []string
		for k, v := range trailers {
			if v == "" {
				continue
			}
			block = append(block, fmt.Sprintf("%s: %s", k, v))
		}
		if len(block) > 0 {
			// One -m keeps the trailers a single paragraph, which is how
			// the wrapper writes them.
			args = append(args, "-m", strings.Join(block, "\n"))
		}
		runGit(t, dir, args...)
	}
	return dir, base
}

func ticketReader(t *testing.T, bare string) func(id string) (ticket.Ticket, error) {
	t.Helper()
	svc := openSvc(t, bare, "reader")
	ctx := context.Background()
	if err := svc.Store.Refresh(ctx); err != nil {
		t.Fatalf("refresh: %v", err)
	}
	return func(id string) (ticket.Ticket, error) {
		path, err := service.PathFor(id)
		if err != nil {
			return ticket.Ticket{}, err
		}
		content, err := svc.Store.ReadFile(ctx, path)
		if err != nil {
			return ticket.Ticket{}, fmt.Errorf("not found")
		}
		doc, perr := frontmatter.Parse(content)
		if perr != nil {
			return ticket.Ticket{}, perr
		}
		tk, _, terr := ticket.FromDoc(doc)
		if terr != nil {
			return ticket.Ticket{}, terr
		}
		return tk, nil
	}
}

func TestTrailersClean(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := openSvc(t, bare, "alice")
	id, err := alice.Add(ctx, service.AddRequest{Prefix: "app", Title: "gate me"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}

	repo, base := setupCodeRepo(t, []map[string]string{
		{"Dricket-Ticket": id, "Dricket-Epoch": "1", "Dricket-Op": "work", "Dricket-Actor": "alice"},
		{"Dricket-Ticket": id, "Dricket-Epoch": "1", "Dricket-Op": "release", "Dricket-Actor": "alice"},
	})
	findings, err := Trailers(ctx, ticketReader(t, bare), repo, base+"..HEAD")
	if err != nil {
		t.Fatalf("trailers: %v", err)
	}
	for _, f := range findings {
		t.Errorf("unexpected finding: %s", f)
	}
}

func TestTrailersDetectsViolations(t *testing.T) {
	bare := setupBare(t)
	ctx := context.Background()
	alice := openSvc(t, bare, "alice")
	id, err := alice.Add(ctx, service.AddRequest{Prefix: "app", Title: "gate me"})
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	// A takeover moves the epoch to 2.
	rewrite(t, bare, id, func(tk *ticket.Ticket) {
		tk.LeaseUntil = time.Now().Add(-2 * time.Hour)
		tk.Rev++
	})
	if _, err := openSvc(t, bare, "bob").Claim(ctx, id, ticket.SizeM, ticket.KindAgent); err != nil {
		t.Fatalf("takeover: %v", err)
	}

	repo, base := setupCodeRepo(t, []map[string]string{
		{"Dricket-Ticket": id, "Dricket-Epoch": "1", "Dricket-Actor": "alice"},   // stale epoch, wrong owner now
		{"Dricket-Ticket": "app-99", "Dricket-Epoch": "1", "Dricket-Actor": "x"}, // unknown ticket
		{}, // no trailer at all
	})
	findings, err := Trailers(ctx, ticketReader(t, bare), repo, base+"..HEAD")
	if err != nil {
		t.Fatalf("trailers: %v", err)
	}
	kinds := map[string]bool{}
	for _, f := range findings {
		kinds[f.Kind] = true
	}
	for _, want := range []string{"stale-epoch", "actor-mismatch", "unknown-ticket", "missing-trailer"} {
		if !kinds[want] {
			t.Errorf("missing finding %q; got %v", want, kinds)
		}
	}
}

func TestParseTrailers(t *testing.T) {
	msg := "Fix the login error\n\nSome body text here.\n\nDricket-Ticket: App-42\nDricket-Epoch: 3\nDricket-Op: work\nDricket-Actor: orch-1.sub-2\n"
	c := parseTrailers(msg)
	if c.Ticket != "app-42" || c.Epoch != 3 || c.Op != "work" || c.Actor != "orch-1.sub-2" {
		t.Fatalf("parsed = %+v", c)
	}
	if c := parseTrailers("no trailers\nat all\n"); c.Ticket != "" {
		t.Fatalf("parsed = %+v, want empty", c)
	}
	// Trailer-like text in the body must not count, only the last block.
	if c := parseTrailers("Dricket-Ticket: app-1\n\nbody\n"); c.Ticket != "" {
		t.Fatalf("body trailer counted: %+v", c)
	}
}
