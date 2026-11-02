// SPDX-License-Identifier: GPL-3.0-or-later
// Package main implements the dricket concurrency spike (R-1).
//
// The ticket file is the spike's minimal version of a dricket ticket: a flat
// front-matter block (key: value, one line per field) and a body with an
// append-only log. Parsing and formatting round-trip deterministically.
package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Front-matter field names, as in docs/CONCEPT.md.
const (
	fID         = "id"
	fCreatedAt  = "created_at"
	fCreatedBy  = "created_by"
	fSchema     = "schema_version"
	fTitle      = "title"
	fStage      = "stage"
	fOwner      = "owner"
	fKind       = "kind"
	fSize       = "size"
	fClaimedAt  = "claimed_at"
	fLeaseUntil = "lease_until"
	fEpoch      = "epoch"
	fRev        = "rev"
	fExtensions = "extensions"
)

// identityFields must never change after creation (invariant 4).
var identityFields = []string{fID, fCreatedAt, fCreatedBy, fSchema}

// ticket keeps the front matter in insertion order so formatting is stable.
type ticket struct {
	order []string
	vals  map[string]string
	body  string
}

// parseTicket reads the first --- block as front matter; everything after the
// closing --- is the body. Duplicate keys are an error.
func parseTicket(s string) (*ticket, error) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, fmt.Errorf("line 1: expected opening '---'")
	}
	t := &ticket{vals: map[string]string{}}
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
		key, val, ok := strings.Cut(lines[i], ":")
		if !ok {
			return nil, fmt.Errorf("line %d: expected 'key: value'", i+1)
		}
		key = strings.TrimSpace(key)
		if key == "" {
			return nil, fmt.Errorf("line %d: empty key", i+1)
		}
		if _, dup := t.vals[key]; dup {
			return nil, fmt.Errorf("line %d: duplicate key %q", i+1, key)
		}
		t.vals[key] = strings.TrimSpace(val)
		t.order = append(t.order, key)
	}
	if end < 0 {
		return nil, fmt.Errorf("unterminated front matter")
	}
	t.body = strings.Join(lines[end+1:], "\n")
	return t, nil
}

func (t *ticket) String() string {
	var b strings.Builder
	b.WriteString("---\n")
	for _, k := range t.order {
		fmt.Fprintf(&b, "%s: %s\n", k, t.vals[k])
	}
	b.WriteString("---\n")
	b.WriteString(t.body)
	return b.String()
}

func (t *ticket) get(k string) string { return t.vals[k] }

func (t *ticket) set(k, v string) {
	if _, ok := t.vals[k]; !ok {
		t.order = append(t.order, k)
	}
	t.vals[k] = v
}

func (t *ticket) geti(k string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(t.vals[k]))
	return n
}

func (t *ticket) seti(k string, n int) { t.set(k, strconv.Itoa(n)) }

func (t *ticket) stamp(k string, ts time.Time) {
	t.set(k, ts.UTC().Format(time.RFC3339))
}

// time returns the parsed value of a timestamp field; ok is false when the
// field is empty or not a timestamp.
func (t *ticket) time(k string) (time.Time, bool) {
	v := strings.TrimSpace(t.vals[k])
	if v == "" {
		return time.Time{}, false
	}
	tt, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, false
	}
	return tt, true
}

func (t *ticket) appendLog(line string) {
	t.body = strings.TrimRight(t.body, "\n") + "\n" + line + "\n"
}

// appendLogEvent adds a log line with attribution and a unique tag. The tag
// encodes op, ticket id and rev, so the verifier can prove that every
// accepted push is still present in the final state (invariant 3).
func (t *ticket) appendLogEvent(actor, op string, now time.Time) {
	tag := op + "-" + t.get(fID) + "-" + t.get(fRev)
	t.appendLog(fmt.Sprintf("- %s %s %s %s", now.UTC().Format(time.RFC3339), actor, op, tag))
}

