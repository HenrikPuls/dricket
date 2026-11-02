// SPDX-License-Identifier: GPL-3.0-or-later

// Package service implements the use cases of docs/CONCEPT.md on top of
// the store and the ticket model: one function per command. Business logic
// lives here, not in the CLI layer.
package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/HenrikPuls/dricket/internal/config"
	"github.com/HenrikPuls/dricket/internal/frontmatter"
	"github.com/HenrikPuls/dricket/internal/store"
	"github.com/HenrikPuls/dricket/internal/ticket"
)

// Limits that are deliberately NOT part of dricket.toml: the grace is
// the safety margin of the fencing model and changes with an ADR, not
// with a config file. Size limits are about untrusted input size.
const (
	Grace      = 20 * time.Minute
	MaxTitle   = 200
	MaxComment = 100000
	MaxBody    = 100000
)

// Error kinds, one per documented exit code; the CLI maps them directly.
const (
	KindNotFound   = "not-found"  // 10
	KindClaimed    = "claimed"    // 11
	KindConflict   = "conflict"   // 12
	KindClaimLost  = "claim-lost" // 13
	KindInvalid    = "invalid"    // 14
	KindNetwork    = "network"    // 15
	KindTooOld     = "too-old"    // 16
	KindError      = "error"      // 1
	KindUnexpected = "unexpected"
)

var kindCodes = map[string]int{
	KindNotFound: 10, KindClaimed: 11, KindConflict: 12, KindClaimLost: 13,
	KindInvalid: 14, KindNetwork: 15, KindTooOld: 16, KindError: 1,
}

