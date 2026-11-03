// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/HenrikPuls/dricket/internal/config"
)

// Discovery is a ticket store found by walking up from a directory, the
// way git discovers a repository.
type Discovery struct {
	Dir    string // store root (absolute)
	Remote string // origin URL, or Dir itself when the store has no origin
	Branch string // ticket branch (dricket.toml branch key, or "tickets")
}

// Spec renders the discovery as the tool's store spec remote[#branch].
func (d Discovery) Spec() string {
	return d.Remote + "#" + d.Branch
}

// DiscoverFrom walks up from dir to the volume root and returns the
// first store root: a git working tree that contains dricket.toml.
// The remote is the origin URL when the repository has one and the
// directory itself (absolute) when it does not - the local stores that
// `dk init` creates are used in place, so they must survive being moved.
// ok is false when no store root was found.
func DiscoverFrom(ctx context.Context, dir string) (Discovery, bool, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Discovery{}, false, fmt.Errorf("discovery: %w", err)
	}
	for {
		found, ok, ferr := InspectRoot(ctx, abs)
		if ferr != nil || ok {
			return found, ok, ferr
		}
		parent := filepath.Dir(abs)
		if parent == abs {
			return Discovery{}, false, nil
		}
		abs = parent
	}
}

// InspectRoot reports whether dir is a store root: the root of a git working
// tree with committed config on main or, for compatibility, dricket.toml in
// the current checkout. ok is false for subdirectories, plain git repositories,
// bare repositories and directories without either config source.
func InspectRoot(ctx context.Context, dir string) (Discovery, bool, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Discovery{}, false, fmt.Errorf("discovery: %w", err)
	}
	dir = abs
	if out, err := runGitEnv(ctx, dir, Options{}, "rev-parse", "--is-inside-work-tree"); err != nil ||
		strings.TrimSpace(out) != "true" {
		return Discovery{}, false, nil
	}
	root, err := runGitEnv(ctx, dir, Options{}, "rev-parse", "--show-toplevel")
	if err != nil || filepath.Clean(strings.TrimSpace(root)) != filepath.Clean(dir) {
		return Discovery{}, false, nil
	}

	committed, gitErr := runGitEnv(ctx, dir, Options{}, "show", "refs/heads/main:dricket.toml")
	var data []byte
	if gitErr == nil {
		data = []byte(committed)
	} else {
		data, err = os.ReadFile(filepath.Join(dir, "dricket.toml")) // #nosec G304 -- fixed name in the inspected directory
		if err != nil {
			return Discovery{}, false, nil
		}
	}

	// A broken file does not block discovery - the default applies and the
	// real error surfaces when the config is loaded for a write.
	branch := "tickets"
	if cfg, perr := config.Parse(data); perr == nil && cfg.Branch != "" {
		branch = cfg.Branch
	}

	remote := dir
	if origin, err := runGitEnv(ctx, dir, Options{}, "remote", "get-url", "origin"); err == nil {
		if u := strings.TrimSpace(origin); u != "" {
			remote = u
		}
	}
	return Discovery{Dir: dir, Remote: remote, Branch: branch}, true, nil
}
