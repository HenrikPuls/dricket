// SPDX-License-Identifier: GPL-3.0-or-later

package ticket

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/lariscus/dricket/internal/frontmatter"
)

// ExtensionPrefix starts the key namespace reserved for extension fields:
// single-line values that dricket never interprets, only preserves, scans
// for secrets and size-limits. Consumers use them for their own metadata.
const ExtensionPrefix = "x-"

// MaxExtensionValue is the byte cap of one extension value.
const MaxExtensionValue = 1000

var extensionKey = regexp.MustCompile(`^x-[a-z0-9][a-z0-9-]*$`)

// ValidExtensionKey reports whether key is a well-formed extension key
// (`x-<name>`, lower case letters, digits and inner dashes).
func ValidExtensionKey(key string) bool { return extensionKey.MatchString(key) }

// CheckExtensionValue validates the shape of an extension value: a single
// line without control characters, at most MaxExtensionValue bytes.
func CheckExtensionValue(key, value string) error {
	if len(value) > MaxExtensionValue {
		return fmt.Errorf("%s: value is %d bytes, the limit is %d", key, len(value), MaxExtensionValue)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s: value must be a single line without control characters", key)
		}
	}
	return nil
}

// Extension returns the value of an extension field; ok is false when absent.
func (t Ticket) Extension(key string) (string, bool) {
	for _, f := range t.Extra {
		if f.Key == key {
			return f.Value, true
		}
	}
	return "", false
}

// SetExtension sets an extension field, keeping the position of an existing
// key and appending a new one at the end of the header. An empty value
// removes the key. It reports whether the field list changed.
func (t *Ticket) SetExtension(key, value string) bool {
	for i, f := range t.Extra {
		if f.Key != key {
			continue
		}
		if value == "" {
			t.Extra = append(t.Extra[:i:i], t.Extra[i+1:]...)
			return true
		}
		if f.Value == value {
			return false
		}
		t.Extra[i].Value = value
		return true
	}
	if value == "" {
		return false
	}
	t.Extra = append(t.Extra, frontmatter.Field{Key: key, Value: value})
	return true
}

// ExtensionFields returns the extension fields by key.
func (t Ticket) ExtensionFields() map[string]string {
	out := map[string]string{}
	for _, f := range t.Extra {
		if cls, _ := ClassOf(f.Key); cls == Extension {
			out[f.Key] = f.Value
		}
	}
	return out
}

// StageSet is the stage vocabulary of a store: the configurable form of the
// fixed seven stages of docs/CONCEPT.md ([stages] in dricket.toml).
type StageSet struct {
	Values    []Stage // ordered, as shown to users
	Initial   Stage   // stage of a new unclaimed ticket
	Terminal  []Stage // satisfy `depends`; `next` skips them
	Claim     []Stage // a claim is only valid in these stages
	Cancelled Stage   // optional reserved stage, always valid; "" for none
	// Legacy marks the built-in set. Only it keeps the old coupling
	// "in-progress means claimed" and the "free means open" rule of next.
	Legacy bool
}

// DefaultStageSet is the built-in vocabulary: today's seven stages.
func DefaultStageSet() StageSet {
	return StageSet{
		Values: []Stage{StageOpen, StageInProgress, StageReview, StageDone,
			StageBlocked, StageNeedsInput, StageCancelled},
		Initial:   StageOpen,
		Terminal:  []Stage{StageDone},
		Claim:     []Stage{StageInProgress},
		Cancelled: StageCancelled,
		Legacy:    true,
	}
}

func contains(list []Stage, s Stage) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// Valid reports whether s belongs to the vocabulary.
func (set StageSet) Valid(s Stage) bool {
	return contains(set.Values, s) || (set.Cancelled != "" && s == set.Cancelled)
}

// IsTerminal reports whether a ticket in s satisfies a dependency on it:
// terminal stages and the reserved cancelled stage.
func (set StageSet) IsTerminal(s Stage) bool {
	return contains(set.Terminal, s) || (set.Cancelled != "" && s == set.Cancelled)
}

// InClaim reports whether a claim is valid in s.
func (set StageSet) InClaim(s Stage) bool { return contains(set.Claim, s) }

// AfterClaim is the stage a ticket gets when it is claimed from cur.
func (set StageSet) AfterClaim(cur Stage, takeover bool) Stage {
	if set.Legacy {
		if cur == set.Initial || takeover {
			return set.Claim[0]
		}
		return cur
	}
	if set.InClaim(cur) {
		return cur
	}
	return set.Claim[0]
}

// Pickable reports whether `next` may offer an unclaimed ticket in s.
func (set StageSet) Pickable(s Stage) bool {
	if set.Legacy {
		return s == set.Initial
	}
	return set.InClaim(s)
}

// Names lists the vocabulary for messages.
func (set StageSet) Names() string {
	names := make([]string, 0, len(set.Values)+1)
	for _, v := range set.Values {
		names = append(names, string(v))
	}
	if set.Cancelled != "" && !contains(set.Values, set.Cancelled) {
		names = append(names, string(set.Cancelled))
	}
	return strings.Join(names, ", ")
}

// DefaultTransitionsFor is the agent rule table that goes with a custom
// vocabulary when dricket.toml gives none: an agent may move a ticket
// between any two non-terminal stages; terminal stages stay with humans.
// The built-in vocabulary keeps DefaultTransitions.
func DefaultTransitionsFor(set StageSet) TransitionTable {
	if set.Legacy {
		return DefaultTransitions()
	}
	moves := map[string][]string{}
	for _, from := range set.Values {
		if set.IsTerminal(from) {
			continue
		}
		for _, to := range set.Values {
			if to != from && !set.IsTerminal(to) {
				moves[string(from)] = append(moves[string(from)], string(to))
			}
		}
	}
	return TransitionTable{"agent": moves}
}