// Error is a service failure with the exit code it maps to.
type Error struct {
	Kind string
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

// ExitCode returns the documented exit code for a service error.
func ExitCode(err error) int {
	var se *Error
	if errors.As(err, &se) {
		if c, ok := kindCodes[se.Kind]; ok {
			return c
		}
		return 1
	}
	var we *store.WriteError
	if errors.As(err, &we) {
		switch we.Outcome {
		case store.PushFailed, store.Timeout:
			return 15
		default:
			return 1
		}
	}
	return 1
}

// KindOf returns the machine-readable kind of a service error.
func KindOf(err error) string {
	var se *Error
	if errors.As(err, &se) {
		return se.Kind
	}
	var we *store.WriteError
	if errors.As(err, &we) {
		return KindNetwork
	}
	return KindError
}

func errf(kind, format string, a ...any) *Error {
	return &Error{Kind: kind, Msg: fmt.Sprintf(format, a...)}
}

// Service runs the use cases for one actor against one store.
type Service struct {
	Store *store.Store
	Owner string // unique owner id of this process
	Now   func() time.Time
	// ToolVersion is this binary's version ("dev" or empty skips the
	// min_version check of dricket.toml).
	ToolVersion string

	cfg     *config.Config
	cfgErr  error
	cfgOnce sync.Once
}

// New builds a service. Owner must be unique per claim-capable process.
func New(st *store.Store, owner string) *Service {
	return &Service{Store: st, Owner: owner, Now: time.Now}
}

// config returns the effective dricket.toml, loaded once per service.
// A missing config means the defaults; a broken config is a hard error
// (kind error, exit 1) — the file is protected, so its breakage blocks
// every operation that needs limits until an admin fixes it.
func (s *Service) config(ctx context.Context) (*config.Config, error) {
	s.cfgOnce.Do(func() {
		data, err := s.Store.ConfigFile(ctx, "dricket.toml")
		if err != nil {
			s.cfgErr = errf(KindError, "%v", err)
			return
		}
		cfg := config.Defaults()
		if len(data) > 0 {
			cfg, err = config.Parse(data)
			if err != nil {
				s.cfgErr = errf(KindError, "dricket.toml: %v", err)
				return
			}
		}
		if cfg.MinVersion != "" && s.ToolVersion != "" && s.ToolVersion != "dev" {
			cmp, err := config.CompareVersion(s.ToolVersion, cfg.MinVersion)
			if err != nil {
				s.cfgErr = errf(KindError, "%v", err)
				return
			}
			if cmp < 0 {
				s.cfgErr = errf(KindTooOld,
					"this tool is version %s, but dricket.toml requires at least %s (exit 16)",
					s.ToolVersion, cfg.MinVersion)
				return
			}
		}
		s.cfg = cfg
	})
	return s.cfg, s.cfgErr
}

// leaseFor maps a size to its starting lease under the config, clamped by
// lease_min and lease_max.
func (s *Service) leaseFor(ctx context.Context, size ticket.Size) (time.Duration, error) {
	cfg, err := s.config(ctx)
	if err != nil {
		return 0, err
	}
	return cfg.Lease(size)
}

// ---------------------------------------------------------- paths and ids -

// PathFor builds the stable ticket path: tickets/<prefix>/<shard>/<id>/
// ticket.md with the shard n/1000 in four digits. The path never changes.
func PathFor(id string) (string, error) {
	if !ticket.ValidID(id) {
		return "", errf(KindInvalid, "ticket id %q is not of the form prefix-number, lower case", id)
	}
	prefix, n, _ := splitID(id)
	shard := fmt.Sprintf("%04d", n/1000)
	return fmt.Sprintf("tickets/%s/%s/%s/ticket.md", prefix, shard, id), nil
}

func splitID(id string) (string, int, error) {
	i := strings.LastIndex(id, "-")
	if i <= 0 || i == len(id)-1 {
		return "", 0, errors.New("expected prefix-number")
	}
	prefix, num := id[:i], id[i+1:]
	n, err := strconv.Atoi(num)
	if err != nil || n < 1 {
		return "", 0, errors.New("number part is not a positive integer")
	}
	return prefix, n, nil
}

// nextNumber finds the highest used number of a prefix on the head.
func nextNumber(h store.Head, prefix string) (int, error) {
	files, err := h.List("tickets/" + prefix + "/")
	if err != nil {
		return 0, err
	}
	highest := 0
	for _, f := range files {
		parts := strings.Split(f, "/")
		if len(parts) != 5 || parts[1] != prefix || parts[4] != "ticket.md" {
			continue
		}
		if _, n, err := splitID(parts[3]); err == nil && n > highest {
			highest = n
		}
	}
	return highest + 1, nil
}

// loadTicket reads and parses a ticket from the head.
func loadTicket(h store.Head, path string) (ticket.Ticket, []string, error) {
	content, err := h.ReadFile(path)
	if err != nil {
		return ticket.Ticket{}, nil, errf(KindNotFound, "%s: not found", path)
	}
	doc, err := frontmatter.Parse(content)
	if err != nil {
		return ticket.Ticket{}, nil, errf(KindInvalid, "%s: %v", path, err)
	}
	tk, unknown, err := ticket.FromDoc(doc)
	if err != nil {
		return ticket.Ticket{}, nil, errf(KindInvalid, "%s: %v", path, err)
	}
	return tk, unknown, nil
}

// appendLog adds one line to the tool-written log section of the body.
func appendLog(tk *ticket.Ticket, actor, event string, now time.Time) {
	line := fmt.Sprintf("- %s %s %s", now.UTC().Format(time.RFC3339), actor, event)
	tk.Body = strings.TrimRight(tk.Body, "\n") + "\n" + line + "\n"
}

// sanitize removes control characters from untrusted text and enforces a
// length limit. Ticket text is untrusted input; it never reaches a shell,
// but it must not smuggle control characters into files either.
func sanitize(s string, limit int) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 && r != '\n' && r != '\t' {
			r = ' '
		}
		if r == 0x7f {
			r = ' '
		}
		b.WriteRune(r)
	}
	out := b.String()
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// checkLint refuses a ticket that does not pass the lint level.
func checkLint(tk ticket.Ticket, id string) error {
	var errs []string
	for _, f := range ticket.Lint(tk) {
		if !f.Warning {
			errs = append(errs, f.String())
		}
	}
	if len(errs) > 0 {
		return errf(KindInvalid, "ticket %s: %s", id, strings.Join(errs, "; "))
	}
	return nil
}
