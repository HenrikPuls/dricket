// SPDX-License-Identifier: GPL-3.0-or-later
package config

import (
	"testing"
	"time"

	"github.com/lariscus/dricket/internal/ticket"
)

func TestDefaults(t *testing.T) {
	c := Defaults()
	if c.LeaseS != 2*time.Hour || c.LeaseM != 8*time.Hour || c.LeaseL != 24*time.Hour {
		t.Errorf("default leases = %v/%v/%v", c.LeaseS, c.LeaseM, c.LeaseL)
	}
	if c.MaxExtensions != 3 || c.QuotaTicketsPerDay != 50 || !c.SecretScan {
		t.Errorf("defaults = %+v", c)
	}
	if d, err := c.Lease(ticket.SizeS); err != nil || d != 2*time.Hour {
		t.Errorf("Lease(S) = %v, %v", d, err)
	}
	// Clamping: a tiny configured size rises to the floor, a giant one
	// caps at the ceiling.
	if d, _ := c.Lease(ticket.SizeM); d != 8*time.Hour {
		t.Errorf("Lease(M) = %v", d)
	}
}

func TestLeaseClamp(t *testing.T) {
	c, err := Parse([]byte(`
[limits]
lease_min = "1h"
lease_max = "10h"
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d, _ := c.Lease(ticket.SizeS); d != 2*time.Hour {
		t.Errorf("Lease(S) = %v, want 2h (unchanged)", d)
	}
	tiny := *c
	tiny.LeaseS = 30 * time.Minute
	if d, _ := tiny.Lease(ticket.SizeS); d != time.Hour {
		t.Errorf("Lease(S) with floor = %v, want 1h", d)
	}
	big := *c
	big.LeaseL = 100 * time.Hour
	if d, _ := big.Lease(ticket.SizeL); d != 10*time.Hour {
		t.Errorf("Lease(L) with cap = %v, want 10h", d)
	}
}

func TestParseFull(t *testing.T) {
	c, err := Parse([]byte(`
schema_version = 1
min_version = "0.2.0"

[limits]
lease_l = "48h"
max_extensions = 5

[security]
admins = ["henrik", "alice"]
quota_tickets_per_day = 10
secret_scan = false

[transitions.agent]
review = ["in-progress", "blocked"]
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.MinVersion != "0.2.0" || c.LeaseL != 48*time.Hour || c.MaxExtensions != 5 {
		t.Errorf("parsed = %+v", c)
	}
	if len(c.Admins) != 2 || c.QuotaTicketsPerDay != 10 || c.SecretScan {
		t.Errorf("security = %+v", c)
	}
	// The review stage is overridden, everything else keeps defaults.
	if got := c.Transitions["agent"]["review"]; len(got) != 2 {
		t.Errorf("transitions.agent.review = %v", got)
	}
	if got := c.Transitions["agent"]["open"]; len(got) != 3 {
		t.Errorf("transitions.agent.open = %v (want defaults)", got)
	}
	// human is fixed and always allowed.
	if !ticket.CanTransitionTable(c.Transitions, ticket.RoleHuman, ticket.StageReview, ticket.StageDone) {
		t.Error("human must always be allowed")
	}
	// But review -> blocked is the new allowance beyond the default.
	if !ticket.CanTransitionTable(c.Transitions, ticket.RoleAgent, ticket.StageReview, ticket.StageBlocked) {
		t.Error("agent review -> blocked must be allowed by the override")
	}
	if ticket.CanTransitionTable(c.Transitions, ticket.RoleAgent, ticket.StageReview, ticket.StageDone) {
		t.Error("agent review -> done must stay forbidden")
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		toml string
		want string
	}{
		{"unknown key", `what_is_this = 1`, "unknown key"},
		{"bad schema", `schema_version = 2`, "schema_version 2"},
		{"bad duration", "[limits]\nlease_m = \"soon\"", "positive duration"},
		{"negative extensions", "[limits]\nmax_extensions = -1", "must not be negative"},
		{"negative quota", "[security]\nquota_tickets_per_day = -5", "must not be negative"},
		{"bad version", `min_version = "v1.2"`, "dotted integers"},
		{"human transitions", "[transitions.human]\nreview = [\"done\"]", "fixed by the concept"},
		{"bad role", "[transitions.robot]\nopen = [\"review\"]", "unknown role"},
		{"bad stage", "[transitions.agent]\nparked = [\"review\"]", "unknown stage"},
		{"bad target", "[transitions.agent]\nopen = [\"filed\"]", "unknown target stage"},
		{"syntax", "[limits\nlease_m = \"2h\"", "line"},
	}
	for _, tc := range cases {
		if _, err := Parse([]byte(tc.toml)); err == nil {
			t.Errorf("%s: Parse accepted %q", tc.name, tc.toml)
		} else if !contains(err.Error(), tc.want) {
			t.Errorf("%s: error = %q, want it to contain %q", tc.name, err.Error(), tc.want)
		}
	}
}

func TestParseMissingIsDefaults(t *testing.T) {
	c, err := Parse([]byte(""))
	if err != nil {
		t.Fatalf("parse empty: %v", err)
	}
	d := Defaults()
	if c.LeaseM != d.LeaseM || c.MaxExtensions != d.MaxExtensions ||
		c.QuotaTicketsPerDay != d.QuotaTicketsPerDay || !c.SecretScan {
		t.Errorf("empty config = %+v, want defaults", c)
	}
}

func TestIsAdminFailsClosed(t *testing.T) {
	c := Defaults()
	if c.IsAdmin("anyone") {
		t.Error("empty admins list must fail closed")
	}
	c.Admins = []string{"henrik"}
	if !c.IsAdmin("henrik") || c.IsAdmin("Henrik") || c.IsAdmin("henrik2") {
		t.Error("IsAdmin matching wrong")
	}
}

func TestCompareVersion(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"0.2.0", "0.2.0", 0},
		{"0.2", "0.2.0", 0},
		{"0.1.9", "0.2.0", -1},
		{"0.10.0", "0.9.0", 1},
		{"1.0", "0.9.9", 1},
	}
	for _, tc := range cases {
		got, err := CompareVersion(tc.a, tc.b)
		if err != nil {
			t.Fatalf("CompareVersion(%s, %s): %v", tc.a, tc.b, err)
		}
		if got != tc.want {
			t.Errorf("CompareVersion(%s, %s) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
	if _, err := CompareVersion("dev", "1.0"); err == nil {
		t.Error("CompareVersion must reject non-numeric versions")
	}
}

func TestParsePriorities(t *testing.T) {
	// Defaults: the starting set critical, high, normal, low.
	c := Defaults()
	if !containsSlice(c.PriorityValues, []string{"critical", "high", "normal", "low"}) ||
		c.PriorityDefault != "normal" {
		t.Errorf("default priorities = %v, default %q", c.PriorityValues, c.PriorityDefault)
	}

	c, err := Parse([]byte(`
[priorities]
values = ["urgent", "normal"]
default = "urgent"
`))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !containsSlice(c.PriorityValues, []string{"urgent", "normal"}) || c.PriorityDefault != "urgent" {
		t.Errorf("priorities = %v, default %q", c.PriorityValues, c.PriorityDefault)
	}
	if err := c.CheckPriority("urgent"); err != nil {
		t.Errorf("CheckPriority(urgent) = %v", err)
	}
	if err := c.CheckPriority("high"); err == nil {
		t.Error("CheckPriority(high) must fail outside the configured set")
	}
	// Without an explicit default, normal wins when present.
	c, _ = Parse([]byte("[priorities]\nvalues = [\"low\", \"normal\"]\n"))
	if c.PriorityDefault != "normal" {
		t.Errorf("implicit default = %q, want normal", c.PriorityDefault)
	}
	c, _ = Parse([]byte("[priorities]\nvalues = [\"p1\", \"p2\"]\n"))
	if c.PriorityDefault != "p1" {
		t.Errorf("implicit default = %q, want the first value", c.PriorityDefault)
	}

	cases := []struct {
		name string
		toml string
		want string
	}{
		{"empty values", "[priorities]\nvalues = []\n", "must not be empty"},
		{"default without values", "[priorities]\ndefault = \"high\"\n", "without priorities.values"},
		{"default not in values", "[priorities]\nvalues = [\"low\"]\ndefault = \"high\"\n", "not in priorities.values"},
		{"uppercase", "[priorities]\nvalues = [\"High\"]\n", "lower-case"},
		{"duplicate", "[priorities]\nvalues = [\"low\", \"low\"]\n", "twice"},
		{"bad word", "[priorities]\nvalues = [\"very_high\"]\n", "lower-case word"},
	}
	for _, tc := range cases {
		if _, err := Parse([]byte(tc.toml)); err == nil || !contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want it to contain %q", tc.name, err, tc.want)
		}
	}
}

func containsSlice(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range want {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
