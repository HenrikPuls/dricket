// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"strings"
	"testing"
	"time"
)

func TestParseRoundTrip(t *testing.T) {
	now := time.Date(2026, 11, 2, 10, 2, 11, 0, time.UTC)
	tk := newTicket("app-42", "7", now)
	parsed, err := parseTicket(tk.String())
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, k := range []string{fID, fOwner, fStage, fEpoch, fRev} {
		if tk.get(k) != parsed.get(k) {
			t.Errorf("field %s: got %q want %q", k, parsed.get(k), tk.get(k))
		}
	}
	if lastLogTag(parsed) != "add-app-42-0" {
		t.Errorf("lastLogTag = %q", lastLogTag(parsed))
	}
}

func TestParseDuplicateKey(t *testing.T) {
	in := "---\nid: app-1\nid: app-2\n---\nbody\n"
	if _, err := parseTicket(in); err == nil {
		t.Fatal("duplicate key accepted")
	}
}

func TestParseNoFrontMatter(t *testing.T) {
	if _, err := parseTicket("no front matter here"); err == nil {
		t.Fatal("missing front matter accepted")
	}
}

func mk(stage, owner string, epoch, rev int, lease time.Time) *ticket {
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	tk := newTicket("app-1", "7", now)
	tk.set(fStage, stage)
	tk.set(fOwner, owner)
	tk.seti(fEpoch, epoch)
	tk.seti(fRev, rev)
	if !lease.IsZero() {
		tk.stamp(fLeaseUntil, lease)
		tk.stamp(fClaimedAt, now)
	}
	return tk
}

func TestCheckSnapshotLegalTransitions(t *testing.T) {
	grace := 10 * time.Minute
	base := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		status byte
		prev   *ticket
		cur    *ticket
	}{
		{"claim free ticket", 'M',
			mk("open", "", 0, 0, time.Time{}),
			mk("in-progress", "1", 1, 1, base.Add(8*time.Hour))},
		{"update by owner", 'M',
			mk("in-progress", "1", 1, 1, base.Add(8*time.Hour)),
			mk("in-progress", "1", 1, 2, base.Add(8*time.Hour))},
		{"release", 'M',
			mk("in-progress", "1", 1, 2, base.Add(8*time.Hour)),
			mk("review", "", 1, 3, time.Time{})},
		{"takeover after expiry", 'M',
			mk("in-progress", "1", 1, 2, base), // lease long gone
			mk("in-progress", "2", 2, 3, base.Add(5*time.Hour))},
		{"re-claim of released ticket", 'M',
			mk("review", "", 1, 3, time.Time{}),
			mk("review", "2", 2, 4, time.Time{})},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// mk clears claimed_at when lease is zero; fix owner/lease pairs.
			if c.cur.get(fOwner) != "" && c.cur.get(fLeaseUntil) == "" {
				c.cur.stamp(fLeaseUntil, base.Add(8*time.Hour))
				c.cur.stamp(fClaimedAt, base)
			}
			if c.name == "takeover after expiry" {
				// The new owner claimed five hours after the old lease ended.
				c.cur.stamp(fClaimedAt, base.Add(5*time.Hour))
			}
			if v := checkSnapshot("p", c.status, c.prev, c.cur, grace); len(v) != 0 {
				t.Errorf("unexpected violations: %s", strings.Join(v, "; "))
			}
		})
	}
}

func TestCheckSnapshotViolations(t *testing.T) {
	grace := 10 * time.Minute
	base := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	fix := func(tk *ticket) *ticket {
		if tk.get(fOwner) != "" && tk.get(fLeaseUntil) == "" {
			tk.stamp(fLeaseUntil, base.Add(8*time.Hour))
			tk.stamp(fClaimedAt, base)
		}
		return tk
	}
	cases := []struct {
		name   string
		status byte
		prev   *ticket
		cur    *ticket
		want   string
	}{
		{
			"claim of owned ticket without expiry", 'M',
			fix(mk("in-progress", "1", 1, 1, base.Add(8*time.Hour))),
			fix(mk("in-progress", "2", 2, 2, base.Add(time.Minute))),
			"takeover before lease expiry",
		},
		{
			"rev jump", 'M',
			fix(mk("in-progress", "1", 1, 1, base.Add(8*time.Hour))),
			fix(mk("in-progress", "1", 1, 5, base.Add(8*time.Hour))),
			"expected +1",
		},
		{
			"identity change", 'M',
			fix(mk("in-progress", "1", 1, 1, base.Add(8*time.Hour))),
			fix(mk("in-progress", "1", 1, 2, base.Add(8*time.Hour))),
			"", // patched below
		},
		{
			"epoch change on update", 'M',
			fix(mk("in-progress", "1", 1, 1, base.Add(8*time.Hour))),
			fix(mk("in-progress", "1", 2, 2, base.Add(8*time.Hour))),
			"epoch changed on an update",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.name == "identity change" {
				c.cur.set(fCreatedBy, "someone-else")
				c.want = "identity field created_by changed"
			}
			v := checkSnapshot("p", c.status, c.prev, c.cur, grace)
			found := false
			for _, s := range v {
				if strings.Contains(s, c.want) {
					found = true
				}
			}
			if !found {
				t.Errorf("want violation %q, got %v", c.want, v)
			}
		})
	}
}

func TestTagSetAndLastLogTag(t *testing.T) {
	now := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	tk := newTicket("app-5", "3", now)
	tk.seti(fRev, 1)
	tk.appendLogEvent("3", "claim", now)
	tk.seti(fRev, 2)
	tk.appendLogEvent("3", "update", now)
	set := tagSet(tk)
	for _, want := range []string{"add-app-5-0", "claim-app-5-1", "update-app-5-2"} {
		if !set[want] {
			t.Errorf("tag %s missing from %v", want, set)
		}
	}
	if lastLogTag(tk) != "update-app-5-2" {
		t.Errorf("lastLogTag = %q", lastLogTag(tk))
	}
}
