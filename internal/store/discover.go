// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lariscus/dricket/internal/config"
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
	return InspectRootWith(ctx, nil, dir)
}

// InspectRootWith is InspectRoot that remembers what it learns in probe (nil
// = remember nothing): one `rev-parse` answers "inside a work tree" and "its
// root" together, levels between a directory and its work-tree root are known
// not to be roots without asking git, and the root found is handed to Open
// through Options.Probe.
func InspectRootWith(ctx context.Context, probe *Probe, dir string) (Discovery, bool, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return Discovery{}, false, fmt.Errorf("discovery: %w", err)
	}
	dir = abs
	if probe.knownNotRoot(dir) {
		return Discovery{}, false, nil
	}
	out, err := runGitEnv(ctx, dir, Options{}, "rev-parse", "--is-inside-work-tree", "--show-toplevel")
	facts := lines(out)
	if err != nil || len(facts) != 2 || facts[0] != "true" {
		return Discovery{}, false, nil
	}
	if !samePath(facts[1], dir) {
		probe.markNotRoots(dir, facts[1])
		return Discovery{}, false, nil
	}
	probe.rememberRoot(dir, facts[1])

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

// Probe carries repository facts found during one call's discovery so later
// steps do not ask git again. It lives for one process invocation; a nil
// Probe is valid and remembers nothing.
type Probe struct {
	mu       sync.Mutex
	roots    map[string]string // directory -> work-tree root git reported
	notRoots map[string]bool   // directories known to lie inside a work tree, below its root
}

// NewProbe returns an empty Probe.
func NewProbe() *Probe {
	return &Probe{roots: map[string]string{}, notRoots: map[string]bool{}}
}

func (p *Probe) knownNotRoot(dir string) bool {
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.notRoots[filepath.Clean(dir)]
}

func (p *Probe) rememberRoot(dir, top string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.roots[filepath.Clean(dir)] = filepath.Clean(top)
}

func (p *Probe) root(dir string) (string, bool) {
	if p == nil {
		return "", false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	top, ok := p.roots[filepath.Clean(dir)]
	return top, ok
}

// markNotRoots records dir and every parent below top as "not a work-tree
// root". Nothing is recorded unless top is actually reached by file identity.
func (p *Probe) markNotRoots(dir, top string) {
	if p == nil {
		return
	}
	var below []string
	for cur := filepath.Clean(dir); ; {
		if samePath(cur, top) {
			break
		}
		below = append(below, cur)
		parent := filepath.Dir(cur)
		if parent == cur {
			return // top is not an ancestor; learn nothing
		}
		cur = parent
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, d := range below {
		p.notRoots[d] = true
	}
}
