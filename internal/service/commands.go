// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/HenrikPuls/dricket/internal/frontmatter"
	"github.com/HenrikPuls/dricket/internal/store"
	"github.com/HenrikPuls/dricket/internal/ticket"
)

// ------------------------------------------------------------- add ----

// AddRequest is `dk add`; Title is required unless Unclaimed is used with a
// title anyway (the title is always required by lint).
type AddRequest struct {
	Prefix      string
	Title       string
	Description string
	Labels      []string
	Depends     []string
	Parent      string
	Unclaimed   bool
	Size        ticket.Size
	Kind        ticket.Kind
	Priority    string
}

// Add creates a ticket with an id allocated by push retry: the number is
// computed on the fresh head inside the precondition, so a lost race just
// recomputes it on the next attempt.
func (s *Service) Add(ctx context.Context, req AddRequest) (string, error) {
	req.Title = sanitize(strings.TrimSpace(req.Title), MaxTitle)
	req.Prefix = strings.ToLower(strings.TrimSpace(req.Prefix))
	if req.Prefix == "" {
		return "", errf(KindInvalid, "a project prefix is required")
	}
	if req.Title == "" {
		return "", errf(KindInvalid, "a title is required")
	}
	// Content checks run before any git access: the secret scan first,
	// so a leaked credential is reported even when the quota is spent.
	if err := s.scanSecrets(ctx, req.Title, req.Description); err != nil {
		return "", err
	}
	if err := s.checkQuota(ctx); err != nil {
		return "", err
	}
	// The priority comes from the flag or the config's default value.
	cfg, err := s.config(ctx)
	if err != nil {
		return "", err
	}
	priority := strings.ToLower(strings.TrimSpace(req.Priority))
	if priority == "" {
		priority = cfg.PriorityDefault
	}
	if err := cfg.CheckPriority(priority); err != nil {
		return "", errf(KindInvalid, "%v", err)
	}
	if req.Size == "" {
		req.Size = ticket.SizeM
	}
	if req.Kind == "" {
		req.Kind = ticket.KindAgent
	}
	var lease time.Duration
	if !req.Unclaimed {
		var err error
		if lease, err = s.leaseFor(ctx, req.Size); err != nil {
			return "", err
		}
	}

	var id string
	var path string
	var tk ticket.Ticket
	spec := store.WriteSpec{
		Message: "dricket: add ticket",
		Check: func(h store.Head) error {
			n, err := nextNumber(h, req.Prefix)
			if err != nil {
				return err
			}
			id = fmt.Sprintf("%s-%d", req.Prefix, n)
			var perr error
			path, perr = PathFor(id)
			if perr != nil {
				return perr
			}
			now := s.Now()
			tk = ticket.Ticket{
				ID:            id,
				CreatedAt:     now,
				CreatedBy:     s.Owner,
				SchemaVersion: ticket.SchemaVersion,
				Title:         req.Title,
				Stage:         ticket.StageOpen,
				Kind:          req.Kind,
				Size:          req.Size,
				Priority:      priority,
				Epoch:         0,
				Rev:           0,
				Labels:        req.Labels,
				Depends:       req.Depends,
				Parent:        strings.ToLower(req.Parent),
				Body:          bodyFor(req.Description),
			}
			if !req.Unclaimed {
				tk.Owner = s.Owner
				tk.ClaimedAt = now
				tk.LeaseUntil = now.Add(lease)
				tk.Epoch = 1
				tk.Rev = 1
				tk.Stage = ticket.StageInProgress
				appendLog(&tk, s.Owner, "add-claim "+id, now)
			} else {
				appendLog(&tk, s.Owner, "add "+id, now)
			}
			if err := checkLint(tk, id); err != nil {
				return err
			}
			return nil
		},
		Mutate: func(w store.Worktree) error {
			return w.WriteFile(path, tk.ToDoc().Render())
		},
	}
	if err := s.Store.Write(ctx, spec); err != nil {
		return "", err
	}
	return id, nil
}

func bodyFor(description string) string {
	d := strings.TrimSpace(description)
	if d == "" {
		d = "(no description)"
	}
	return fmt.Sprintf("\n## Description\n\n%s\n\n## Result\n\n\n## Log\n", d)
}

// ---------------------------------------------------------- claim ----

// ClaimResult reports the outcome of a claim.
type ClaimResult struct {
	ID         string `json:"id"`
	Epoch      int    `json:"epoch"`
	LeaseUntil string `json:"lease_until"`
	Takeover   bool   `json:"takeover"`
}

