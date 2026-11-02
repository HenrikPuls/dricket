// SPDX-License-Identifier: GPL-3.0-or-later

// Package validate implements the second validation level of
// docs/CONCEPT.md: everything lint checks on single files, plus reference
// and cycle checks over the whole store, plus a replay of the complete
// history that proves every state change was a legal transition and
// comments stayed add-only. Read only; meant for hooks, CI and scheduled
// jobs. Lint prevents accidents per file, validate detects intent.
package validate

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/HenrikPuls/dricket/internal/config"
	"github.com/HenrikPuls/dricket/internal/frontmatter"
	"github.com/HenrikPuls/dricket/internal/service"
	"github.com/HenrikPuls/dricket/internal/store"
	"github.com/HenrikPuls/dricket/internal/ticket"
)

// Finding is one validation result; warnings do not fail a run.
type Finding struct {
	Ticket  string `json:"ticket,omitempty"`
	Kind    string `json:"kind"`
	Message string `json:"message"`
	Warning bool   `json:"warning,omitempty"`
}

func (f Finding) String() string {
	if f.Ticket != "" {
		return fmt.Sprintf("%s: %s: %s", f.Ticket, f.Kind, f.Message)
	}
	return fmt.Sprintf("%s: %s", f.Kind, f.Message)
}

// HasErrors reports whether any finding is error level.
func HasErrors(fs []Finding) bool {
	for _, f := range fs {
		if !f.Warning {
			return true
		}
	}
	return false
}

type report struct {
	findings []Finding
}

func (r *report) add(id, kind, format string, a ...any) {
	r.findings = append(r.findings, Finding{Ticket: id, Kind: kind, Message: fmt.Sprintf(format, a...)})
}

func (r *report) warn(id, kind, format string, a ...any) {
	r.findings = append(r.findings, Finding{Ticket: id, Kind: kind, Message: fmt.Sprintf(format, a...), Warning: true})
}

// Store validates the whole ticket branch: the current state (parse,
// lint, references, cycles, comment shape, lease bounds, secret scan) and
// the replayed history (identity immutable, rev +1 per write, epoch never
// decreasing, legal ownership and stage transitions per role, comments
// add-only, nothing deleted). The replay judges by the timestamps the
// tickets carry, not by wall-clock time. The dricket.toml from main
// provides the limits and the transition table; a broken config is
// reported as a finding and the defaults apply so the rest still runs.
func Store(ctx context.Context, st *store.Store) ([]Finding, error) {
	if err := st.Refresh(ctx); err != nil {
		return nil, fmt.Errorf("refresh: %w", err)
	}
	r := &report{}

	cfg := loadConfig(ctx, st, r)

	tickets, stages := checkCurrentState(ctx, st, r)
	checkReferences(tickets, stages, r)
	checkLeaseBounds(tickets, cfg, r)
	checkPriorities(tickets, cfg, r)
	checkPrefixRegister(ctx, st, stages, r)
	if err := checkHistory(ctx, st, cfg, r); err != nil {
		return nil, err
	}
	sort.Slice(r.findings, func(i, j int) bool {
		if r.findings[i].Ticket != r.findings[j].Ticket {
			return r.findings[i].Ticket < r.findings[j].Ticket
		}
		return r.findings[i].Kind < r.findings[j].Kind
	})
	return r.findings, nil
}

// loadConfig reads dricket.toml from main and reports problems as
// findings; the defaults keep the rest of the run working.
func loadConfig(ctx context.Context, st *store.Store, r *report) *config.Config {
	data, err := st.ConfigFile(ctx, "dricket.toml")
	if err != nil {
		r.add("", "config-unreadable", "%v", err)
		return config.Defaults()
	}
	if len(data) == 0 {
		return config.Defaults()
	}
	cfg, perr := config.Parse(data)
	if perr != nil {
		r.add("", "config-invalid", "%v (writes through the tool fail until this is fixed)", perr)
		return config.Defaults()
	}
	return cfg
}