// lastLogTag returns the tag of the most recent log line.
func lastLogTag(t *ticket) string {
	lines := strings.Split(strings.TrimRight(t.body, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimRight(lines[i], "\r")
		if !strings.HasPrefix(l, "- ") {
			continue
		}
		fields := strings.Fields(l)
		if len(fields) > 0 {
			return fields[len(fields)-1]
		}
	}
	return ""
}

// tagSet collects all log tags of a ticket.
func tagSet(t *ticket) map[string]bool {
	set := map[string]bool{}
	for _, l := range strings.Split(t.body, "\n") {
		if !strings.HasPrefix(strings.TrimRight(l, "\r"), "- ") {
			continue
		}
		fields := strings.Fields(l)
		if len(fields) > 0 {
			set[fields[len(fields)-1]] = true
		}
	}
	return set
}

// newTicket creates an unclaimed ticket, as `dk add --unclaimed` would.
func newTicket(id, creator string, now time.Time) *ticket {
	t := &ticket{vals: map[string]string{}}
	t.set(fID, id)
	t.stamp(fCreatedAt, now)
	t.set(fCreatedBy, creator)
	t.set(fSchema, "1")
	t.set(fTitle, "spike ticket "+id)
	t.set(fStage, "open")
	t.set(fOwner, "")
	t.set(fKind, "agent")
	t.set(fSize, "M")
	t.set(fClaimedAt, "")
	t.set(fLeaseUntil, "")
	t.seti(fEpoch, 0)
	t.seti(fRev, 0)
	t.seti(fExtensions, 0)
	t.body = "\n## Description\n\nCreated by the concurrency spike.\n\n## Log\n"
	t.appendLogEvent(creator, "add", now)
	return t
}

// checkSnapshot validates one commit's effect on one ticket: prev is the
// snapshot at the parent commit (nil at creation, status 'A'). It returns
// every violation found; an empty slice means the transition is legal.
//
// This encodes the ownership protocol of docs/CONCEPT.md:
//   - identity fields never change,
//   - rev increases by exactly one per write,
//   - the epoch never decreases and rises on every ownership change,
//   - a claim requires a free ticket, the same owner (idempotent) or an
//     expired lease (takeover, lease_until + grace <= claimed_at),
//   - owner, claimed_at and lease_until are set and cleared together.
func checkSnapshot(path string, status byte, prev, cur *ticket, grace time.Duration) []string {
	var v []string
	add := func(format string, a ...any) {
		v = append(v, fmt.Sprintf(format, a...))
	}
	consistency := func(label string, t *ticket) {
		owner := t.get(fOwner) != ""
		lease := t.get(fLeaseUntil) != ""
		claimed := t.get(fClaimedAt) != ""
		if owner != lease || owner != claimed {
			add("%s: %s owner/claimed_at/lease_until inconsistent (owner=%t claimed=%t lease=%t)",
				path, label, owner, claimed, lease)
		}
	}
	if status == 'A' {
		if prev != nil {
			add("%s: creation with a previous snapshot", path)
		}
		if cur.get(fOwner) != "" {
			add("%s: created with an owner", path)
		}
		if cur.geti(fEpoch) != 0 || cur.geti(fRev) != 0 {
			add("%s: created with epoch or rev set", path)
		}
		consistency("created", cur)
		return v
	}
	if prev == nil {
		add("%s: modification without a previous snapshot", path)
		return v
	}
	for _, f := range identityFields {
		if prev.get(f) != cur.get(f) {
			add("%s: identity field %s changed from %q to %q", path, f, prev.get(f), cur.get(f))
		}
	}
	if cur.geti(fRev) != prev.geti(fRev)+1 {
		add("%s: rev %d -> %d, expected +1", path, prev.geti(fRev), cur.geti(fRev))
	}
	if cur.geti(fEpoch) < prev.geti(fEpoch) {
		add("%s: epoch decreased from %d to %d", path, prev.geti(fEpoch), cur.geti(fEpoch))
	}
	po, co := prev.get(fOwner), cur.get(fOwner)
	consistency("before", prev)
	consistency("after", cur)
	switch {
	case po == "" && co == "":
		// Free-field change while unowned: legal in the concept, not
		// produced by the spike.
	case po == "" && co != "": // claim
		if cur.geti(fEpoch) != prev.geti(fEpoch)+1 {
			add("%s: claim did not raise the epoch", path)
		}
	case po != "" && co == "": // release
		// no additional rules
	case po == co: // update by the owner
		if cur.geti(fEpoch) != prev.geti(fEpoch) {
			add("%s: epoch changed on an update", path)
		}
	default: // takeover: po != co, both set
		lu, okLU := prev.time(fLeaseUntil)
		ca, okCA := cur.time(fClaimedAt)
		if !okLU || !okCA {
			add("%s: takeover without timestamps", path)
			break
		}
		if lu.Add(grace).After(ca) {
			add("%s: takeover before lease expiry: lease_until=%s grace=%s claimed_at=%s",
				path, lu.Format(time.RFC3339), grace, ca.Format(time.RFC3339))
		}
		if cur.geti(fEpoch) != prev.geti(fEpoch)+1 {
			add("%s: takeover did not raise the epoch", path)
		}
	}
	return v
}