// Claim takes a ticket: free tickets first, expired leases after the grace
// period by takeover, own tickets idempotently without a commit.
func (s *Service) Claim(ctx context.Context, id string, size ticket.Size, kind ticket.Kind) (ClaimResult, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	path, err := PathFor(id)
	if err != nil {
		return ClaimResult{}, err
	}
	if size == "" {
		size = ticket.SizeM
	}
	lease, err := s.leaseFor(ctx, size)
	if err != nil {
		return ClaimResult{}, err
	}
	if kind == "" {
		kind = ticket.KindAgent
	}
	cfg, cerr := s.config(ctx)
	if cerr != nil {
		return ClaimResult{}, cerr
	}

	var result ClaimResult
	spec := store.WriteSpec{
		Message: "dricket: claim " + id,
		Check: func(h store.Head) error {
			tk, _, err := loadTicket(h, path)
			if err != nil {
				return err
			}
			if err := checkLint(tk, id); err != nil {
				return err
			}
			now := s.Now()
			switch tk.Owner {
			case s.Owner:
				// Idempotent: claiming a ticket I already own succeeds
				// without a new commit.
				result = ClaimResult{ID: id, Epoch: tk.Epoch,
					LeaseUntil: stamp(tk.LeaseUntil), Takeover: false}
				return errStop{idempotent: true}
			case "":
				// Free ticket: a normal claim.
			default:
				if lu := tk.LeaseUntil; !lu.IsZero() && lu.Add(Grace).After(now) {
					return errf(KindClaimed, "ticket %s is claimed by %s (lease until %s)",
						id, tk.Owner, stamp(tk.LeaseUntil))
				}
				result.Takeover = true
			}
			if !ticket.CanTransitionTable(cfg.Transitions, roleOf(kind), tk.Stage, ticket.StageInProgress) && tk.Stage != ticket.StageInProgress {
				return errf(KindInvalid, "%s may not move %s from %s to %s",
					roleOf(kind), id, tk.Stage, ticket.StageInProgress)
			}
			result.Epoch = tk.Epoch + 1
			return nil
		},
		Mutate: func(w store.Worktree) error {
			content, err := w.ReadFile(path)
			if err != nil {
				return err
			}
			doc, err := frontmatter.Parse(content)
			if err != nil {
				return err
			}
			tk, _, err := ticket.FromDoc(doc)
			if err != nil {
				return err
			}
			now := s.Now()
			tk.Owner = s.Owner
			tk.Kind = kind
			tk.Size = size
			tk.ClaimedAt = now
			tk.LeaseUntil = now.Add(lease)
			tk.Epoch = result.Epoch
			tk.Rev++
			tk.Extensions = 0
			if tk.Stage == ticket.StageOpen || result.Takeover {
				tk.Stage = ticket.StageInProgress
			}
			event := "claim " + id
			if result.Takeover {
				event = "takeover " + id + " (lease expired)"
			}
			appendLog(&tk, s.Owner, event, now)
			result.ID = id
			result.LeaseUntil = stamp(tk.LeaseUntil)
			if err := checkLint(tk, id); err != nil {
				return err
			}
			return w.WriteFile(path, tk.ToDoc().Render())
		},
	}
	err = s.Store.Write(ctx, spec)
	if err == nil {
		return result, nil
	}
	var stop errStop
	if errors.As(err, &stop) {
		return result, nil // idempotent claim
	}
	return ClaimResult{}, err
}

// -------------------------------------------------------- release ----

// ReleaseResult reports the outcome of a release.
type ReleaseResult struct {
	ID    string `json:"id"`
	Stage string `json:"stage"`
}

