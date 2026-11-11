// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lariscus/dricket/internal/frontmatter"
	"github.com/lariscus/dricket/internal/store"
	"github.com/lariscus/dricket/internal/ticket"
)

// Thresholds for the lease status display; estimates from
// docs/CONCEPT.md, Monitoring (not part of dricket.toml).
const (
	// ExpiringFraction: less than this share of the lease left means
	// status "expiring".
	ExpiringFraction = 0.2
	// QuietFraction: no activity for more than this share of the lease is
	// the hint "quiet". Not a reason for takeover.
	QuietFraction = 0.5
)

// Lease statuses (docs/CONCEPT.md, Monitoring).
const (
	LeaseOK       = "ok"
	LeaseExpiring = "expiring"
	LeaseExpired  = "expired"
)

// ------------------------------------------------------------- extend ----

// ExtendResult reports the outcome of an extend.
type ExtendResult struct {
	ID         string `json:"id"`
	Epoch      int    `json:"epoch"`
	Extensions int    `json:"extensions"`
	MaxExtend  int    `json:"max_extensions"`
	LeaseUntil string `json:"lease_until"`
}

// Extend lengthens the lease of a ticket its owner still holds. The reason
// is required and lands in the log; the counter is limited by the
// max_extensions value of dricket.toml (default 3).
func (s *Service) Extend(ctx context.Context, id, reason string) (ExtendResult, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	path, err := PathFor(id)
	if err != nil {
		return ExtendResult{}, err
	}
	reason = sanitize(strings.TrimSpace(reason), MaxBody)
	if reason == "" {
		return ExtendResult{}, errf(KindInvalid, "extend requires a reason")
	}
	cfg, cerr := s.config(ctx)
	if cerr != nil {
		return ExtendResult{}, cerr
	}

	var out ExtendResult
	spec := store.WriteSpec{
		Message: "dricket: extend " + id,
		Check: func(h store.Head) error {
			tk, _, err := loadTicket(h, path)
			if err != nil {
				return err
			}
			switch {
			case tk.Owner == "":
				return errf(KindInvalid, "ticket %s is not claimed", id)
			case tk.Owner != s.Owner:
				return errf(KindClaimed, "ticket %s is claimed by %s, not by you", id, tk.Owner)
			case tk.Extensions >= cfg.MaxExtensions:
				return errf(KindInvalid, "extend limit reached (%d times); release the ticket instead", cfg.MaxExtensions)
			}
			out = ExtendResult{ID: id, Epoch: tk.Epoch, Extensions: tk.Extensions + 1, MaxExtend: cfg.MaxExtensions}
			return nil
		},
		Mutate: func(w store.Worktree) error {
			tk, err := readWorktreeTicket(w, path)
			if err != nil {
				return err
			}
			now := s.Now()
			lease, err := cfg.Lease(tk.Size)
			if err != nil {
				return err
			}
			tk.Extensions++
			tk.LeaseUntil = now.Add(lease)
			tk.Rev++
			appendLog(&tk, s.Owner, "extend "+id+": "+firstLine(reason), now)
			if err := s.checkLint(tk, id); err != nil {
				return err
			}
			out.LeaseUntil = stamp(tk.LeaseUntil)
			return w.WriteFile(path, tk.ToDoc().Render())
		},
	}
	if err := s.Store.Write(ctx, spec); err != nil {
		return ExtendResult{}, err
	}
	return out, nil
}

// --------------------------------------------------------------- note ----

// NoteResult reports the outcome of a note.
type NoteResult struct {
	ID  string `json:"id"`
	Rev int    `json:"rev"`
}

// Note appends a log entry without changing the stage; the claim stays
// with the owner. `update --note` maps here in stage v1; editing body
// sections through the tool comes later.
func (s *Service) Note(ctx context.Context, id, text string) (NoteResult, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	path, err := PathFor(id)
	if err != nil {
		return NoteResult{}, err
	}
	text = sanitize(strings.TrimSpace(text), MaxComment)
	if text == "" {
		return NoteResult{}, errf(KindInvalid, "note text is empty")
	}
	if err := s.scanSecrets(ctx, text); err != nil {
		return NoteResult{}, err
	}

	var out NoteResult
	spec := store.WriteSpec{
		Message: "dricket: note on " + id,
		Check: func(h store.Head) error {
			tk, _, err := loadTicket(h, path)
			if err != nil {
				return err
			}
			if tk.Owner == "" {
				return errf(KindInvalid, "ticket %s is not claimed", id)
			}
			if tk.Owner != s.Owner {
				return errf(KindClaimed, "ticket %s is claimed by %s, not by you", id, tk.Owner)
			}
			return nil
		},
		Mutate: func(w store.Worktree) error {
			tk, err := readWorktreeTicket(w, path)
			if err != nil {
				return err
			}
			now := s.Now()
			tk.Rev++
			appendLog(&tk, s.Owner, "note "+id+": "+firstLine(text), now)
			if err := s.checkLint(tk, id); err != nil {
				return err
			}
			out = NoteResult{ID: id, Rev: tk.Rev}
			return w.WriteFile(path, tk.ToDoc().Render())
		},
	}
	if err := s.Store.Write(ctx, spec); err != nil {
		return NoteResult{}, err
	}
	return out, nil
}

