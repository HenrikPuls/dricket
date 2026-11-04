// SPDX-License-Identifier: GPL-3.0-or-later

package ticket

// ValidateTransition checks the successor relation between two ticket
// snapshots. Content and workflow edits are allowed; identity and claim
// protocol fields must remain coherent.
func ValidateTransition(previous, current Ticket) []Finding {
	return ValidateTransitionWith(previous, current, DefaultStageSet(), true)
}

// ValidateTransitionWith is ValidateTransition under a store's stage
// vocabulary; see LintWith for checkVocab.
func ValidateTransitionWith(previous, current Ticket, set StageSet, checkVocab bool) []Finding {
	var findings []Finding
	if current.ID != previous.ID || !current.CreatedAt.Equal(previous.CreatedAt) ||
		current.CreatedBy != previous.CreatedBy || current.SchemaVersion != previous.SchemaVersion {
		findings = append(findings, Finding{Field: "identity", Message: "identity fields are immutable"})
	}
	if current.Rev != previous.Rev+1 {
		findings = append(findings, Finding{Field: "rev", Message: "rev must increase by exactly one"})
	}

	switch {
	case current.Epoch < previous.Epoch:
		findings = append(findings, Finding{Field: "epoch", Message: "epoch must never decrease"})
	case previous.Owner == current.Owner:
		if current.Epoch != previous.Epoch {
			findings = append(findings, Finding{Field: "epoch", Message: "epoch must stay unchanged while the owner stays"})
		}
	case current.Owner == "":
		if current.Epoch != previous.Epoch {
			findings = append(findings, Finding{Field: "epoch", Message: "release must not change epoch"})
		}
	default:
		if current.Epoch != previous.Epoch+1 {
			findings = append(findings, Finding{Field: "epoch", Message: "claim or takeover must increase epoch by exactly one"})
		}
	}
	return append(findings, LintWith(current, set, checkVocab)...)
}