// Release gives a ticket free, optionally with a target stage and a result
// note. Releasing an unclaimed ticket is idempotent.
func (s *Service) Release(ctx context.Context, id string, stage ticket.Stage, result string) (ReleaseResult, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	path, err := PathFor(id)
	if err != nil {
		return ReleaseResult{}, err
	}
	if stage == "" {
		stage = ticket.StageReview
	}
	result = sanitize(strings.TrimSpace(result), MaxBody)
	if err := s.scanSecrets(ctx, result); err != nil {
		return ReleaseResult{}, err
	}
	cfg, cerr := s.config(ctx)
	if cerr != nil {
		return ReleaseResult{}, cerr
	}

	var out ReleaseResult
	spec := store.WriteSpec{
		Message: "dricket: release " + id,
		Check: func(h store.Head) error {
			tk, _, err := loadTicket(h, path)
			if err != nil {
				return err
			}
			if tk.Owner == "" {
				// Idempotent: an already free ticket stays free.
				out = ReleaseResult{ID: id, Stage: string(tk.Stage)}
				return errStop{idempotent: true}
			}
			if tk.Owner != s.Owner {
				return errf(KindClaimed, "ticket %s is claimed by %s, not by you", id, tk.Owner)
			}
			// The role comes from the kind of the claim (concept: the
			// role comes from `kind`); before stage v2 this check
			// wrongly used the agent role for every release.
			if !ticket.CanTransitionTable(cfg.Transitions, roleOf(tk.Kind), tk.Stage, stage) {
				return errf(KindInvalid, "%s may not release %s from %s to %s",
					roleOf(tk.Kind), id, tk.Stage, stage)
			}
			return nil
		},
		Mutate: func(w store.Worktree) error {
			content, err := w.ReadFile(path)
			if err != nil {
				return err
			}
			doc, err := frontmatter.Parse(content)
			if err != nil {
				return err
			}
			tk, _, err := ticket.FromDoc(doc)
			if err != nil {
				return err
			}
			now := s.Now()
			tk.Owner = ""
			tk.ClaimedAt = time.Time{}
			tk.LeaseUntil = time.Time{}
			tk.Extensions = 0
			tk.Stage = stage
			tk.Rev++
			event := "release " + id + " -> " + string(stage)
			if result != "" {
				event += ": " + firstLine(result)
			}
			appendLog(&tk, s.Owner, event, now)
			if result != "" {
				tk.Body = setResult(tk.Body, result)
			}
			if err := checkLint(tk, id); err != nil {
				return err
			}
			out = ReleaseResult{ID: id, Stage: string(stage)}
			return w.WriteFile(path, tk.ToDoc().Render())
		},
	}
	err = s.Store.Write(ctx, spec)
	if err == nil {
		return out, nil
	}
	var stop errStop
	if errors.As(err, &stop) {
		return out, nil // idempotent release
	}
	return ReleaseResult{}, err
}

// setResult replaces the "## Result" section; the tool owns that block.
func setResult(body, text string) string {
	head, rest := body, ""
	if i := strings.Index(body, "## Result"); i >= 0 {
		head = body[:i]
		if j := strings.Index(body[i:], "\n## Log"); j >= 0 {
			rest = body[i+j:]
		}
	} else if j := strings.Index(body, "## Log"); j >= 0 {
		head = body[:j]
		rest = body[j:]
	}
	out := strings.TrimRight(head, "\n") + "\n\n## Result\n\n" + strings.TrimSpace(text) + "\n"
	if rest != "" {
		out += rest
	}
	return out
}

// -------------------------------------------------------- comment ----

// Comment appends a comment as a new, add-only file. The number is
// allocated per ticket by push retry, like ticket ids.
func (s *Service) Comment(ctx context.Context, id, text string) (string, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	path, err := PathFor(id)
	if err != nil {
		return "", err
	}
	dir := strings.TrimSuffix(path, "ticket.md") + "comments"
	text = sanitize(strings.TrimRight(text, "\n"), MaxComment)
	if strings.TrimSpace(text) == "" {
		return "", errf(KindInvalid, "comment text is empty")
	}
	if err := s.scanSecrets(ctx, text); err != nil {
		return "", err
	}

	var file string
	var content string
	spec := store.WriteSpec{
		Message: "dricket: comment on " + id,
		Check: func(h store.Head) error {
			files, err := h.List(dir + "/")
			if err != nil {
				return err
			}
			highest := 0
			for _, f := range files {
				name := strings.TrimSuffix(strings.TrimPrefix(f, dir+"/"), ".md")
				if n, err := strconv.Atoi(name); err == nil && n > highest {
					highest = n
				}
			}
			file = fmt.Sprintf("%s/%04d.md", dir, highest+1)
			now := s.Now()
			content = fmt.Sprintf("---\nticket: %s\nnumber: %04d\nauthor: %s\ncreated_at: %s\n---\n\n%s\n",
				id, highest+1, s.Owner, now.UTC().Format(time.RFC3339), text)
			if has, err := h.Has(file); err != nil {
				return err
			} else if has {
				return errf(KindConflict, "comment file %s already exists", file)
			}
			return nil
		},
		Mutate: func(w store.Worktree) error {
			return w.WriteFile(file, content)
		},
	}
	if err := s.Store.Write(ctx, spec); err != nil {
		return "", err
	}
	return file, nil
}

// ------------------------------------------------------------- set ----

// SetResult reports a changed free field.
type SetResult struct {
	ID    string `json:"id"`
	Key   string `json:"key"`
	Value string `json:"value"`
	Rev   int    `json:"rev"`
}

