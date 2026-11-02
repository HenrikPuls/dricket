// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/HenrikPuls/dricket/internal/frontmatter"
	"github.com/HenrikPuls/dricket/internal/ticket"
)

// ListItem is one row of `dk ls`.
type ListItem struct {
	ID     string `json:"id"`
	Stage  string `json:"stage"`
	Owner  string `json:"owner"`
	Title  string `json:"title"`
	Kind   string `json:"kind,omitempty"`
	Epoch  int    `json:"epoch"`
	Rev    int    `json:"rev"`
	Labels string `json:"labels,omitempty"`
}

// ListFilter narrows `dk ls`; empty fields mean "no restriction".
type ListFilter struct {
	Stage  string
	Owner  string // "@me" resolves to the service owner
	Prefix string
}

// List returns tickets from the current head, sorted by id.
func (s *Service) List(ctx context.Context, f ListFilter) ([]ListItem, []string, error) {
	if err := s.Store.Refresh(ctx); err != nil {
		return nil, nil, wrapRead(err)
	}
	paths, err := s.Store.List(ctx, "tickets/")
	if err != nil {
		return nil, nil, wrapRead(err)
	}
	owner := f.Owner
	if owner == "@me" {
		owner = s.Owner
	}
	var items []ListItem
	var warnings []string
	for _, p := range paths {
		if !strings.HasSuffix(p, "/ticket.md") {
			continue
		}
		prefix, _, serr := splitID(dirID(p))
		if serr != nil {
			warnings = append(warnings, fmt.Sprintf("%s: %v", p, serr))
			continue
		}
		if f.Prefix != "" && prefix != strings.ToLower(f.Prefix) {
			continue
		}
		content, err := s.Store.ReadFile(ctx, p)
		if err != nil {
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
		items = append(items, ListItem{
			ID:     tk.ID,
			Stage:  string(tk.Stage),
			Owner:  tk.Owner,
			Title:  tk.Title,
			Kind:   string(tk.Kind),
			Epoch:  tk.Epoch,
			Rev:    tk.Rev,
			Labels: strings.Join(tk.Labels, ","),
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
func (s *Service) Lint(ctx context.Context, id string) ([]LintResult, error) {
	if err := s.Store.Refresh(ctx); err != nil {
		return nil, wrapRead(err)
	}
	var paths []string
	if id != "" {
		id = strings.ToLower(strings.TrimSpace(id))
		path, err := PathFor(id)
		if err != nil {
			return nil, err
		}
		paths = []string{path}
	} else {
		var err error
		paths, err = s.Store.List(ctx, "tickets/")
		if err != nil {
			return nil, wrapRead(err)
		}
	}
	var results []LintResult
	for _, p := range paths {
		if !strings.HasSuffix(p, "/ticket.md") {
			continue
		}
		res := LintResult{ID: dirID(p)}
		content, err := s.Store.ReadFile(ctx, p)
		if err != nil {
			res.Findings = append(res.Findings, fmt.Sprintf("%s: not found", p))
			results = append(results, res)
			continue
		}
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
		for _, f := range ticket.Lint(tk) {
			res.Findings = append(res.Findings, f.String())
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
