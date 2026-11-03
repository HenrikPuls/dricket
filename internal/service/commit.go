// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"strings"

	"github.com/HenrikPuls/dricket/internal/frontmatter"
	"github.com/HenrikPuls/dricket/internal/store"
	"github.com/HenrikPuls/dricket/internal/ticket"
)

// CommitResult reports an accepted whole-ticket edit and whether the user
// checkout was moved to the accepted commit.
type CommitResult struct {
	ID     string `json:"id"`
	Rev    int    `json:"rev"`
	Commit string `json:"commit"`
	Synced bool   `json:"synced"`
}

// Commit publishes an edited claimed ticket if its fresh remote content is
// still byte-for-byte equal to the local edit base.
func (s *Service) Commit(ctx context.Context, ws *store.Workspace, id string) (CommitResult, error) {
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

	var toWrite ticket.Ticket
	var result CommitResult
	spec := store.WriteSpec{
		Message: "dricket: commit " + id,
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
			if findings := ticket.ValidateTransition(fresh, candidate); len(findings) != 0 {
				messages := make([]string, 0, len(findings))
				for _, finding := range findings {
					messages = append(messages, finding.String())
				}
				return errf(KindInvalid, "ticket %s: %s", id, strings.Join(messages, "; "))
			}
			toWrite = candidate
			result.ID = id
			result.Rev = candidate.Rev
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
