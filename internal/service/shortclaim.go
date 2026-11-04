// SPDX-License-Identifier: GPL-3.0-or-later

package service

import "github.com/HenrikPuls/dricket/internal/ticket"

// shortClaimMark annotates the log line of a write under --claim: the
// claim and its release share the one commit, so the line's timestamp is
// both; no lease clock is ever written (ADR-0012).
const shortClaimMark = " (short claim)"

// checkShortClaim applies the rules of a short claim (ADR-0012): the
// ticket must be unclaimed, because the claim is taken and released
// inside the one commit. A foreign claim is refused exactly like before
// (exit 11); the own claim needs no short claim; --force is the
// exception path and excludes --claim.
func (s *Service) checkShortClaim(fresh ticket.Ticket, force bool, id string) error {
	if force {
		return errf(KindInvalid, "put: --claim and --force exclude each other; --claim is the path without an exception")
	}
	switch fresh.Owner {
	case "":
		return nil
	case s.Owner:
		return errf(KindInvalid, "put: you already hold the claim on %s; --claim is for unclaimed tickets", id)
	default:
		return errf(KindClaimed, "ticket %s is claimed by %s (lease until %s)",
			id, fresh.Owner, stamp(fresh.LeaseUntil))
	}
}
