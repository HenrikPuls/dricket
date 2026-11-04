// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/HenrikPuls/dricket/internal/secrets"
	"github.com/HenrikPuls/dricket/internal/store"
	"github.com/HenrikPuls/dricket/internal/ticket"
)

// ------------------------------------------------------- secret scan ----

// scanSecrets checks untrusted text against the configured secret
// patterns before anything is written. A hit refuses the write (exit 19)
// and names the pattern and the line — never the content, so a leaked
// secret is not echoed into a terminal or a log.
func (s *Service) scanSecrets(ctx context.Context, texts ...string) error {
	cfg, err := s.config(ctx)
	if err != nil {
		return err
	}
	if !cfg.SecretScan {
		return nil
	}
	var findings []secrets.Finding
	for _, t := range texts {
		if f := secrets.Scan(t); len(f) > 0 {
			findings = append(findings, f...)
		}
	}
	if len(findings) == 0 {
		return nil
	}
	parts := make([]string, 0, len(findings))
	for _, f := range findings {
		parts = append(parts, f.String())
	}
	return errf(KindSecret,
		"refusing write: the secret scan found %d likely secret(s): %s. Rotate the credential and keep it out of tickets; the scan is incomplete, so this is no guarantee (exit 19)",
		len(findings), strings.Join(parts, ", "))
}

// ------------------------------------------------------------ quota ----

// checkQuota enforces the new-tickets-per-owner-and-day quota from the
// config. Counting uses the commit author identity of add commits, which
// makes it abuse protection, not a security boundary: an agent with shell
// access can forge authors.
func (s *Service) checkQuota(ctx context.Context) error {
	cfg, err := s.config(ctx)
	if err != nil {
		return err
	}
	if cfg.QuotaTicketsPerDay <= 0 {
		return nil
	}
	email := s.Store.AuthorEmail()
	if email == "" {
		return nil
	}
	n, err := s.Store.RecentAdds(ctx, 24*time.Hour, email)
	if err != nil {
		return errf(KindError, "quota check: %v", err)
	}
	if n >= cfg.QuotaTicketsPerDay {
		return errf(KindQuota,
			"quota reached: %d new ticket(s) by this identity in the last 24 h, limit %d (exit 18)",
			n, cfg.QuotaTicketsPerDay)
	}
	return nil
}

// ------------------------------------------------------ force-release ----

// ForceReleaseResult reports the outcome of an admin force-release.
type ForceReleaseResult struct {
	ID       string `json:"id"`
	Stage    string `json:"stage"`
	Released bool   `json:"released"` // false when the ticket was already free
}

// ForceRelease frees a ticket without the owner's consent: the runbook
// answer to dead claims. Only owners listed in security.admins may call
// it; an empty admins list means nobody (fail closed). The reason is
// mandatory and lands in the log. The epoch is a fencing token and stays,
// the extensions counter resets, and the ticket goes to the target stage
// (default open, so `next` picks it up again).
func (s *Service) ForceRelease(ctx context.Context, id, reason string, stage ticket.Stage) (ForceReleaseResult, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	path, err := PathFor(id)
	if err != nil {
		return ForceReleaseResult{}, err
	}
	reason = sanitize(strings.TrimSpace(reason), MaxBody)
	if reason == "" {
		return ForceReleaseResult{}, errf(KindInvalid, "force-release requires a reason")
	}
	cfg, cerr := s.config(ctx)
	if cerr != nil {
		return ForceReleaseResult{}, cerr
	}
	if !cfg.IsAdmin(s.Owner) {
		return ForceReleaseResult{}, errf(KindForbidden,
			"force-release: owner %s is not in security.admins of dricket.toml (exit 17)", s.Owner)
	}
	if stage == "" {
		stage = cfg.Stages.Initial
	}
	if !cfg.Stages.Valid(stage) {
		return ForceReleaseResult{}, errf(KindInvalid, "stage %q is not one of %s", string(stage), cfg.Stages.Names())
	}

	var out ForceReleaseResult
	spec := store.WriteSpec{
		Message: "dricket: force-release " + id,
		Check: func(h store.Head) error {
			tk, _, err := loadTicket(h, path)
			if err != nil {
				return err
			}
			if tk.Owner == "" {
				// Idempotent, like release: an already free ticket
				// stays free.
				out = ForceReleaseResult{ID: id, Stage: string(tk.Stage), Released: false}
				return errStop{idempotent: true}
			}
			out = ForceReleaseResult{ID: id, Stage: string(stage), Released: true}
			return nil
		},
		Mutate: func(w store.Worktree) error {
			tk, err := readWorktreeTicket(w, path)
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
			appendLog(&tk, s.Owner, "force-release "+id+" by "+s.Owner+": "+firstLine(reason), now)
			if err := s.checkLint(tk, id); err != nil {
				return err
			}
			return w.WriteFile(path, tk.ToDoc().Render())
		},
	}
	if err := s.Store.Write(ctx, spec); err != nil {
		var stop errStop
		if errors.As(err, &stop) {
			return out, nil // idempotent force-release
		}
		return ForceReleaseResult{}, err
	}
	return out, nil
}
