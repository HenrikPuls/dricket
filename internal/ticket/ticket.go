// SPDX-License-Identifier: GPL-3.0-or-later

// Package ticket holds the ticket data model of docs/CONCEPT.md: required
// fields, enums, state consistency and structural transitions. Field classes
// guide targeted commands; they are not repository authorization. The package
// works on parsed front-matter documents and never touches Git.
package ticket

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/HenrikPuls/dricket/internal/frontmatter"
)

// SchemaVersion is the on-disk format version this tool reads and writes.
const SchemaVersion = 1

// Class is one of the field classes of the concept (three from the concept
// plus the extension namespace).
type Class int

// Field classes classify targeted mutations; whole-ticket edits still pass
// through structural transition validation.
const (
	Identity  Class = iota // immutable after creation
	State                  // coordination state with transition invariants
	Free                   // editable content or workflow metadata
	Extension              // x-<name>: opaque consumer metadata, preserved on every write
)

func (c Class) String() string {
	switch c {
	case Identity:
		return "identity"
	case State:
		return "state"
	case Extension:
		return "extension"
	default:
		return "free"
	}
}

var (
	identityFields = map[string]bool{
		"id": true, "created_at": true, "created_by": true, "schema_version": true,
	}
	stateFields = map[string]bool{
		"stage": true, "owner": true, "kind": true, "claimed_at": true,
		"lease_until": true, "size": true, "epoch": true, "rev": true,
		"extensions": true,
	}
	freeFields = map[string]bool{
		"title": true, "labels": true, "depends": true, "parent": true,
		"touches": true, "branch": true, "repos": true, "priority": true,
	}
)

// ClassOf reports the class of a field; ok is false for unknown keys.
func ClassOf(field string) (Class, bool) {
	switch {
	case identityFields[field]:
		return Identity, true
	case stateFields[field]:
		return State, true
	case freeFields[field]:
		return Free, true
	case ValidExtensionKey(field):
		return Extension, true
	}
	return Free, false
}

// Stage is the life-cycle state of a ticket; the path never depends on it.
type Stage string

// Stages from docs/CONCEPT.md; the set becomes configurable in stage v2.
const (
	StageOpen       Stage = "open"
	StageInProgress Stage = "in-progress"
	StageReview     Stage = "review"
	StageDone       Stage = "done"
	StageBlocked    Stage = "blocked"
	StageNeedsInput Stage = "needs-input"
	StageCancelled  Stage = "cancelled"
)

var stages = map[Stage]bool{
	StageOpen: true, StageInProgress: true, StageReview: true, StageDone: true,
	StageBlocked: true, StageNeedsInput: true, StageCancelled: true,
}

// Role says what an actor may do; the role comes from `kind` in the ticket
// and is enforced only on writes through the tool (we detect, we do not
// prevent).
type Role string

// Roles from docs/CONCEPT.md.
const (
	RoleAgent Role = "agent"
	RoleHuman Role = "human"
	RoleAdmin Role = "admin"
)

// Size is the lease size; language models estimate durations badly, so a
// claim carries a size instead of a number.
type Size string

// Sizes from docs/CONCEPT.md; starting leases are configuration values.
const (
	SizeS Size = "S"
	SizeM Size = "M"
	SizeL Size = "L"
)

var sizes = map[Size]bool{SizeS: true, SizeM: true, SizeL: true}

// Kind distinguishes agent work from human work on a ticket.
type Kind string

// Kinds stored in the `kind` field.
const (
	KindAgent Kind = "agent"
	KindHuman Kind = "human"
)

var kinds = map[Kind]bool{KindAgent: true, KindHuman: true}

