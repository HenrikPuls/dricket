// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"strings"

	"github.com/lariscus/dricket/internal/config"
	"github.com/lariscus/dricket/internal/frontmatter"
	"github.com/lariscus/dricket/internal/store"
	"github.com/lariscus/dricket/internal/ticket"
)

// MaxDocument bounds a whole-ticket write: the body limit plus room for
// the header.
const MaxDocument = MaxBody + 32*1024

// WriteMode says on what grounds a caller writes a ticket without having
// checked it out: it owns the claim, or it is an admin overriding a claim.
type WriteMode struct {
	Claimant bool
	Forced   bool
}

// writeRights decides whether the caller may write fresh outside the claim
// protocol (ADR-0013): the claim holder always; an unclaimed ticket only
// under --claim, never claim-free; someone else's claim is exit 11, an
// admin may override with --force.
func (s *Service) writeRights(cfg *config.Config, fresh ticket.Ticket, force bool, id string) (WriteMode, error) {
	admin := cfg.IsAdmin(s.Owner)
	switch fresh.Owner {
	case s.Owner:
		if force {
			return WriteMode{}, errf(KindInvalid, "--force applies to a ticket claimed by someone else; you hold the claim on %s", id)
		}
		return WriteMode{Claimant: true}, nil
	case "":
		if force {
			return WriteMode{}, errf(KindInvalid, "--force applies to a claimed ticket; %s is not claimed", id)
		}
		return WriteMode{}, errf(KindForbidden,
			"put on unclaimed %s needs --claim: no write without a claim (exit 17)", id)
	default:
		if !force {
			return WriteMode{}, errf(KindClaimed, "ticket %s is claimed by %s (lease until %s); an admin may pass --force",
				id, fresh.Owner, stamp(fresh.LeaseUntil))
		}
		if !admin {
			return WriteMode{}, errf(KindForbidden,
				"put --force: owner %s is not in security.admins of dricket.toml (exit 17)", s.Owner)
		}
		return WriteMode{Forced: true}, nil
	}
}

// checkVersion applies --if-version; mandatory for callers that do not own
// the claim, because they have no exact edit base.
//
// The version is a pointer because rev 0 is a real version: a ticket that
// was added unclaimed and never written again is at rev 0.
func checkVersion(fresh ticket.Ticket, mode WriteMode, ifVersion *int, id string) error {
	if ifVersion == nil {
		if !mode.Claimant {
			return errf(KindInvalid, "put needs --if-version unless you hold the claim (current rev of %s is %d)", id, fresh.Rev)
		}
		return nil
	}
	if fresh.Rev != *ifVersion {
		return errf(KindConflict, "ticket %s is at rev %d, expected %d", id, fresh.Rev, *ifVersion)
	}
	return nil
}

// ---------------------------------------------------------------- put ----

// PutOptions are the flags of `dk put`.
type PutOptions struct {
	IfVersion *int // nil: not given
	Force     bool
	// Claim runs the write under a short claim (ADR-0012): the ticket
	// must be unclaimed, and --if-version is still required because the
	// caller holds no committed claim.
	Claim bool
}

// PutResult reports an accepted whole-ticket write. When the document
// equals the current ticket nothing is committed: rev stays, ChangedFields
// is empty and Commit is the current head.
type PutResult struct {
	ID            string   `json:"id"`
	Rev           int      `json:"rev"`
	Commit        string   `json:"commit"`
	ChangedFields []string `json:"changed_fields"`
	ShortClaim    bool     `json:"short_claim,omitempty"`
}

