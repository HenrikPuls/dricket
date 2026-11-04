// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"fmt"
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
	// Extensions are x-<name> fields set on creation (`add --set`).
	Extensions []FieldValue
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
	scanned := []string{req.Title, req.Description}
	for i, e := range req.Extensions {
		if cls, _ := ticket.ClassOf(e.Key); cls != ticket.Extension {
			return "", errf(KindInvalid, "add --set accepts only extension fields (x-<name>), got %q", e.Key)
		}
		v := strings.TrimSpace(e.Value)
		if err := ticket.CheckExtensionValue(e.Key, v); err != nil {
			return "", errf(KindInvalid, "%v", err)
		}
		req.Extensions[i].Value = v
		scanned = append(scanned, v)
	}
	if err := s.scanSecrets(ctx, scanned...); err != nil {
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
				Stage:         cfg.Stages.Initial,
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
			for _, e := range req.Extensions {
				tk.SetExtension(e.Key, e.Value)
			}
			if !req.Unclaimed {
				tk.Owner = s.Owner
				tk.ClaimedAt = now
				tk.LeaseUntil = now.Add(lease)
				tk.Epoch = 1
				tk.Rev = 1
				tk.Stage = cfg.Stages.Claim[0]
				appendLog(&tk, s.Owner, "add-claim "+id, now)
			} else {
				appendLog(&tk, s.Owner, "add "+id, now)
			}
			if err := s.checkLint(tk, id); err != nil {
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
			if err := s.checkLint(tk, id); err != nil {
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
			if !cfg.Stages.InClaim(tk.Stage) && !ticket.CanTransitionIn(cfg.Stages, cfg.Transitions, roleOf(kind), tk.Stage, cfg.Stages.Claim[0]) {
				return errf(KindInvalid, "%s may not move %s from %s to %s",
					roleOf(kind), id, tk.Stage, cfg.Stages.Claim[0])
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
			tk.Stage = cfg.Stages.AfterClaim(tk.Stage, result.Takeover)
			event := "claim " + id
			if result.Takeover {
				event = "takeover " + id + " (lease expired)"
			}
			appendLog(&tk, s.Owner, event, now)
			result.ID = id
			result.LeaseUntil = stamp(tk.LeaseUntil)
			if err := s.checkLint(tk, id); err != nil {
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
	result = sanitize(strings.TrimSpace(result), MaxBody)
	if err := s.scanSecrets(ctx, result); err != nil {
		return ReleaseResult{}, err
	}
	cfg, cerr := s.config(ctx)
	if cerr != nil {
		return ReleaseResult{}, cerr
	}
	if stage == "" {
		if !cfg.Stages.Legacy {
			return ReleaseResult{}, errf(KindInvalid, "release needs --stage with a configured vocabulary (one of %s)", cfg.Stages.Names())
		}
		stage = ticket.StageReview
	}
	if !cfg.Stages.Valid(stage) {
		return ReleaseResult{}, errf(KindInvalid, "stage %q is not one of %s", string(stage), cfg.Stages.Names())
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
			if !ticket.CanTransitionIn(cfg.Stages, cfg.Transitions, roleOf(tk.Kind), tk.Stage, stage) {
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
			if err := s.checkLint(tk, id); err != nil {
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

// Comment appends a comment as a new, add-only file. Everyone may
// comment; the file name is the UTC timestamp plus the author — no
// running number — so two comments collide only when the same author
// writes twice in the same second, and the allocation resolves that by
// trying the next second inside the precondition (docs/CONCEPT.md,
// "Comments"; ADR-0007).
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
			now := s.Now()
			// Successive seconds until a free, unique file name: the
			// only possible collision is the same author in the same
			// second, and the retry loop resolves it without a second
			// push.
			for i := 0; i < 8; i++ {
				stamp := now.Add(time.Duration(i) * time.Second).UTC()
				candidate := dir + "/" + commentFileName(stamp, s.Owner)
				has, herr := h.Has(candidate)
				if herr != nil {
					return herr
				}
				if has {
					continue
				}
				file = candidate
				content = fmt.Sprintf("---\nticket: %s\nauthor: %s\ncreated_at: %s\n---\n\n%s\n",
					id, s.Owner, stamp.Format(time.RFC3339), text)
				return nil
			}
			return errf(KindConflict, "could not allocate a comment file name for %s within 8 seconds", id)
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

// commentFileName renders the add-only comment file name: UTC timestamp
// without colons (Windows-safe) plus the author, e.g.
// 2026-11-03T10-04-24Z-henrik.md.
func commentFileName(t time.Time, author string) string {
	var b strings.Builder
	for _, r := range author {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.', r == '@', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return t.UTC().Format("2006-01-02T15-04-05Z") + "-" + b.String() + ".md"
}

// ------------------------------------------------------------- set ----

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
	case "priority":
		return tk.Priority
	}
	if v, ok := tk.Extension(key); ok {
		return v
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