// readWorktreeTicket loads and parses a ticket from the working copy.
func readWorktreeTicket(w store.Worktree, path string) (ticket.Ticket, error) {
	content, err := w.ReadFile(path)
	if err != nil {
		return ticket.Ticket{}, err
	}
	doc, err := frontmatter.Parse(content)
	if err != nil {
		return ticket.Ticket{}, err
	}
	tk, _, err := ticket.FromDoc(doc)
	if err != nil {
		return ticket.Ticket{}, err
	}
	return tk, nil
}

// ------------------------------------------------------------- leases ----

// LeaseInfo is one row of `dk leases`.
type LeaseInfo struct {
	ID           string `json:"id"`
	Owner        string `json:"owner"`
	Size         string `json:"size"`
	LeaseUntil   string `json:"lease_until"`
	RemainingMin int    `json:"remaining_min"`
	Status       string `json:"status"` // ok, expiring, expired
	Quiet        bool   `json:"quiet"`  // hint: no activity for half the lease
}

// LeasesFilter narrows `dk leases`; both flags may be combined with plain
// output. Expiring keeps status "expiring", Stale keeps status "expired"
// (dead claims, the runbook case).
type LeasesFilter struct {
	Expiring bool
	Stale    bool
}

// Leases lists all active leases with time remaining, most urgent first.
// Read only: one fetch, then reads against the fetched head.
func (s *Service) Leases(ctx context.Context, f LeasesFilter) ([]LeaseInfo, error) {
	if err := s.Store.Refresh(ctx); err != nil {
		return nil, wrapRead(err)
	}
	files, err := s.Store.ReadAll(ctx, "tickets/", "/ticket.md")
	if err != nil {
		return nil, wrapRead(err)
	}
	cfg, cerr := s.config(ctx)
	if cerr != nil {
		return nil, cerr
	}
	now := s.Now()
	var out []LeaseInfo
	for _, file := range files {
		p := file.Path
		doc, err := frontmatter.Parse(file.Content)
		if err != nil {
			continue
		}
		tk, _, err := ticket.FromDoc(doc)
		if err != nil || tk.Owner == "" {
			continue
		}
		dur, derr := cfg.Lease(tk.Size)
		if derr != nil {
			dur = cfg.LeaseM
		}
		status := LeaseOK
		switch {
		case tk.LeaseUntil.Add(Grace).Before(now):
			status = LeaseExpired
		case now.Add(time.Duration(float64(dur) * ExpiringFraction)).After(tk.LeaseUntil):
			status = LeaseExpiring
		}
		if f.Expiring && status != LeaseExpiring {
			continue
		}
		if f.Stale && status != LeaseExpired {
			continue
		}
		quiet := false
		if last, ok := s.Store.LastChange(ctx, p); ok {
			quiet = now.Sub(last) > time.Duration(float64(dur)*QuietFraction)
		}
		remaining := tk.LeaseUntil.Sub(now)
		if remaining < 0 {
			remaining = 0
		}
		out = append(out, LeaseInfo{
			ID:           tk.ID,
			Owner:        tk.Owner,
			Size:         string(tk.Size),
			LeaseUntil:   stamp(tk.LeaseUntil),
			RemainingMin: int(remaining.Minutes()),
			Status:       status,
			Quiet:        quiet,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LeaseUntil < out[j].LeaseUntil })
	return out, nil
}

// HasExpired reports whether any lease in the list is expired
// (`leases --check`, exit 20).
func HasExpired(infos []LeaseInfo) bool {
	for _, l := range infos {
		if l.Status == LeaseExpired {
			return true
		}
	}
	return false
}

// -------------------------------------------------------------- inbox ----

// InboxComment is one new comment since the last sight.
type InboxComment struct {
	File      string `json:"file"`
	Author    string `json:"author"`
	CreatedAt string `json:"created_at"`
	Excerpt   string `json:"excerpt"`
}

// InboxResult is what `dk inbox <id>` reports: new log lines and new
// comments since the last time this owner looked at the ticket.
type InboxResult struct {
	ID          string         `json:"id"`
	Rev         int            `json:"rev"`
	Owner       string         `json:"owner"`
	FirstSeen   bool           `json:"first_seen"`
	NewLog      []string       `json:"new_log"`
	NewComments []InboxComment `json:"new_comments"`
}

// seenState is the local marker for one ticket; disposable, per store and
// per owner, never part of the repository. Comments holds the base names
// of the comment files already seen (an old numeric counter in a stale
// marker simply resets the state).
type seenState struct {
	Rev      int      `json:"rev"`
	Comments []string `json:"comments,omitempty"`
}

type inboxState struct {
	Tickets map[string]seenState `json:"tickets"`
}

// Inbox shows what is new for a ticket since the last look and moves the
// marker forward. The first call only records the current state.
func (s *Service) Inbox(ctx context.Context, id string) (InboxResult, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	path, err := PathFor(id)
	if err != nil {
		return InboxResult{}, err
	}
	if err := s.Store.Refresh(ctx); err != nil {
		return InboxResult{}, wrapRead(err)
	}
	if err := s.Store.Pin(ctx); err != nil { // ticket and comments from one state
		return InboxResult{}, wrapRead(err)
	}
	content, err := s.Store.ReadFile(ctx, path)
	if err != nil {
		return InboxResult{}, errf(KindNotFound, "ticket %s: not found", id)
	}
	doc, perr := frontmatter.Parse(content)
	if perr != nil {
		return InboxResult{}, errf(KindInvalid, "%s: %v", path, perr)
	}
	tk, _, terr := ticket.FromDoc(doc)
	if terr != nil {
		return InboxResult{}, errf(KindInvalid, "%s: %v", path, terr)
	}

	commentsDir := strings.TrimSuffix(path, "ticket.md") + "comments"
	var names []string // comment base names; ls order = chronological
	if files, ferr := s.Store.List(ctx, commentsDir+"/"); ferr == nil {
		for _, f := range files {
			if strings.HasSuffix(f, ".md") {
				names = append(names, strings.TrimPrefix(f, commentsDir+"/"))
			}
		}
	}

	state := s.loadInboxState()
	seen, ok := state.Tickets[path]
	res := InboxResult{ID: tk.ID, Rev: tk.Rev, Owner: tk.Owner}
	if !ok {
		res.FirstSeen = true
	} else {
		if d := tk.Rev - seen.Rev; d > 0 {
			res.NewLog = lastLogLines(tk.Body, d)
		}
		seenFiles := map[string]bool{}
		for _, n := range seen.Comments {
			seenFiles[n] = true
		}
		for _, n := range names {
			if !seenFiles[n] {
				res.NewComments = append(res.NewComments, s.readComment(ctx, commentsDir+"/"+n))
			}
		}
	}
	state.Tickets[path] = seenState{Rev: tk.Rev, Comments: names}
	if err := s.saveInboxState(state); err != nil {
		return InboxResult{}, err
	}
	return res, nil
}

// readComment loads one comment file for the inbox summary.
func (s *Service) readComment(ctx context.Context, file string) InboxComment {
	c := InboxComment{File: file}
	content, err := s.Store.ReadFile(ctx, file)
	if err != nil {
		return c
	}
	if doc, perr := frontmatter.Parse(content); perr == nil {
		if v, ok := doc.Get("author"); ok {
			c.Author = v
		}
		if v, ok := doc.Get("created_at"); ok {
			c.CreatedAt = v
		}
		c.Excerpt = excerpt(doc.Body, 80)
	}
	return c
}

// excerpt returns the first non-empty body line, trimmed to limit runes.
func excerpt(body string, limit int) string {
	for _, l := range strings.Split(body, "\n") {
		if l = strings.TrimSpace(l); l != "" {
			r := []rune(l)
			if len(r) > limit {
				r = r[:limit]
			}
			return string(r)
		}
	}
	return ""
}

// lastLogLines returns the last n log lines ("- " prefixed) of a body.
func lastLogLines(body string, n int) []string {
	var lines []string
	for _, l := range strings.Split(body, "\n") {
		if strings.HasPrefix(strings.TrimRight(l, "\r"), "- ") {
			lines = append(lines, strings.TrimRight(l, "\r"))
		}
	}
	if n > len(lines) {
		n = len(lines)
	}
	return lines[len(lines)-n:]
}

func (s *Service) inboxPath() (string, error) {
	dir, err := s.Store.StateDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "inbox.json"), nil
}

func (s *Service) loadInboxState() inboxState {
	st := inboxState{Tickets: map[string]seenState{}}
	p, err := s.inboxPath()
	if err != nil {
		return st
	}
	b, err := os.ReadFile(p) // #nosec G304 -- path built by the tool from its data dir
	if err != nil {
		return st // no marker yet
	}
	if jerr := json.Unmarshal(b, &st); jerr != nil || st.Tickets == nil {
		return inboxState{Tickets: map[string]seenState{}} // disposable: reset on corruption
	}
	return st
}

func (s *Service) saveInboxState(st inboxState) error {
	p, err := s.inboxPath()
	if err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p, b, 0o600)
}
