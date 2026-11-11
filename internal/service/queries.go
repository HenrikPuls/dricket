// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/lariscus/dricket/internal/frontmatter"
	"github.com/lariscus/dricket/internal/store"
	"github.com/lariscus/dricket/internal/ticket"
)

// ListItem is one row of `dk ls`.
type ListItem struct {
	ID       string `json:"id"`
	Stage    string `json:"stage"`
	Owner    string `json:"owner"`
	Title    string `json:"title"`
	Priority string `json:"priority,omitempty"`
	Kind     string `json:"kind,omitempty"`
	Epoch    int    `json:"epoch"`
	Rev      int    `json:"rev"`
	Labels   string `json:"labels,omitempty"`
}

// ListFilter narrows `dk ls`; empty fields mean "no restriction".
type ListFilter struct {
	Stage  string
	Owner  string // "@me" resolves to the service owner
	Prefix string
	Stale  bool // only tickets whose lease expired (dead claims)
}

// List returns tickets from the current head, sorted by id.
func (s *Service) List(ctx context.Context, f ListFilter) ([]ListItem, []string, error) {
	if err := s.Store.Refresh(ctx); err != nil {
		return nil, nil, wrapRead(err)
	}
	files, err := s.Store.ReadAll(ctx, "tickets/", "/ticket.md")
	if err != nil {
		return nil, nil, wrapRead(err)
	}
	owner := f.Owner
	if owner == "@me" {
		owner = s.Owner
	}
	var items []ListItem
	var warnings []string
	for _, file := range files {
		p, content := file.Path, file.Content
		prefix, _, serr := splitID(dirID(p))
		if serr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", p, serr))
			continue
		}
		if f.Prefix != "" && prefix != strings.ToLower(f.Prefix) {
			continue
		}
		doc, perr := frontmatter.Parse(content)
		if perr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", p, perr))
			continue
		}
		tk, unknown, terr := ticket.FromDoc(doc)
		if terr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", p, terr))
			continue
		}
		for _, u := range unknown {
			warnings = append(warnings, fmt.Sprintf("%s: unknown key %q (warning)", p, u))
		}
		if f.Stage != "" && string(tk.Stage) != f.Stage {
			continue
		}
		if owner != "" && tk.Owner != owner {
			continue
		}
		if f.Stale && (tk.Owner == "" || !tk.LeaseUntil.Add(Grace).Before(s.Now())) {
			continue
		}
		items = append(items, ListItem{
			ID:       tk.ID,
			Stage:    string(tk.Stage),
			Owner:    tk.Owner,
			Title:    tk.Title,
			Priority: tk.Priority,
			Kind:     string(tk.Kind),
			Epoch:    tk.Epoch,
			Rev:      tk.Rev,
			Labels:   strings.Join(tk.Labels, ","),
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	return items, warnings, nil
}

// dirID extracts the ticket id (the directory name) from a ticket path.
func dirID(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2]
}

// Show returns the raw ticket file; unknown ids are exit 10.
func (s *Service) Show(ctx context.Context, id string) (string, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	path, err := PathFor(id)
	if err != nil {
		return "", err
	}
	if err := s.Store.Refresh(ctx); err != nil {
		return "", wrapRead(err)
	}
	content, err := s.Store.ReadFile(ctx, path)
	if err != nil {
		return "", errf(KindNotFound, "ticket %s: not found", id)
	}
	return content, nil
}

// LintResult holds the findings for one ticket.
type LintResult struct {
	ID       string   `json:"id"`
	Findings []string `json:"findings,omitempty"`
}

// Lint checks one ticket or, when id is empty, every ticket on the head.
// The priority check needs dricket.toml; a missing config keeps the
// defaults (the starting set), a broken one is reported as a finding.
func (s *Service) Lint(ctx context.Context, id string) ([]LintResult, error) {
	if err := s.Store.Refresh(ctx); err != nil {
		return nil, wrapRead(err)
	}
	cfg, err := s.config(ctx)
	if err != nil {
		return nil, err
	}
	var files []store.FileContent
	var missing string // single-ticket lint: the path that was not found
	if id != "" {
		id = strings.ToLower(strings.TrimSpace(id))
		path, err := PathFor(id)
		if err != nil {
			return nil, err
		}
		content, rerr := s.Store.ReadFile(ctx, path)
		if rerr != nil {
			missing = path
		} else {
			files = []store.FileContent{{Path: path, Content: content}}
		}
	} else {
		var err error
		files, err = s.Store.ReadAll(ctx, "tickets/", "/ticket.md")
		if err != nil {
			return nil, wrapRead(err)
		}
	}
	var results []LintResult
	if missing != "" {
		results = append(results, LintResult{ID: dirID(missing), Findings: []string{fmt.Sprintf("%s: not found", missing)}})
	}
	for _, file := range files {
		p, content := file.Path, file.Content
		res := LintResult{ID: dirID(p)}
		doc, err := frontmatter.Parse(content)
		if err != nil {
			res.Findings = append(res.Findings, err.Error())
			results = append(results, res)
			continue
		}
		tk, unknown, err := ticket.FromDoc(doc)
		if err != nil {
			res.Findings = append(res.Findings, err.Error())
			results = append(results, res)
			continue
		}
		for _, u := range unknown {
			res.Findings = append(res.Findings, "warning: unknown key "+u)
		}
		for _, f := range ticket.LintWith(tk, cfg.Stages, true) {
			res.Findings = append(res.Findings, f.String())
		}
		if tk.Priority == "" {
			res.Findings = append(res.Findings, "warning priority: not set (default "+cfg.PriorityDefault+")")
		} else if perr := cfg.CheckPriority(tk.Priority); perr != nil {
			res.Findings = append(res.Findings, "error priority: "+perr.Error())
		}
		if len(res.Findings) > 0 {
			results = append(results, res)
		}
	}
	return results, nil
}

// wrapRead turns a store read failure into a service error.
func wrapRead(err error) error {
	return &Error{Kind: KindError, Msg: err.Error()}
}

// SearchHit is one matching line of `dk search`.
type SearchHit struct {
	ID   string `json:"id"`
	Path string `json:"path"`
	Line string `json:"line"`
}

// Search runs a fixed-string, case-insensitive full-text search over the
// ticket files of the fetched head (git grep; a local index comes only
// once a measurement shows slowness). `ls` filters by field, search reads
// the text.
func (s *Service) Search(ctx context.Context, query, prefix string) ([]SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errf(KindInvalid, "search needs a query")
	}
	prefix = strings.ToLower(strings.TrimSpace(prefix))
	if err := s.Store.Refresh(ctx); err != nil {
		return nil, wrapRead(err)
	}
	raw, err := s.Store.Grep(ctx, query, prefix)
	if err != nil {
		return nil, wrapRead(err)
	}
	var hits []SearchHit
	for _, l := range raw {
		// FETCH_HEAD:<path>:<lineno>:<text>
		parts := strings.SplitN(l, ":", 4)
		if len(parts) != 4 {
			continue
		}
		hits = append(hits, SearchHit{
			ID:   ticketIDOfPath(parts[1]),
			Path: parts[1],
			Line: strings.TrimRight(parts[3], "\r"),
		})
	}
	return hits, nil
}

// ticketIDOfPath derives the ticket id from a ticket-tree path.
func ticketIDOfPath(p string) string {
	parts := strings.Split(p, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i] == "comments" && i > 0 {
			return parts[i-1]
		}
	}
	if len(parts) >= 2 {
		return parts[len(parts)-2]
	}
	return ""
}
