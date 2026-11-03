// SPDX-License-Identifier: GPL-3.0-or-later

// Package config reads dricket.toml, the declarative configuration on the
// protected main branch of a ticket store: lease limits, the extension
// limit, admins, the ticket quota, the secret scan switch, the minimum
// tool version and the stage transitions per role (docs/CONCEPT.md,
// "Declarative schema"). A missing config means the built-in defaults; a
// broken config is a hard error for writes and a finding in validate.
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/HenrikPuls/dricket/internal/ticket"
)

// ConfigSchemaVersion is the version of dricket.toml this tool reads.
const ConfigSchemaVersion = 1

// Config is the effective configuration after defaults are applied.
type Config struct {
	SchemaVersion int
	MinVersion    string // dotted integers; "" disables the check
	Branch        string // ticket branch of this store (discovery default)

	LeaseS        time.Duration
	LeaseM        time.Duration
	LeaseL        time.Duration
	LeaseMin      time.Duration // clamp; 0 disables
	LeaseMax      time.Duration // clamp and validate bound; 0 disables
	MaxExtensions int

	Admins             []string // owner ids allowed to force-release
	QuotaTicketsPerDay int      // new tickets per owner and day; 0 disables
	SecretScan         bool

	// Priorities: the free field `priority` may only carry values from
	// this list (starting set critical, high, normal, low).
	PriorityValues  []string
	PriorityDefault string

	Transitions ticket.TransitionTable
}

// raw is the decoded dricket.toml before defaults and parsing apply.
// Pointer fields distinguish "key absent" from "set to zero".
type raw struct {
	SchemaVersion int    `toml:"schema_version"`
	MinVersion    string `toml:"min_version"`
	Branch        string `toml:"branch"`
	Limits        struct {
		LeaseS        string `toml:"lease_s"`
		LeaseM        string `toml:"lease_m"`
		LeaseL        string `toml:"lease_l"`
		LeaseMin      string `toml:"lease_min"`
		LeaseMax      string `toml:"lease_max"`
		MaxExtensions *int   `toml:"max_extensions"`
	} `toml:"limits"`
	Security struct {
		Admins             []string `toml:"admins"`
		QuotaTicketsPerDay *int     `toml:"quota_tickets_per_day"`
		SecretScan         *bool    `toml:"secret_scan"`
	} `toml:"security"`
	Priorities struct {
		Values  []string `toml:"values"`
		Default string   `toml:"default"`
	} `toml:"priorities"`
	Transitions ticket.TransitionTable `toml:"transitions"`
}

// Defaults returns the built-in configuration, the starting values of
// docs/CONCEPT.md. All numbers there are estimates; a store's
// dricket.toml is the place to change them.
func Defaults() *Config {
	scan := true
	maxExt := 3
	return &Config{
		SchemaVersion: ConfigSchemaVersion,
		Branch:        "tickets",
		LeaseS:        2 * time.Hour,
		LeaseM:        8 * time.Hour,
		LeaseL:        24 * time.Hour,
		LeaseMin:      15 * time.Minute,
		LeaseMax:      72 * time.Hour,
		MaxExtensions: maxExt,
		Admins:        nil,
		// Abuse protection: a buggy agent must not create thousands of
		// tickets. Fifty per day is a generous starting value.
		QuotaTicketsPerDay: 50,
		SecretScan:         scan,
		PriorityValues:     []string{"critical", "high", "normal", "low"},
		PriorityDefault:    "normal",
		Transitions:        ticket.DefaultTransitions(),
	}
}

