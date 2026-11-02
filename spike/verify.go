// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// verify replays the complete history of the bare repository and checks the
// four invariants of docs/CONCEPT.md:
//
//  1. duplicate IDs: no ticket path is ever created twice,
//  2. two owners: every ownership transition is legal (free ticket, same
//     owner, or takeover only after lease_until + grace),
//  3. lost updates: every log tag of every accepted push is still present
//     in the final state of its ticket,
//  4. identity violations: id, created_at, created_by and schema_version
//     never change after creation.
//
// It also checks spike-protocol rules the harness itself must uphold: one
// ticket per commit, rev +1 per write, id equals the path.
func verify(bare string, cfg *config, acc *accepted) (violations []string, err error) {
	base := filepath.Dir(bare)
	vdir := filepath.Join(base, "verify")
	if err := os.RemoveAll(vdir); err != nil {
		return nil, err
	}
	if r := git(base, nil, "clone", "--quiet", bare, vdir); r.err != nil {
		return nil, fmt.Errorf("clone for verification: %v: %s", r.err, r.stderr)
	}

	type snap struct {
		status byte
		commit string
		t      *ticket
	}
	byPath := map[string][]snap{}
	createdIn := map[string]string{}
	var viol []string

	logs := git(vdir, nil, "log", "--reverse", "--format=%H", "--", "tickets/")
	if logs.err != nil {
		return nil, fmt.Errorf("git log: %v: %s", logs.err, logs.stderr)
	}
	for _, c := range lines(logs.stdout) {
		ns := git(vdir, nil, "diff-tree", "--no-commit-id", "--name-status", "-r", c)
		if ns.err != nil {
			return nil, fmt.Errorf("diff-tree %s: %v: %s", c, ns.err, ns.stderr)
		}
		var files []struct {
			status byte
			path   string
		}
		for _, l := range lines(ns.stdout) {
			f := strings.SplitN(l, "\t", 2)
			if len(f) != 2 {
				continue
			}
			files = append(files, struct {
				status byte
				path   string
			}{f[0][0], f[1]})
		}
		if len(files) == 0 {
			continue
		}
		if len(files) > 1 {
			viol = append(viol, fmt.Sprintf("commit %s touches %d paths; the protocol allows one ticket per commit", c[:10], len(files)))
		}
		for _, f := range files {
			if !strings.HasSuffix(f.path, "/ticket.md") {
				viol = append(viol, fmt.Sprintf("commit %s changes non-ticket path %s", c[:10], f.path))
				continue
			}
			show := git(vdir, nil, "show", c+":"+f.path)
			if show.err != nil {
				return nil, fmt.Errorf("show %s:%s: %v", c, f.path, show.err)
			}
			t, perr := parseTicket(show.stdout)
			if perr != nil {
				viol = append(viol, fmt.Sprintf("%s at %s: %v", f.path, c[:10], perr))
				continue
			}
			byPath[f.path] = append(byPath[f.path], snap{f.status, c, t})
		}
	}

	acceptedTags := acc.snapshot()
	for path, snaps := range byPath {
		parts := strings.Split(path, "/")
		dirID := ""
		if len(parts) >= 2 {
			dirID = parts[len(parts)-2]
		}
		for i, s := range snaps {
			var prev *ticket
			if i > 0 {
				prev = snaps[i-1].t
			}
			viol = append(viol, checkSnapshot(path, s.status, prev, s.t, cfg.Grace)...)
			if s.status == 'A' {
				if s.t.get(fID) != dirID {
					viol = append(viol, fmt.Sprintf("%s: id %q does not match its path", path, s.t.get(fID)))
				}
				if first, dup := createdIn[path]; dup {
					viol = append(viol, fmt.Sprintf("duplicate id: %s created in %s and again in %s", path, first[:10], s.commit[:10]))
				} else {
					createdIn[path] = s.commit
				}
			}
		}
		// Lost updates: every accepted push must still be visible at HEAD.
		finalTags := tagSet(snaps[len(snaps)-1].t)
		for _, tag := range acceptedTags[path] {
			if !finalTags[tag] {
				viol = append(viol, fmt.Sprintf("lost update: %s: accepted %s missing from the final state", path, tag))
			}
		}
	}
	return viol, nil
}
