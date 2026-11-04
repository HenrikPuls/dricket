// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/HenrikPuls/dricket/internal/config"
	"github.com/HenrikPuls/dricket/internal/frontmatter"
	"github.com/HenrikPuls/dricket/internal/store"
	"github.com/HenrikPuls/dricket/internal/ticket"
)

// MaxDocument bounds a whole-ticket write: the body limit plus room for
// the header.
const MaxDocument = MaxBody + 32*1024

// WriteMode says on what grounds a caller writes a ticket without having
// checked it out: it owns the claim, it is a human or admin on an unclaimed
// ticket (a "coordinator write"), or it is an admin overriding a claim.
type WriteMode struct {
	Claimant bool
	Forced   bool
}

// writeRights decides whether the caller may write fresh outside the claim
// protocol. kind is the caller's self-declared kind (like `claim --kind`);
// roles are detected, not enforced against the repository. The rules are
// shared by put and move.
func (s *Service) writeRights(cfg *config.Config, fresh ticket.Ticket, kind ticket.Kind, force bool, id, verb string) (WriteMode, error) {
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
		if kind != ticket.KindHuman && !admin {
			return WriteMode{}, errf(KindForbidden,
				"%s on unclaimed %s needs --kind human or an admin from security.admins; owner %s is neither (exit 17)", verb, id, s.Owner)
		}
		return WriteMode{}, nil
	default:
		if !force {
			return WriteMode{}, errf(KindClaimed, "ticket %s is claimed by %s (lease until %s); an admin may pass --force",
				id, fresh.Owner, stamp(fresh.LeaseUntil))
		}
		if !admin {
			return WriteMode{}, errf(KindForbidden,
				"%s --force: owner %s is not in security.admins of dricket.toml (exit 17)", verb, s.Owner)
		}
		return WriteMode{Forced: true}, nil
	}
}

// checkVersion applies --if-version; mandatory for callers that do not own
// the claim, because they have no exact edit base.
//
// The version is a pointer because rev 0 is a real version: a ticket that
// was added unclaimed and never written again is at rev 0.
func checkVersion(fresh ticket.Ticket, mode WriteMode, ifVersion *int, id, verb string) error {
	if ifVersion == nil {
		if !mode.Claimant {
			return errf(KindInvalid, "%s needs --if-version unless you hold the claim (current rev of %s is %d)", verb, id, fresh.Rev)
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
	Kind      ticket.Kind // caller kind; empty means agent
}

// PutResult reports an accepted whole-ticket write. When the document
// equals the current ticket nothing is committed: rev stays, ChangedFields
// is empty and Commit is the current head.
type PutResult struct {
	ID            string   `json:"id"`
	Rev           int      `json:"rev"`
	Commit        string   `json:"commit"`
	ChangedFields []string `json:"changed_fields"`
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
	kind := opts.Kind
	if kind == "" {
		kind = ticket.KindAgent
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
			mode, err := s.writeRights(cfg, fresh, kind, opts.Force, id, "put")
			if err != nil {
				return err
			}
			if err := checkVersion(fresh, mode, opts.IfVersion, id, "put"); err != nil {
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
	err = s.Store.Write(ctx, spec)
	var stop errStop
	if err != nil && !errors.As(err, &stop) {
		return PutResult{}, err
	}
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

// --------------------------------------------------------------- move ----

// MoveOptions are the flags of `dk move`.
type MoveOptions struct {
	Note      string
	IfVersion *int // nil: not given
	Force     bool
	Kind      ticket.Kind
}

// MoveResult reports a stage change. From is the previous stage; when the
// ticket already was in the target stage nothing is committed.
type MoveResult struct {
	ID    string `json:"id"`
	Stage string `json:"stage"`
	From  string `json:"from"`
	Rev   int    `json:"rev"`
}

// Move changes the stage of a ticket in one commit: rev + 1, epoch
// unchanged, one log line. A claim holder keeps the claim; a forced move by
// an admin on someone else's live claim also clears the claim (like
// force-release followed by the move) and leaves the epoch alone.
func (s *Service) Move(ctx context.Context, id string, stage ticket.Stage, opts MoveOptions) (MoveResult, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	path, err := PathFor(id)
	if err != nil {
		return MoveResult{}, err
	}
	stage = ticket.Stage(strings.ToLower(strings.TrimSpace(string(stage))))
	if stage == "" {
		return MoveResult{}, errf(KindInvalid, "move needs --stage")
	}
	note := sanitize(strings.TrimSpace(opts.Note), MaxBody)
	if err := s.scanSecrets(ctx, note); err != nil {
		return MoveResult{}, err
	}
	cfg, cerr := s.config(ctx)
	if cerr != nil {
		return MoveResult{}, cerr
	}
	if !cfg.Stages.Valid(stage) {
		return MoveResult{}, errf(KindInvalid, "stage %q is not one of %s", string(stage), cfg.Stages.Names())
	}
	kind := opts.Kind
	if kind == "" {
		kind = ticket.KindAgent
	}

	var out MoveResult
	var mode WriteMode
	var prevOwner string
	spec := store.WriteSpec{
		Message: "dricket: move " + id,
		Check: func(h store.Head) error {
			fresh, _, err := loadTicket(h, path)
			if err != nil {
				return err
			}
			if mode, err = s.writeRights(cfg, fresh, kind, opts.Force, id, "move"); err != nil {
				return err
			}
			if opts.IfVersion != nil {
				if err := checkVersion(fresh, mode, opts.IfVersion, id, "move"); err != nil {
					return err
				}
			}
			if mode.Claimant && !cfg.IsAdmin(s.Owner) &&
				!ticket.CanTransitionIn(cfg.Stages, cfg.Transitions, roleOf(fresh.Kind), fresh.Stage, stage) && fresh.Stage != stage {
				return errf(KindInvalid, "%s may not move %s from %s to %s",
					roleOf(fresh.Kind), id, fresh.Stage, stage)
			}
			prevOwner = fresh.Owner
			out = MoveResult{ID: id, Stage: string(stage), From: string(fresh.Stage), Rev: fresh.Rev}
			if fresh.Stage == stage && !mode.Forced {
				return errStop{idempotent: true}
			}
			out.Rev = fresh.Rev + 1
			return nil
		},
		Mutate: func(w store.Worktree) error {
			tk, err := readWorktreeTicket(w, path)
			if err != nil {
				return err
			}
			now := s.Now()
			event := "move " + id + " " + string(tk.Stage) + " -> " + string(stage)
			if mode.Forced {
				tk.Owner = ""
				tk.ClaimedAt = time.Time{}
				tk.LeaseUntil = time.Time{}
				tk.Extensions = 0
				event += " (override of the claim of " + prevOwner + ")"
			}
			tk.Stage = stage
			tk.Rev++
			if note != "" {
				event += ": " + firstLine(note)
			}
			appendLog(&tk, s.Owner, event, now)
			if err := s.checkLint(tk, id); err != nil {
				return err
			}
			return w.WriteFile(path, tk.ToDoc().Render())
		},
	}
	err = s.Store.Write(ctx, spec)
	var stop errStop
	if err != nil && !errors.As(err, &stop) {
		return MoveResult{}, err
	}
	return out, nil
}