// Parse decodes dricket.toml strictly: unknown keys are an error (the
// config is protected and maintained deliberately), values are checked,
// everything missing falls back to the defaults.
func Parse(data []byte) (*Config, error) {
	var r raw
	md, err := toml.Decode(string(data), &r)
	if err != nil {
		var perr toml.ParseError
		if errors.As(err, &perr) {
			return nil, fmt.Errorf("line %d: %s", perr.Position.Line, perr.Error())
		}
		return nil, err
	}
	if u := md.Undecoded(); len(u) > 0 {
		keys := make([]string, 0, len(u))
		for _, k := range u {
			keys = append(keys, k.String())
		}
		return nil, fmt.Errorf("unknown key(s) %s; see docs/CONCEPT.md for the schema", strings.Join(keys, ", "))
	}
	cfg := Defaults()
	if r.SchemaVersion != 0 {
		if r.SchemaVersion != ConfigSchemaVersion {
			return nil, fmt.Errorf("schema_version %d is not supported by this tool (supports %d)",
				r.SchemaVersion, ConfigSchemaVersion)
		}
		cfg.SchemaVersion = r.SchemaVersion
	}
	cfg.MinVersion = r.MinVersion
	// The branch key feeds store discovery; the store itself validates
	// the name with git before using it.
	if r.Branch != "" {
		cfg.Branch = r.Branch
	}

	dur := func(field, what string, into *time.Duration) error {
		if field == "" {
			return nil
		}
		d, err := time.ParseDuration(field)
		if err != nil || d <= 0 {
			return fmt.Errorf("%s: expected a positive duration like \"2h\", got %q", what, field)
		}
		*into = d
		return nil
	}
	if err := dur(r.Limits.LeaseS, "limits.lease_s", &cfg.LeaseS); err != nil {
		return nil, err
	}
	if err := dur(r.Limits.LeaseM, "limits.lease_m", &cfg.LeaseM); err != nil {
		return nil, err
	}
	if err := dur(r.Limits.LeaseL, "limits.lease_l", &cfg.LeaseL); err != nil {
		return nil, err
	}
	// A clamp key left out keeps the default; there is no way to express
	// "no clamp" in the file, because an unbounded lease contradicts the
	// concept's validate bound.
	if err := dur(r.Limits.LeaseMin, "limits.lease_min", &cfg.LeaseMin); err != nil {
		return nil, err
	}
	if err := dur(r.Limits.LeaseMax, "limits.lease_max", &cfg.LeaseMax); err != nil {
		return nil, err
	}
	if r.Limits.MaxExtensions != nil {
		if *r.Limits.MaxExtensions < 0 {
			return nil, fmt.Errorf("limits.max_extensions: must not be negative, got %d", *r.Limits.MaxExtensions)
		}
		cfg.MaxExtensions = *r.Limits.MaxExtensions
	}
	cfg.Admins = r.Security.Admins
	if r.Security.QuotaTicketsPerDay != nil {
		if *r.Security.QuotaTicketsPerDay < 0 {
			return nil, fmt.Errorf("security.quota_tickets_per_day: must not be negative, got %d", *r.Security.QuotaTicketsPerDay)
		}
		cfg.QuotaTicketsPerDay = *r.Security.QuotaTicketsPerDay
	}
	if r.Security.SecretScan != nil {
		cfg.SecretScan = *r.Security.SecretScan
	}
	if err := mergePriorities(cfg, r.Priorities); err != nil {
		return nil, err
	}
	if r.MinVersion != "" && !validVersion(r.MinVersion) {
		return nil, fmt.Errorf("min_version %q is not dotted integers like \"0.2.0\"", r.MinVersion)
	}
	if err := mergeTransitions(cfg.Transitions, r.Transitions); err != nil {
		return nil, err
	}
	return cfg, nil
}

// mergePriorities folds the [priorities] table over the defaults: values
// are replaced as a set (never extended piecemeal), and the default must
// be one of them. Without an explicit default, "normal" wins if it is
// among the values, otherwise the first value.
func mergePriorities(cfg *Config, p struct {
	Values  []string `toml:"values"`
	Default string   `toml:"default"`
}) error {
	switch {
	case p.Values == nil && p.Default == "":
		return nil // nothing configured: keep the starting set
	case p.Values == nil:
		return fmt.Errorf("priorities.default %q given without priorities.values", p.Default)
	case len(p.Values) == 0:
		return fmt.Errorf("priorities.values must not be empty; leave the key out for the starting set")
	}
	seen := map[string]bool{}
	for _, v := range p.Values {
		if v == "" || v != strings.ToLower(v) || !isPriorityWord(v) {
			return fmt.Errorf("priorities.values: %q is not a lower-case word of letters, digits and dashes", v)
		}
		if seen[v] {
			return fmt.Errorf("priorities.values: %q appears twice", v)
		}
		seen[v] = true
	}
	cfg.PriorityValues = append([]string{}, p.Values...)
	switch {
	case p.Default != "":
		if !seen[p.Default] {
			return fmt.Errorf("priorities.default %q is not in priorities.values", p.Default)
		}
		cfg.PriorityDefault = p.Default
	case seen["normal"]:
		cfg.PriorityDefault = "normal"
	default:
		cfg.PriorityDefault = cfg.PriorityValues[0]
	}
	return nil
}

// isPriorityWord accepts lower-case letters, digits and inner dashes.
func isPriorityWord(s string) bool {
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-' && i > 0 && i < len(s)-1:
		default:
			return false
		}
	}
	return true
}