// Put publishes the complete text of a ticket as the new content, without a
// checkout and without the exact-base compare of `commit`. Identity and
// claim fields must be unchanged; rev becomes fresh + 1; the secret scan,
// the size limit and the lint of the whole transition apply. See
// writeRights for who may call it.
func (s *Service) Put(ctx context.Context, id, content string, opts PutOptions) (PutResult, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	path, err := PathFor(id)
	if err != nil {
		return PutResult{}, err
	}
	// Line endings are normalised so that a CRLF editor on Windows cannot
	// smuggle carriage returns into the body or break byte comparisons.
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if len(content) > MaxDocument {
		return PutResult{}, errf(KindInvalid, "ticket document is %d bytes, the limit is %d", len(content), MaxDocument)
	}
	doc, err := frontmatter.Parse(content)
	if err != nil {
		return PutResult{}, errf(KindInvalid, "document: %v", err)
	}
	candidate, _, err := ticket.FromDoc(doc)
	if err != nil {
		return PutResult{}, errf(KindInvalid, "document: %v", err)
	}
	if len(candidate.Body) > MaxBody {
		return PutResult{}, errf(KindInvalid, "body is %d bytes, the limit is %d", len(candidate.Body), MaxBody)
	}
	if err := s.scanSecrets(ctx, content); err != nil {
		return PutResult{}, err
	}
	cfg, cerr := s.config(ctx)
	if cerr != nil {
		return PutResult{}, cerr
	}

	var out PutResult
	var toWrite ticket.Ticket
	spec := store.WriteSpec{
		Message: "dricket: put " + id,
		Check: func(h store.Head) error {
			fresh, _, err := loadTicket(h, path)
			if err != nil {
				return err
			}
			var mode WriteMode
			if opts.Claim {
				if err := s.checkShortClaim(fresh, opts.Force, id); err != nil {
					return err
				}
			} else {
				if mode, err = s.writeRights(cfg, fresh, opts.Force, id); err != nil {
					return err
				}
			}
			if err := checkVersion(fresh, mode, opts.IfVersion, id); err != nil {
				return err
			}
			c := candidate
			if c.Owner != fresh.Owner || c.Kind != fresh.Kind ||
				c.Size != fresh.Size || !c.ClaimedAt.Equal(fresh.ClaimedAt) ||
				!c.LeaseUntil.Equal(fresh.LeaseUntil) || c.Epoch != fresh.Epoch ||
				c.Extensions != fresh.Extensions {
				return errf(KindInvalid, "ticket %s: claim fields change through their protocol commands", id)
			}
			c.Rev = fresh.Rev
			changed := changedFields(fresh, c)
			if len(changed) == 0 {
				out = PutResult{ID: id, Rev: fresh.Rev, ChangedFields: []string{}}
				return errStop{idempotent: true}
			}
			c.Rev = fresh.Rev + 1
			// The priority values come from dricket.toml; without this the
			// write path would accept values that validate later rejects.
			if c.Priority != "" {
				if err := cfg.CheckPriority(strings.ToLower(c.Priority)); err != nil {
					return errf(KindInvalid, "%v", err)
				}
			}
			if findings := ticket.ValidateTransitionWith(fresh, c, cfg.Stages, true); len(findings) != 0 {
				messages := make([]string, 0, len(findings))
				for _, finding := range findings {
					messages = append(messages, finding.String())
				}
				return errf(KindInvalid, "ticket %s: %s", id, strings.Join(messages, "; "))
			}
			toWrite = c
			out = PutResult{ID: id, Rev: c.Rev, ChangedFields: changed}
			return nil
		},
		Mutate: func(w store.Worktree) error {
			return w.WriteFile(path, toWrite.ToDoc().Render())
		},
	}
	if opts.Force {
		spec.Message = "dricket: put " + id + " (admin override of a claim)"
	}
	if opts.Claim {
		// put never touches the document's log; the commit message
		// carries the mark of the short claim (ADR-0012).
		spec.Message += shortClaimMark
	}
	err = s.Store.Write(ctx, spec)
	var stop errStop
	if err != nil && !errors.As(err, &stop) {
		return PutResult{}, err
	}
	out.ShortClaim = opts.Claim
	out.Commit, err = s.Store.HeadSHA(ctx)
	if err != nil {
		return PutResult{}, err
	}
	return out, nil
}

// changedFields lists the header keys whose value differs between two
// tickets (rev excluded), then "body" when the body differs. Both sides are
// compared in canonical form, so field order and the empty/absent
// distinction of a hand-edited file do not count as changes.
func changedFields(a, b ticket.Ticket) []string {
	da, db := a.ToDoc(), b.ToDoc()
	out := []string{}
	seen := map[string]bool{}
	check := func(key string) {
		if seen[key] || key == "rev" {
			return
		}
		seen[key] = true
		va, oka := da.Get(key)
		vb, okb := db.Get(key)
		if oka != okb || va != vb {
			out = append(out, key)
		}
	}
	for _, f := range da.Fields {
		check(f.Key)
	}
	for _, f := range db.Fields {
		check(f.Key)
	}
	if a.Body != b.Body {
		out = append(out, "body")
	}
	return out
}
