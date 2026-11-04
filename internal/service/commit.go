// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"strings"
	"time"

	"github.com/HenrikPuls/dricket/internal/frontmatter"
	"github.com/HenrikPuls/dricket/internal/store"
	"github.com/HenrikPuls/dricket/internal/ticket"
)

// CommitOptions controls an optional release in the same commit: the edited
// ticket is written and the claim is freed in one commit (ADR-0013). Stage
// and Note behave like the release arguments.
type CommitOptions struct {
	Release bool
	Stage   ticket.Stage
	Note    string
}

// CommitResult reports an accepted whole-ticket edit and whether the user
// checkout was moved to the accepted commit.
type CommitResult struct {
	ID     string `json:"id"`
	Rev    int    `json:"rev"`
	Commit string `json:"commit"`
	Synced bool   `json:"synced"`
	Stage  string `json:"stage,omitempty"`
}

// Commit publishes an edited claimed ticket if its fresh remote content is
// still byte-for-byte equal to the local edit base. With Release set it also
// frees the claim in the same commit, under the release transition policy.
func (s *Service) Commit(ctx context.Context, ws *store.Workspace, id string, opts CommitOptions) (CommitResult, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	path, err := PathFor(id)
	if err != nil {
		return CommitResult{}, err
	}
	base, edited, err := ws.ReadPair(ctx, path)
	if err != nil {
		return CommitResult{}, err
	}
	if base == edited {
		return CommitResult{}, errf(KindInvalid, "ticket %s has no local changes", id)
	}
	if err := s.scanSecrets(ctx, edited); err != nil {
		return CommitResult{}, err
	}
	editedDoc, err := frontmatter.Parse(edited)
	if err != nil {
		return CommitResult{}, errf(KindInvalid, "%s: %v", path, err)
	}
	candidate, _, err := ticket.FromDoc(editedDoc)
	if err != nil {
		return CommitResult{}, errf(KindInvalid, "%s: %v", path, err)
	}

	var cfgStages ticket.StageSet
	var cfgTransitions ticket.TransitionTable
	message := "dricket: commit " + id
	if opts.Release {
		opts.Note = sanitize(strings.TrimSpace(opts.Note), MaxBody)
		if err := s.scanSecrets(ctx, opts.Note); err != nil {
			return CommitResult{}, err
		}
		cfg, cerr := s.config(ctx)
		if cerr != nil {
			return CommitResult{}, cerr
		}
		if opts.Stage == "" {
			if !cfg.Stages.Legacy {
				return CommitResult{}, errf(KindInvalid, "commit --release needs --stage with a configured vocabulary (one of %s)", cfg.Stages.Names())
			}
			opts.Stage = ticket.StageReview
		}
		if !cfg.Stages.Valid(opts.Stage) {
			return CommitResult{}, errf(KindInvalid, "stage %q is not one of %s", string(opts.Stage), cfg.Stages.Names())
		}
		cfgStages = cfg.Stages
		cfgTransitions = cfg.Transitions
		message += " (release -> " + string(opts.Stage) + ")"
	}

	var toWrite ticket.Ticket
	var result CommitResult
	spec := store.WriteSpec{
		Message:          message,
		PreserveWorktree: true,
		Check: func(h store.Head) error {
			fresh, _, err := loadTicket(h, path)
			if err != nil {
				return err
			}
			if fresh.Owner != s.Owner {
				return errf(KindClaimLost, "ticket %s is no longer claimed by %s", id, s.Owner)
			}
			freshContent, err := h.ReadFile(path)
			if err != nil {
				return err
			}
			if freshContent != base {
				return errf(KindConflict, "ticket %s changed since the local edit base", id)
			}
			candidate := candidate
			if candidate.Owner != fresh.Owner || candidate.Kind != fresh.Kind ||
				candidate.Size != fresh.Size || !candidate.ClaimedAt.Equal(fresh.ClaimedAt) ||
				!candidate.LeaseUntil.Equal(fresh.LeaseUntil) || candidate.Epoch != fresh.Epoch ||
				candidate.Extensions != fresh.Extensions {
				return errf(KindInvalid, "ticket %s: claim fields change through their protocol commands", id)
			}
			candidate.Rev = fresh.Rev + 1
			if opts.Release {
				if !ticket.CanTransitionIn(cfgStages, cfgTransitions, roleOf(fresh.Kind), fresh.Stage, opts.Stage) {
					return errf(KindInvalid, "%s may not release %s from %s to %s",
						roleOf(fresh.Kind), id, fresh.Stage, opts.Stage)
				}
				candidate.Owner = ""
				candidate.ClaimedAt = time.Time{}
				candidate.LeaseUntil = time.Time{}
				candidate.Extensions = 0
				candidate.Stage = opts.Stage
			}
			if findings := ticket.ValidateTransition(fresh, candidate); len(findings) != 0 {
				messages := make([]string, 0, len(findings))
				for _, finding := range findings {
					messages = append(messages, finding.String())
				}
				return errf(KindInvalid, "ticket %s: %s", id, strings.Join(messages, "; "))
			}
			toWrite = candidate
			if opts.Release {
				event := "commit-release " + id + " -> " + string(opts.Stage)
				if opts.Note != "" {
					event += ": " + firstLine(opts.Note)
				}
				appendLog(&toWrite, s.Owner, event, s.Now())
				if opts.Note != "" {
					toWrite.Body = setResult(toWrite.Body, opts.Note)
				}
				if err := s.checkLint(toWrite, id); err != nil {
					return err
				}
			}
			result.ID = id
			result.Rev = candidate.Rev
			if opts.Release {
				result.Stage = string(opts.Stage)
			}
			return nil
		},
		Mutate: func(w store.Worktree) error {
			return w.WriteFile(path, toWrite.ToDoc().Render())
		},
	}
	if err := s.Store.Write(ctx, spec); err != nil {
		return CommitResult{}, err
	}
	result.Commit, err = s.Store.HeadSHA(ctx)
	if err != nil {
		return CommitResult{}, err
	}
	if err := ws.Adopt(ctx, result.Commit, path, edited); err != nil {
		result.Synced = false
		return result, nil
	}
	result.Synced = true
	return result, nil
}