// checkPriorities validates the free `priority` field against the
// configured values: an unset priority is a warning, an unknown value is
// an error.
func checkPriorities(tickets map[string]ticket.Ticket, cfg *config.Config, r *report) {
	for _, tk := range tickets {
		switch tk.Priority {
		case "":
			r.warn(tk.ID, "priority-unset", "no priority set (default %s)", cfg.PriorityDefault)
		default:
			if err := cfg.CheckPriority(tk.Priority); err != nil {
				r.add(tk.ID, "priority-invalid", "%v", err)
			}
		}
	}
}

// checkPrefixRegister warns about ticket prefixes that are not in
// prefixes.toml on main. The register is governance: a missing entry is
// a warning, not an error, and without a register file nothing is checked.
func checkPrefixRegister(ctx context.Context, st *store.Store, stages map[string]ticket.Stage, r *report) {
	data, err := st.ConfigFile(ctx, "prefixes.toml")
	if err != nil || len(data) == 0 {
		return // no register: nothing to check
	}
	reg, perr := config.ParsePrefixes(data)
	if perr != nil {
		r.warn("", "prefixes-invalid", "prefixes.toml: %v", perr)
		return
	}
	if len(reg) == 0 {
		return
	}
	registered := map[string]bool{}
	for _, p := range reg {
		registered[p] = true
	}
	for id := range stages {
		prefix := id
		if i := strings.LastIndexByte(id, '-'); i > 0 {
			prefix = id[:i]
		}
		if !registered[prefix] {
			r.warn(id, "prefix-not-registered", "prefix %q is not in prefixes.toml", prefix)
		}
	}
}

// checkLeaseBounds enforces the lease_max bound from the config: the
// granted lease (lease_until minus claimed_at) must not exceed it. This
// is the check deferred from R-3 until the config existed.
func checkLeaseBounds(tickets map[string]ticket.Ticket, cfg *config.Config, r *report) {
	if cfg.LeaseMax <= 0 {
		return
	}
	for _, tk := range tickets {
		if tk.Owner == "" || tk.ClaimedAt.IsZero() || tk.LeaseUntil.IsZero() {
			continue
		}
		if granted := tk.LeaseUntil.Sub(tk.ClaimedAt); granted > cfg.LeaseMax {
			r.add(tk.ID, "lease-too-long",
				"granted lease %s exceeds limits.lease_max %s", granted, cfg.LeaseMax)
		}
	}
}

// checkCurrentState parses every file under tickets/ and lints it.
// It returns the parsed tickets by path and their stages by id.
func checkCurrentState(ctx context.Context, st *store.Store, r *report) (map[string]ticket.Ticket, map[string]ticket.Stage) {
	tickets := map[string]ticket.Ticket{}
	stages := map[string]ticket.Stage{}
	paths, err := st.List(ctx, "tickets/")
	if err != nil {
		r.add("", "read-error", "%v", err)
		return tickets, stages
	}
	for _, p := range paths {
		id := dirID(p)
		switch {
		case strings.HasSuffix(p, "/ticket.md"):
			content, err := st.ReadFile(ctx, p)
			if err != nil {
				r.add(id, "read-error", "%v", err)
				continue
			}
			doc, err := frontmatter.Parse(content)
			if err != nil {
				r.add(id, "unparseable", "%v", err)
				continue
			}
			tk, unknown, err := ticket.FromDoc(doc)
			if err != nil {
				r.add(id, "unusable", "%v", err)
				continue
			}
			if tk.ID != id {
				r.add(id, "id-path-mismatch", "ticket says id %q", tk.ID)
			}
			for _, u := range unknown {
				r.warn(id, "unknown-key", "%q (warning, newer tool?)", u)
			}
			for _, lf := range ticket.Lint(tk) {
				if lf.Warning {
					r.warn(id, "lint", "%s", lf.String())
				} else {
					r.add(id, "lint", "%s", lf.String())
				}
			}
			tickets[p] = tk
			stages[tk.ID] = tk.Stage
		case strings.HasSuffix(p, ".md") && strings.Contains(p, "/comments/"):
			checkCommentFile(ctx, st, p, id, r)
		default:
			r.add("", "unexpected-path", "%s is neither ticket.md nor a comment", p)
		}
	}
	return tickets, stages
}