// CheckPriority reports whether value is an allowed priority; the empty
// value is allowed here (an unset priority is a lint warning, not an
// error) - callers that need a concrete value apply PriorityDefault.
func (c *Config) CheckPriority(value string) error {
	if value == "" {
		return nil
	}
	for _, v := range c.PriorityValues {
		if v == value {
			return nil
		}
	}
	return fmt.Errorf("priority %q is not one of %s", value, strings.Join(c.PriorityValues, ", "))
}

// ParsePrefixes reads prefixes.toml, the prefix register on main: a list
// of prefixes that belong to this store. Admins add entries; prefixes are
// never renamed. Only the [prefixes] table with a string array is
// understood; anything else is an error.
func ParsePrefixes(data []byte) ([]string, error) {
	var r struct {
		Prefixes []string `toml:"prefixes"`
	}
	md, err := toml.Decode(string(data), &r)
	if err != nil {
		var perr toml.ParseError
		if errors.As(err, &perr) {
			return nil, fmt.Errorf("line %d: %s", perr.Position.Line, perr.Error())
		}
		return nil, err
	}
	if u := md.Undecoded(); len(u) > 0 {
		keys := make([]string, 0, len(u))
		for _, k := range u {
			keys = append(keys, k.String())
		}
		return nil, fmt.Errorf("unknown key(s) %s; only [prefixes] with a string array", strings.Join(keys, ", "))
	}
	for _, p := range r.Prefixes {
		if p == "" || p != strings.ToLower(p) {
			return nil, fmt.Errorf("prefix %q must be non-empty and lower case", p)
		}
	}
	return r.Prefixes, nil
}

// mergeTransitions folds a partial [transitions] table over the defaults:
// a missing role or stage keeps the default targets; an explicit empty
// list means "no moves allowed from this stage".
func mergeTransitions(base ticket.TransitionTable, overlay ticket.TransitionTable) error {
	for role, stages := range overlay {
		r := ticket.Role(role)
		if !ticket.ValidRole(r) {
			return fmt.Errorf("transitions.%s: unknown role (agent, human, admin)", role)
		}
		if r != ticket.RoleAgent {
			return fmt.Errorf("transitions.%s: human and admin are fixed by the concept (they may move anything)", role)
		}
		for from, targets := range stages {
			if !ticket.ValidStage(ticket.Stage(from)) {
				return fmt.Errorf("transitions.%s.%s: unknown stage", role, from)
			}
			for _, t := range targets {
				if !ticket.ValidStage(ticket.Stage(t)) {
					return fmt.Errorf("transitions.%s.%s: unknown target stage %q", role, from, t)
				}
			}
			base[role][from] = targets
		}
	}
	return nil
}

// Lease returns the clamped starting lease for a size: the size value from
// the config, raised to LeaseMin and capped at LeaseMax.
func (c *Config) Lease(size ticket.Size) (time.Duration, error) {
	var d time.Duration
	switch size {
	case ticket.SizeS:
		d = c.LeaseS
	case ticket.SizeM:
		d = c.LeaseM
	case ticket.SizeL:
		d = c.LeaseL
	default:
		return 0, fmt.Errorf("size %q is not one of S, M, L", string(size))
	}
	if c.LeaseMin > 0 && d < c.LeaseMin {
		d = c.LeaseMin
	}
	if c.LeaseMax > 0 && d > c.LeaseMax {
		d = c.LeaseMax
	}
	return d, nil
}

// IsAdmin reports whether owner may force-release. An empty admins list
// means nobody may (fail closed).
func (c *Config) IsAdmin(owner string) bool {
	for _, a := range c.Admins {
		if a == owner {
			return true
		}
	}
	return false
}

// validVersion accepts dotted integers ("0.2", "1.2.3").
func validVersion(s string) bool {
	_, err := versionParts(s)
	return err == nil
}

// CompareVersion returns -1, 0 or 1 like a numeric comparison of two
// dotted-integer versions; a shorter list counts as trailing zeros.
func CompareVersion(a, b string) (int, error) {
	as, err := versionParts(a)
	if err != nil {
		return 0, fmt.Errorf("version %q: %w", a, err)
	}
	bs, err := versionParts(b)
	if err != nil {
		return 0, fmt.Errorf("version %q: %w", b, err)
	}
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		switch {
		case x < y:
			return -1, nil
		case x > y:
			return 1, nil
		}
	}
	return 0, nil
}

func versionParts(s string) ([]int, error) {
	var out []int
	for _, p := range strings.Split(s, ".") {
		if p == "" || len(p) > 9 {
			return nil, fmt.Errorf("not dotted integers")
		}
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return nil, fmt.Errorf("not dotted integers")
		}
		out = append(out, n)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("not dotted integers")
	}
	return out, nil
}
