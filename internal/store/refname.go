// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"errors"
	"strings"
)

// checkBranchName validates a branch name without starting git: the rules
// of `git check-ref-format --branch` for a plain name (git-check-ref-format
// (1) rules 1-9, applied to refs/heads/<name>, plus the branch-only rules
// "not empty, not HEAD, no leading dash"). The reflog shorthand @{-N} that
// --branch expands is rejected here; a store branch is never a shorthand.
//
// The validator is deliberately at least as strict as git; the differential
// test in refname_test.go runs both on a table of names. Ref names come
// from dricket.toml or a --store spec and end up in refs and file paths, so
// "stricter" is the safe direction (CONCEPT: validate ref names).
func checkBranchName(name string) error {
	switch {
	case name == "":
		return errors.New("empty")
	case name == "HEAD" || name == "@":
		return errors.New("reserved name")
	case strings.HasPrefix(name, "-"):
		return errors.New("starts with a dash")
	case strings.HasPrefix(name, "/") || strings.HasSuffix(name, "/"):
		return errors.New("starts or ends with a slash")
	case strings.HasSuffix(name, "."):
		return errors.New("ends with a dot")
	case strings.Contains(name, ".."):
		return errors.New("contains a double dot")
	case strings.Contains(name, "@{"):
		return errors.New("contains @{")
	case strings.Contains(name, "//"):
		return errors.New("contains an empty path component")
	}
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f:
			return errors.New("contains a control character")
		case strings.ContainsRune(" ~^:?*[\\", r):
			return errors.New("contains a forbidden character")
		}
	}
	for _, comp := range strings.Split(name, "/") {
		if strings.HasPrefix(comp, ".") {
			return errors.New("a path component starts with a dot")
		}
		if strings.HasSuffix(comp, ".lock") {
			return errors.New("a path component ends with .lock")
		}
	}
	return nil
}