// checkCommentFile checks the shape of one comment file.
func checkCommentFile(ctx context.Context, st *store.Store, p, ticketID string, r *report) {
	base := p[strings.LastIndexByte(p, '/')+1 : len(p)-3] // 0001
	content, err := st.ReadFile(ctx, p)
	if err != nil {
		r.add(ticketID, "read-error", "%v", err)
		return
	}
	doc, err := frontmatter.Parse(content)
	if err != nil {
		r.add(ticketID, "unparseable", "comment %s: %v", base, err)
		return
	}
	if v, ok := doc.Get("number"); !ok || v != base {
		r.add(ticketID, "comment-number-mismatch", "comment %s says number %q", base, v)
	}
	if v, ok := doc.Get("ticket"); !ok || v != ticketID {
		r.add(ticketID, "comment-ticket-mismatch", "comment %s says ticket %q", base, v)
	}
	if v, _ := doc.Get("author"); v == "" {
		r.add(ticketID, "comment-author-missing", "comment %s has no author", base)
	}
}

// checkReferences verifies depends and parent and finds cycles.
func checkReferences(tickets map[string]ticket.Ticket, stages map[string]ticket.Stage, r *report) {
	for path, tk := range tickets {
		_ = path
		for _, dep := range tk.Depends {
			if _, ok := stages[dep]; !ok {
				r.add(tk.ID, "dangling-depends", "%q does not exist", dep)
			}
		}
		if tk.Parent != "" {
			if _, ok := stages[tk.Parent]; !ok {
				r.add(tk.ID, "dangling-parent", "%q does not exist", tk.Parent)
			}
			if tk.Parent == tk.ID {
				r.add(tk.ID, "parent-self", "ticket is its own parent")
			}
		}
	}
	// Cycles in depends (parent is a single link; a cycle there is also a
	// depends-style loop once combined, the walk below covers both).
	byID := map[string]ticket.Ticket{}
	for _, tk := range tickets {
		byID[tk.ID] = tk
	}
	const visiting = 1
	const done = 2
	state := map[string]int{}
	var cycleAt func(id string, chain []string) bool
	cycleAt = func(id string, chain []string) bool {
		switch state[id] {
		case done:
			return false
		case visiting:
			r.add(id, "dependency-cycle", "via %s", strings.Join(chain, " -> "))
			return true
		}
		state[id] = visiting
		tk, ok := byID[id]
		if !ok {
			state[id] = done
			return false
		}
		next := append([]string{}, tk.Depends...)
		if tk.Parent != "" {
			next = append(next, tk.Parent)
		}
		for _, n := range next {
			if cycleAt(n, append(chain, n)) {
				state[id] = done
				return true
			}
		}
		state[id] = done
		return false
	}
	ids := make([]string, 0, len(byID))
	for id := range byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		cycleAt(id, []string{id})
	}
}

// checkHistory replays every commit that touched the ticket tree.
func checkHistory(ctx context.Context, st *store.Store, cfg *config.Config, r *report) error {
	commits, err := st.Commits(ctx)
	if err != nil {
		return fmt.Errorf("commits: %w", err)
	}
	for _, c := range commits {
		changes, err := st.CommitChanges(ctx, c)
		if err != nil {
			return fmt.Errorf("changes of %s: %w", short(c), err)
		}
		if len(changes) == 0 {
			continue
		}
		touched := map[string]bool{}
		for _, ch := range changes {
			if strings.HasPrefix(ch.Path, "tickets/") {
				d := ticketDir(ch.Path)
				if d != "" {
					touched[d] = true
				}
			}
		}
		if len(touched) > 1 {
			r.add("", "multi-ticket-commit", "%s touches %d tickets; the tool writes one ticket per commit", short(c), len(touched))
		}
		for _, ch := range changes {
			checkHistoryChange(ctx, st, c, ch, cfg, r)
		}
	}
	return nil
}