// Set changes one free field. State and identity fields are rejected: they
// change only through commands. IfVersion guards against races (exit 12).
func (s *Service) Set(ctx context.Context, id, key, value string, ifVersion int) (SetResult, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	path, err := PathFor(id)
	if err != nil {
		return SetResult{}, err
	}
	// The field classes live in the ticket model; state and identity
	// fields change only through commands.
	cls, known := ticket.ClassOf(key)
	if !known {
		return SetResult{}, errf(KindInvalid, "unknown field %q", key)
	}
	if cls != ticket.Free {
		return SetResult{}, errf(KindInvalid, "%s is not a free field; it changes only through a command", key)
	}
	value = sanitize(strings.TrimSpace(value), MaxBody)
	if key == "priority" {
		// The allowed values come from dricket.toml.
		cfg, cerr := s.config(ctx)
		if cerr != nil {
			return SetResult{}, cerr
		}
		if err := cfg.CheckPriority(strings.ToLower(value)); err != nil {
			return SetResult{}, errf(KindInvalid, "%v", err)
		}
		value = strings.ToLower(value)
	}
	if err := s.scanSecrets(ctx, value); err != nil {
		return SetResult{}, err
	}

	var out SetResult
	spec := store.WriteSpec{
		Message: "dricket: set " + key + " on " + id,
		Check: func(h store.Head) error {
			tk, _, err := loadTicket(h, path)
			if err != nil {
				return err
			}
			if ifVersion > 0 && tk.Rev != ifVersion {
				return errf(KindConflict, "ticket %s is at rev %d, expected %d", id, tk.Rev, ifVersion)
			}
			if tk.Owner != "" && tk.Owner != s.Owner {
				return errf(KindClaimed, "ticket %s is claimed by %s", id, tk.Owner)
			}
			out = SetResult{ID: id, Key: key, Rev: tk.Rev + 1}
			return nil
		},
		Mutate: func(w store.Worktree) error {
			content, err := w.ReadFile(path)
			if err != nil {
				return err
			}
			doc, err := frontmatter.Parse(content)
			if err != nil {
				return err
			}
			tk, _, err := ticket.FromDoc(doc)
			if err != nil {
				return err
			}
			switch key {
			case "title":
				if value == "" {
					return errf(KindInvalid, "title must not be empty")
				}
				tk.Title = value
			case "parent":
				tk.Parent = strings.ToLower(value)
			case "branch":
				tk.Branch = value
			case "priority":
				tk.Priority = value
			case "labels", "depends", "touches", "repos":
				var list []string
				for _, p := range strings.Split(value, ",") {
					if p = strings.TrimSpace(p); p != "" {
						list = append(list, p)
					}
				}
				if key == "depends" {
					for _, d := range list {
						if !ticket.ValidID(d) {
							return errf(KindInvalid, "%q is not a ticket id", d)
						}
					}
				}
				setList(&tk, key, list)
			}
			tk.Rev++
			appendLog(&tk, s.Owner, "set "+key, s.Now())
			if err := checkLint(tk, id); err != nil {
				return err
			}
			out.Value = fieldValue(tk, key)
			return w.WriteFile(path, tk.ToDoc().Render())
		},
	}
	if err := s.Store.Write(ctx, spec); err != nil {
		return SetResult{}, err
	}
	return out, nil
}

func setList(tk *ticket.Ticket, key string, list []string) {
	switch key {
	case "labels":
		tk.Labels = list
	case "depends":
		tk.Depends = list
	case "touches":
		tk.Touches = list
	case "repos":
		tk.Repos = list
	}
}

func fieldValue(tk ticket.Ticket, key string) string {
	switch key {
	case "title":
		return tk.Title
	case "parent":
		return tk.Parent
	case "branch":
		return tk.Branch
	case "labels":
		return strings.Join(tk.Labels, ", ")
	case "depends":
		return strings.Join(tk.Depends, ", ")
	case "touches":
		return strings.Join(tk.Touches, ", ")
	case "repos":
		return strings.Join(tk.Repos, ", ")
	}
	return ""
}

func roleOf(kind ticket.Kind) ticket.Role {
	if kind == ticket.KindHuman {
		return ticket.RoleHuman
	}
	return ticket.RoleAgent
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// errStop ends a write as a successful no-op (idempotent commands). The
// store reports it as a precondition outcome; the command layer knows it
// means "nothing to do, exit 0".
type errStop struct{ idempotent bool }

func (e errStop) Error() string { return "idempotent no-op" }

// firstLine keeps log events on one line.
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 160 {
		s = s[:160]
	}
	return s
}