// idPattern is PREFIX-N in lower case. Storage is lower case; comparison is
// case-insensitive, so callers normalise user input before storing it
// (docs/CONCEPT.md, "IDs and paths").
var idPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*-[0-9]+$`)

// ValidID reports whether s is a well-formed, lower-case ticket id as it
// must appear in storage.
func ValidID(s string) bool {
	return idPattern.MatchString(s)
}

// prefixPattern is the prefix part of an id: lower case, starts with a
// letter, dashes allowed between segments.
var prefixPattern = regexp.MustCompile(`^[a-z][a-z0-9]*(-[a-z0-9]+)*$`)

// ValidPrefix reports whether s is a well-formed project prefix.
func ValidPrefix(s string) bool {
	return prefixPattern.MatchString(s)
}

// Ticket is the typed view of one ticket file.
type Ticket struct {
	ID            string
	CreatedAt     time.Time
	CreatedBy     string
	SchemaVersion int
	Title         string
	Stage         Stage
	Owner         string // "" when unclaimed
	Kind          Kind
	Size          Size
	ClaimedAt     time.Time           // zero when unclaimed
	LeaseUntil    time.Time           // zero when unclaimed
	Epoch         int                 // fencing token, rises on every claim or takeover
	Rev           int                 // rises with every write to the ticket
	Extensions    int                 // used extends of the claim
	Labels        []string            // free
	Depends       []string            // free, ticket ids
	Parent        string              // free, ticket id
	Touches       []string            // free
	Branch        string              // free, git ref, validated with git check-ref-format later
	Repos         []string            // free
	Priority      string              // free, lower-case value from dricket.toml [priorities]
	Body          string              // everything after the closing ---
	Extra         []frontmatter.Field // unknown fields preserved in their original order
}

// FromDoc converts a parsed document into a Ticket. It returns the unknown
// keys (a warning, so newer tools do not break older repos) and errors for
// values that do not fit their type.
func FromDoc(doc frontmatter.Document) (t Ticket, unknown []string, err error) {
	for _, f := range doc.Fields {
		cls, known := ClassOf(f.Key)
		if !known {
			unknown = append(unknown, f.Key)
		}
		if !known || cls == Extension {
			t.Extra = append(t.Extra, f)
		}
	}
	get := func(key string) (string, bool) { return doc.Get(key) }

	if v, ok := get("id"); ok {
		t.ID = v
	}
	if v, ok := get("created_by"); ok {
		t.CreatedBy = v
	}
	if v, ok := get("title"); ok {
		t.Title = v
	}
	if v, ok := get("stage"); ok {
		t.Stage = Stage(v)
	}
	if v, ok := get("owner"); ok {
		t.Owner = v
	}
	if v, ok := get("kind"); ok {
		t.Kind = Kind(v)
	}
	if v, ok := get("size"); ok {
		t.Size = Size(v)
	}
	if v, ok := get("parent"); ok {
		t.Parent = v
	}
	if v, ok := get("branch"); ok {
		t.Branch = v
	}
	if v, ok := get("priority"); ok {
		t.Priority = v
	}
	t.Labels = splitList(getOr(doc, "labels"))
	t.Depends = splitList(getOr(doc, "depends"))
	t.Touches = splitList(getOr(doc, "touches"))
	t.Repos = splitList(getOr(doc, "repos"))
	t.Body = doc.Body

	if v, ok := get("schema_version"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return t, unknown, fmt.Errorf("schema_version: expected an integer, got %q", v)
		}
		t.SchemaVersion = n
	}
	if v, ok := get("epoch"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return t, unknown, fmt.Errorf("epoch: expected an integer, got %q", v)
		}
		t.Epoch = n
	}
	if v, ok := get("rev"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return t, unknown, fmt.Errorf("rev: expected an integer, got %q", v)
		}
		t.Rev = n
	}
	if v, ok := get("extensions"); ok {
		n, err := strconv.Atoi(v)
		if err != nil {
			return t, unknown, fmt.Errorf("extensions: expected an integer, got %q", v)
		}
		t.Extensions = n
	}
	var perr error
	t.CreatedAt, perr = timeField(doc, "created_at")
	if perr != nil {
		return t, unknown, perr
	}
	t.ClaimedAt, perr = timeField(doc, "claimed_at")
	if perr != nil {
		return t, unknown, perr
	}
	t.LeaseUntil, perr = timeField(doc, "lease_until")
	if perr != nil {
		return t, unknown, perr
	}
	return t, unknown, nil
}

func getOr(doc frontmatter.Document, key string) string {
	v, _ := doc.Get(key)
	return v
}

// splitList splits a comma-separated free-field value; empty items and
// surrounding spaces are dropped, items keep their case (labels are free
// text, ids are normalised where the field requires ids).
func splitList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func timeField(doc frontmatter.Document, key string) (time.Time, error) {
	v, ok := doc.Get(key)
	if !ok || strings.TrimSpace(v) == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(v))
	if err != nil {
		return time.Time{}, fmt.Errorf("%s: expected an RFC 3339 timestamp, got %q", key, v)
	}
	return t, nil
}

// ToDoc renders the ticket in the canonical field order of docs/CONCEPT.md.
func (t Ticket) ToDoc() frontmatter.Document {
	fields := []frontmatter.Field{
		{Key: "id", Value: t.ID},
		{Key: "created_at", Value: stamp(t.CreatedAt)},
		{Key: "created_by", Value: t.CreatedBy},
		{Key: "schema_version", Value: strconv.Itoa(firstNonZero(t.SchemaVersion, SchemaVersion))},
		{Key: "title", Value: t.Title},
		{Key: "stage", Value: string(t.Stage)},
		{Key: "owner", Value: t.Owner},
		{Key: "kind", Value: string(t.Kind)},
		{Key: "size", Value: string(t.Size)},
		{Key: "claimed_at", Value: stamp(t.ClaimedAt)},
		{Key: "lease_until", Value: stamp(t.LeaseUntil)},
		{Key: "epoch", Value: strconv.Itoa(t.Epoch)},
		{Key: "rev", Value: strconv.Itoa(t.Rev)},
		{Key: "extensions", Value: strconv.Itoa(t.Extensions)},
		{Key: "priority", Value: t.Priority},
		{Key: "labels", Value: strings.Join(t.Labels, ", ")},
		{Key: "depends", Value: strings.Join(t.Depends, ", ")},
		{Key: "parent", Value: t.Parent},
		{Key: "touches", Value: strings.Join(t.Touches, ", ")},
		{Key: "branch", Value: t.Branch},
		{Key: "repos", Value: strings.Join(t.Repos, ", ")},
	}
	for _, field := range t.Extra {
		// Only unknown keys and extensions live in Extra; a known key
		// there would duplicate a typed field.
		if cls, known := ClassOf(field.Key); !known || cls == Extension {
			fields = append(fields, frontmatter.Field{Key: field.Key, Value: field.Value})
		}
	}
	return frontmatter.Document{Fields: fields, Body: t.Body}
}

func stamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func firstNonZero(a, b int) int {
	if a != 0 {
		return a
	}
	return b
}

// Finding is one lint result. Warnings do not fail a commit; errors do
// (exit 14 in the CLI).
type Finding struct {
	Field   string
	Message string
	Warning bool
}

func (f Finding) String() string {
	kind := "error"
	if f.Warning {
		kind = "warning"
	}
	if f.Field == "" {
		return kind + ": " + f.Message
	}
	return kind + " " + f.Field + ": " + f.Message
}

// Lint checks a single ticket file, the level that runs before every tool
// commit: parseability is assumed (the caller parsed the document),
// identity shape, required fields, enums and state consistency. Reference
// checks (depends, parent exist) belong to validate and need the store.
func Lint(t Ticket) []Finding {
	return LintWith(t, DefaultStageSet(), true)
}

// LintWith is Lint under a store's stage vocabulary. checkVocab false skips
// the vocabulary checks (membership, and for custom sets the claim-stage
// rule); history replay uses it so that a later edit of [stages] does not
// invalidate old commits.
func LintWith(t Ticket, set StageSet, checkVocab bool) []Finding {
	var fs []Finding
	add := func(field, msg string) { fs = append(fs, Finding{Field: field, Message: msg}) }
	owned := t.Owner != ""

	if t.SchemaVersion == 0 {
		add("schema_version", "required")
	} else if t.SchemaVersion > SchemaVersion {
		add("schema_version", fmt.Sprintf("unknown schema version %d, this tool supports %d", t.SchemaVersion, SchemaVersion))
	}
	if t.ID == "" {
		add("id", "required")
	} else if !ValidID(t.ID) {
		add("id", fmt.Sprintf("%q is not of the form prefix-number, lower case", t.ID))
	}
	if t.CreatedAt.IsZero() {
		add("created_at", "required")
	}
	if t.CreatedBy == "" {
		add("created_by", "required")
	}
	if t.Title == "" {
		add("title", "required")
	}
	if checkVocab && !set.Valid(t.Stage) {
		add("stage", fmt.Sprintf("%q is not one of %s", string(t.Stage), set.Names()))
	}
	if !kinds[t.Kind] {
		add("kind", fmt.Sprintf("%q is not one of agent, human", string(t.Kind)))
	}
	if owned {
		if !sizes[t.Size] {
			add("size", fmt.Sprintf("%q is not one of S, M, L", string(t.Size)))
		}
		if t.ClaimedAt.IsZero() {
			add("claimed_at", "required while the ticket is claimed")
		}
		if t.LeaseUntil.IsZero() {
			add("lease_until", "required while the ticket is claimed")
		}
		if t.Epoch < 1 {
			add("epoch", "must be at least 1 while the ticket is claimed")
		}
	} else {
		if !t.ClaimedAt.IsZero() {
			add("claimed_at", "set while the ticket is not claimed")
		}
		if !t.LeaseUntil.IsZero() {
			add("lease_until", "set while the ticket is not claimed")
		}
		// The epoch is a fencing token: it rises on every claim and stays
		// after a release; it is never reset.
		if t.Extensions != 0 {
			add("extensions", "must be 0 while the ticket is not claimed")
		}
	}
	if t.Rev < 0 {
		add("rev", "must not be negative")
	}
	if set.Legacy {
		if t.Stage == StageInProgress && !owned {
			add("stage", "in-progress while the ticket is not claimed")
		}
	} else if checkVocab && owned && !set.InClaim(t.Stage) {
		add("stage", fmt.Sprintf("%q while the ticket is claimed; a claim is only valid in %s", string(t.Stage), joinStages(set.Claim)))
	}
	for _, f := range t.Extra {
		if cls, _ := ClassOf(f.Key); cls == Extension {
			if err := CheckExtensionValue(f.Key, f.Value); err != nil {
				add(f.Key, err.Error())
			}
		}
	}
	for _, id := range append([]string{t.Parent}, t.Depends...) {
		if id == "" {
			continue
		}
		if !ValidID(id) {
			add("depends", fmt.Sprintf("%q is not a ticket id", id))
		}
	}
	return fs
}

func joinStages(list []Stage) string {
	names := make([]string, len(list))
	for i, v := range list {
		names[i] = string(v)
	}
	return strings.Join(names, ", ")
}

// ValidStage reports whether s is one of the built-in stages.
func ValidStage(s Stage) bool { return stages[s] }

// ValidRole reports whether r is one of the known roles.
func ValidRole(r Role) bool {
	return r == RoleAgent || r == RoleHuman || r == RoleAdmin
}

// TransitionTable is the configurable form of the stage rules:
// role -> from stage -> allowed target stages. Human and admin are fixed
// by the concept (they may move anything); the table carries the agent
// rules, and Parse fills missing roles with the defaults below.
type TransitionTable = map[string]map[string][]string

// DefaultTransitions is the starting proposal of docs/CONCEPT.md: agents
// take a ticket up to review and may park it; a human sets done,
// cancelled and may reopen.
func DefaultTransitions() TransitionTable {
	return map[string]map[string][]string{
		"agent": {
			"open":        {"in-progress", "blocked", "needs-input"},
			"in-progress": {"review", "blocked", "needs-input", "open"},
			"blocked":     {"in-progress", "open"},
			"needs-input": {"in-progress", "open"},
			"review":      {"in-progress"},
		},
	}
}

// CanTransitionTable reports whether role may move a ticket from one stage
// to another under the given table. Human and admin may always; a role or
// stage missing from the table means the move is not allowed.
func CanTransitionTable(table TransitionTable, role Role, from, to Stage) bool {
	return CanTransitionIn(DefaultStageSet(), table, role, from, to)
}

// CanTransitionIn is CanTransitionTable under a store's stage vocabulary.
func CanTransitionIn(set StageSet, table TransitionTable, role Role, from, to Stage) bool {
	if !set.Valid(from) || !set.Valid(to) {
		return false
	}
	if role == RoleHuman || role == RoleAdmin {
		return true
	}
	for _, t := range table[string(role)][string(from)] {
		if Stage(t) == to {
			return true
		}
	}
	return false
}

// CanTransition reports whether role may move a ticket from one stage to
// another under the default table (dricket.toml can override it).
func CanTransition(role Role, from, to Stage) bool {
	return CanTransitionTable(DefaultTransitions(), role, from, to)
}