func checkHistoryChange(ctx context.Context, st *store.Store, commit string, ch store.FileChange, cfg *config.Config, r *report) {
	p := ch.Path
	shortCommit := short(commit)
	if !strings.HasPrefix(p, "tickets/") {
		r.add("", "non-ticket-change", "%s changed %s outside the ticket tree", shortCommit, p)
		return
	}
	id := dirID(p)
	switch {
	case strings.HasSuffix(p, "/ticket.md"):
		switch ch.Status {
		case 'D':
			r.add(id, "ticket-deleted", "%s deleted the ticket file", shortCommit)
		case 'A':
			content, err := st.ShowRev(ctx, commit, p)
			if err != nil {
				r.add(id, "read-error", "creation at %s: %v", shortCommit, err)
				return
			}
			checkCreation(id, content, r)
		default:
			checkTransition(ctx, st, commit, p, id, cfg, r)
		}
	case strings.HasSuffix(p, ".md") && strings.Contains(p, "/comments/"):
		switch ch.Status {
		case 'A':
			// Shape is checked on the current state; creation needs no
			// extra rule beyond being a new file.
		case 'D':
			r.add(id, "comment-deleted", "%s deleted %s", shortCommit, p)
		default:
			r.add(id, "comment-modified", "%s rewrote %s; comments are add-only", shortCommit, p)
		}
	default:
		if ch.Status == 'D' {
			r.add("", "file-deleted", "%s deleted %s", shortCommit, p)
		} else {
			r.add("", "unexpected-path", "%s wrote %s", shortCommit, p)
		}
	}
}

// checkCreation validates the first snapshot of a ticket: either free
// (epoch 0, rev 0) or created with a claim (epoch 1, rev 1).
func checkCreation(id, content string, r *report) {
	doc, err := frontmatter.Parse(content)
	if err != nil {
		r.add(id, "unparseable", "creation: %v", err)
		return
	}
	tk, _, err := ticket.FromDoc(doc)
	if err != nil {
		r.add(id, "unusable", "creation: %v", err)
		return
	}
	if tk.ID != id {
		r.add(id, "id-path-mismatch", "creation says id %q", tk.ID)
	}
	free := tk.Epoch == 0 && tk.Rev == 0 && tk.Owner == ""
	claimed := tk.Epoch == 1 && tk.Rev == 1 && tk.Owner != ""
	if !free && !claimed {
		r.add(id, "bad-creation", "epoch %d, rev %d, owner %q (free or freshly claimed expected)",
			tk.Epoch, tk.Rev, tk.Owner)
	}
}

