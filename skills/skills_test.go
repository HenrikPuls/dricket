// SPDX-License-Identifier: GPL-3.0-or-later
package skills

import (
	"strings"
	"testing"
)

func TestDricketSkillContract(t *testing.T) {
	for _, want := range []string{
		"name: dricket",
		"description: Use when",
		"ticket checkout",
		"DK_OWNER",
		"dk next",
		"dk claim",
		"dk sync",
		"dk commit",
		"dk release",
		"exit 12",
		"exit 13",
		"outcome-unknown",
		"dk guide",
	} {
		if !strings.Contains(strings.ToLower(Dricket), strings.ToLower(want)) {
			t.Errorf("skill misses %q", want)
		}
	}
	for _, removed := range []string{"add -c", "dk update", "--note", "--result", "--reason"} {
		if strings.Contains(Dricket, removed) {
			t.Errorf("skill contains removed CLI spelling %q", removed)
		}
	}
}