// checkTransition replays one change to a ticket file against its parent.
func checkTransition(ctx context.Context, st *store.Store, commit, path, id string, cfg *config.Config, r *report) {
	cur, err := st.ShowRev(ctx, commit, path)
	if err != nil {
		r.add(id, "read-error", "%s: %v", short(commit), err)
		return
	}
	prev, err := st.ShowRev(ctx, commit+"^", path)
	if err != nil {
		// No parent version: the file is new in this commit, which the
		// name-status would have reported as A; treat as unusable.
		r.add(id, "read-error", "no parent version at %s", short(commit))
		return
	}
	curDoc, err := frontmatter.Parse(cur)
	if err != nil {
		r.add(id, "unparseable", "%s: %v", short(commit), err)
		return
	}
	prevDoc, err := frontmatter.Parse(prev)
	if err != nil {
		r.add(id, "unparseable", "parent of %s: %v", short(commit), err)
		return
	}
	curTk, _, err := ticket.FromDoc(curDoc)
	if err != nil {
		r.add(id, "unusable", "%s: %v", short(commit), err)
		return
	}
	prevTk, _, err := ticket.FromDoc(prevDoc)
	if err != nil {
		r.add(id, "unusable", "parent of %s: %v", short(commit), err)
		return
	}

	// Identity never changes.
	if curTk.ID != prevTk.ID || !curTk.CreatedAt.Equal(prevTk.CreatedAt) ||
		curTk.CreatedBy != prevTk.CreatedBy || curTk.SchemaVersion != prevTk.SchemaVersion {
		r.add(id, "identity-changed", "%s rewrote identity fields", short(commit))
	}
	// rev rises by exactly one per write.
	if curTk.Rev != prevTk.Rev+1 {
		r.add(id, "rev-not-incremented", "%s: rev %d -> %d (expected +1)", short(commit), prevTk.Rev, curTk.Rev)
	}
	if curTk.Epoch < prevTk.Epoch {
		r.add(id, "epoch-decreased", "%s: epoch %d -> %d", short(commit), prevTk.Epoch, curTk.Epoch)
	}
	// Ownership transitions.
	switch {
	case prevTk.Owner == "" && curTk.Owner == "":
		// Free-field edit while unclaimed.
	case prevTk.Owner == "" && curTk.Owner != "":
		if curTk.Epoch != prevTk.Epoch+1 {
			r.add(id, "claim-without-epoch", "%s: claim did not raise the epoch", short(commit))
		}
	case prevTk.Owner != "" && curTk.Owner == "":
		// Release.
	case prevTk.Owner == curTk.Owner:
		if curTk.Epoch != prevTk.Epoch {
			r.add(id, "epoch-changed-on-update", "%s: epoch %d -> %d while the owner stayed",
				short(commit), prevTk.Epoch, curTk.Epoch)
		}
	default:
		// Takeover: only after the previous lease plus grace.
		lu := prevTk.LeaseUntil
		ca := curTk.ClaimedAt
		if lu.IsZero() || ca.IsZero() || lu.Add(service.Grace).After(ca) {
			r.add(id, "takeover-before-expiry", "%s: %s took over from %s before lease %s plus grace",
				short(commit), curTk.Owner, prevTk.Owner, stampOf(lu))
		}
		if curTk.Epoch != prevTk.Epoch+1 {
			r.add(id, "takeover-without-epoch", "%s: takeover did not raise the epoch", short(commit))
		}
	}
	// Stage transitions per role, only when the stage actually moved;
	// note, extend and comment keep the stage. The table comes from
	// dricket.toml (defaults apply without a config).
	if curTk.Stage != prevTk.Stage && !ticket.CanTransitionTable(cfg.Transitions, roleOf(curTk.Kind), prevTk.Stage, curTk.Stage) {
		r.add(id, "illegal-transition", "%s: %s may not move %s from %s to %s",
			short(commit), roleOf(curTk.Kind), id, prevTk.Stage, curTk.Stage)
	}
}

func roleOf(kind ticket.Kind) ticket.Role {
	if kind == ticket.KindHuman {
		return ticket.RoleHuman
	}
	return ticket.RoleAgent
}

func stampOf(t time.Time) string {
	if t.IsZero() {
		return "(unset)"
	}
	return t.UTC().Format(time.RFC3339)
}

// ticketDir returns the ticket directory of a path ("tickets/app/0000/
// app-1/ticket.md" -> "tickets/app/0000/app-1"), or "" for other files.
func ticketDir(p string) string {
	if !strings.HasSuffix(p, "/ticket.md") {
		return ""
	}
	return strings.TrimSuffix(p, "/ticket.md")
}

// dirID extracts the ticket id from a ticket-tree path: the directory
// name for ticket.md, and the ticket directory for comment files.
func dirID(p string) string {
	parts := strings.Split(p, "/")
	if len(parts) >= 2 && parts[len(parts)-1] == "ticket.md" {
		return parts[len(parts)-2]
	}
	// tickets/<prefix>/<shard>/<id>/comments/<n>.md -> <id>
	for i := len(parts) - 1; i > 0; i-- {
		if parts[i] == "comments" {
			return parts[i-1]
		}
	}
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return ""
}

func short(commit string) string {
	if len(commit) > 10 {
		return commit[:10]
	}
	return commit
}
