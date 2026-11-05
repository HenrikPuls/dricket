// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/HenrikPuls/dricket/internal/config"
	"github.com/HenrikPuls/dricket/internal/service"
	"github.com/HenrikPuls/dricket/internal/store"
	"github.com/HenrikPuls/dricket/internal/ticket"
	"github.com/HenrikPuls/dricket/internal/trace"
)

// bindings is one `.dricket` file: local bindings for the directory,
// written by `dk use`. It lives in a code repository (which ticket
// store and project it works on) or in a ticket store (project default).
type bindings struct {
	Store  string `toml:"store"`  // remote[#branch]
	Prefix string `toml:"prefix"` // project prefix
}

// readBindings loads the .dricket file of dir; ok is false when it is
// absent or unreadable (a broken file is treated as absent - bindings
// are local, disposable state).
func readBindings(dir string) (bindings, bool) {
	data, err := os.ReadFile(filepath.Join(dir, ".dricket")) // #nosec G304 -- fixed name in the inspected directory
	if err != nil {
		return bindings{}, false
	}
	var b bindings
	if _, derr := toml.Decode(string(data), &b); derr != nil {
		return bindings{}, false
	}
	return b, true
}

// writeBindings merges the bindings into dir's .dricket file, keeping
// keys the call did not touch.
func writeBindings(dir string, b bindings) error {
	existing, _ := readBindings(dir)
	if b.Store == "" {
		b.Store = existing.Store
	}
	if b.Prefix == "" {
		b.Prefix = existing.Prefix
	}
	var buf strings.Builder
	buf.WriteString("# dricket bindings for this directory (written by dk use).\n")
	buf.WriteString("# Commit it for a team convention, or git-ignore it when it carries\n")
	buf.WriteString("# machine-specific paths.\n")
	if b.Store != "" {
		fmt.Fprintf(&buf, "store  = %q\n", b.Store)
	}
	if b.Prefix != "" {
		fmt.Fprintf(&buf, "prefix = %q\n", b.Prefix)
	}
	return os.WriteFile(filepath.Join(dir, ".dricket"), []byte(buf.String()), 0o600) // #nosec G304 -- fixed name in the cwd
}

// resolveProject walks up from the working directory once per process
// and records the nearest `.dricket` bindings and the nearest store
// root. Explicit sources (--store, DK_STORE) are handled by the caller
// and always win.
func (e *env) resolveProject(ctx context.Context) *cliError {
	e.projMu.Lock()
	defer e.projMu.Unlock()
	if e.projDone {
		return e.projErr
	}
	e.projDone = true
	defer trace.Begin("discover store")()
	if e.probe == nil {
		e.probe = store.NewProbe()
	}

	abs, err := filepath.Abs(".")
	if err != nil {
		e.projErr = &cliError{code: exitError, kind: "error", msg: err.Error()}
		return e.projErr
	}
	for level := abs; ; level = filepath.Dir(level) {
		if b, ok := readBindings(level); ok {
			if e.projSpec == "" && b.Store != "" {
				e.projSpec = b.Store
				e.projSpecSrc = filepath.Join(level, ".dricket")
			}
			if e.projPrefix == "" && b.Prefix != "" {
				e.projPrefix = b.Prefix
				e.projPrefixSrc = filepath.Join(level, ".dricket")
			}
		}
		if e.projSpec == "" {
			if d, ok, derr := store.InspectRootWith(ctx, e.probe, level); derr == nil && ok {
				e.projSpec = d.Spec()
				e.projSpecSrc = filepath.Join(d.Dir, "dricket.toml") + " (store root)"
			}
		}
		if (e.projSpec != "" && e.projPrefix != "") || level == filepath.Dir(level) {
			break
		}
	}
	return nil
}

// storeResolution returns the store spec for this run: --store, then
// DK_STORE, then the walked-up bindings/store root.
func (e *env) storeResolution(ctx context.Context) (spec, source string, cerr *cliError) {
	if e.store != "" {
		return e.store, "--store", nil
	}
	if env := os.Getenv("DK_STORE"); env != "" {
		return env, "DK_STORE", nil
	}
	if cerr := e.resolveProject(ctx); cerr != nil {
		return "", "", cerr
	}
	if e.projSpec != "" {
		return e.projSpec, e.projSpecSrc, nil
	}
	return "", "", nil
}

// noStoreHint is the error when no store resolves.
const noStoreHint = "no ticket store found - run dk init <dir> in the store, bind it with dk use --store <remote>#<branch>, or set --store/DK_STORE"

// prefixForAdd resolves the project prefix for `dk add`: --prefix, then
// DK_PROJECT, then bindings, then the single registered prefix (that
// step needs the store; pass nil to skip it, e.g. in `dk use`). An
// empty result means nothing resolved; the caller reports the hint.
func (e *env) prefixForAdd(ctx context.Context, flagPrefix string, s *service.Service) (prefix, source string, cerr *cliError) {
	if flagPrefix != "" {
		return strings.ToLower(strings.TrimSpace(flagPrefix)), "--prefix", nil
	}
	if env := os.Getenv("DK_PROJECT"); env != "" {
		return strings.ToLower(env), "DK_PROJECT", nil
	}
	if cerr := e.resolveProject(ctx); cerr != nil {
		return "", "", cerr
	}
	if e.projPrefix != "" {
		return e.projPrefix, e.projPrefixSrc, nil
	}
	if s == nil {
		return "", "", nil
	}
	// The store knows its prefixes: exactly one registered means no
	// ambiguity.
	data, err := s.Store.ConfigFile(ctx, "prefixes.toml")
	if err != nil {
		return "", "", &cliError{code: exitError, kind: "error", msg: err.Error()}
	}
	registered, perr := config.ParsePrefixes(data)
	if perr != nil {
		return "", "", &cliError{code: exitInvalid, kind: "prefixes", msg: "prefixes.toml: " + perr.Error()}
	}
	if len(registered) == 1 {
		return registered[0], "prefixes.toml (single registered prefix)", nil
	}
	return "", "", nil
}

// noPrefixHint is the error when no prefix resolves for add.
const noPrefixHint = "no project prefix - use --prefix/-p, set DK_PROJECT, or run dk use <prefix>"

// ----------------------------------------------------------------- use --

func runUse(e *env, args []string) (any, *cliError) {
	if len(args) > 1 {
		return nil, usageError("use: at most one prefix expected")
	}
	cwd, _ := os.Getwd()

	// `dk use --store <remote>#<branch>`: the global --store flag is
	// consumed by the global parser and lands in e.store - use it to
	// write the binding.
	if e.store != "" {
		bound := normalizeSpec(e.store)
		if err := writeBindings(cwd, bindings{Store: bound}); err != nil {
			return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
		}
		if !e.json {
			e.outf("bound store %s in %s\n", bound, filepath.Join(cwd, ".dricket"))
		}
		return map[string]any{"bound": "store", "value": bound, "file": filepath.Join(cwd, ".dricket")}, nil
	}

	// A prefix argument binds the project.
	if len(args) == 1 {
		prefix := strings.ToLower(strings.TrimSpace(args[0]))
		if !ticket.ValidPrefix(prefix) {
			return nil, usageError("use: %q is not a valid prefix (lower case letters, digits, dashes)", args[0])
		}
		if err := writeBindings(cwd, bindings{Prefix: prefix}); err != nil {
			return nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
		}
		if !e.json {
			e.outf("bound prefix %s in %s\n", prefix, filepath.Join(cwd, ".dricket"))
		}
		return map[string]any{"bound": "prefix", "value": prefix, "file": filepath.Join(cwd, ".dricket")}, nil
	}

	// No arguments: print the current resolution.
	ctx := context.Background()
	spec, source, cerr := e.storeResolution(ctx)
	if cerr != nil {
		return nil, cerr
	}
	var s *service.Service
	if spec != "" {
		s, cerr = e.svc()
		if cerr != nil {
			return nil, cerr
		}
	}
	prefix, prefixSrc, perr := e.prefixForAdd(ctx, "", s)
	if perr != nil {
		return nil, perr
	}
	if spec == "" {
		spec = "-"
		source = "not found"
	}
	if prefix == "" {
		prefix = "-"
		prefixSrc = "not bound"
	}
	if !e.json {
		e.outf("store:  %s (%s)\n", spec, source)
		e.outf("prefix: %s (%s)\n", prefix, prefixSrc)
	}
	return map[string]any{"store": spec, "store_source": source,
		"prefix": prefix, "prefix_source": prefixSrc}, nil
}

// normalizeSpec cleans the remote part of a store spec; local paths get
// filepath.Clean, URLs and scp-style remotes pass through unchanged.
func normalizeSpec(spec string) string {
	if strings.Contains(spec, "://") || strings.HasPrefix(spec, "git@") {
		return spec
	}
	remote, branch, has := strings.Cut(spec, "#")
	remote = filepath.Clean(remote)
	if has {
		return remote + "#" + branch
	}
	return remote
}

// sameSpec reports whether two store specs name the same remote and branch.
// Local remotes are compared by file identity, because Git reports canonical
// paths while the caller may hold another spelling (Windows 8.3 short names).
func sameSpec(a, b string) bool {
	if normalizeSpec(a) == normalizeSpec(b) {
		return true
	}
	ra, ba, _ := strings.Cut(a, "#")
	rb, bb, _ := strings.Cut(b, "#")
	if ba != bb || strings.Contains(ra, "://") || strings.Contains(rb, "://") ||
		strings.HasPrefix(ra, "git@") || strings.HasPrefix(rb, "git@") {
		return false
	}
	ia, erra := os.Stat(ra)
	ib, errb := os.Stat(rb)
	return erra == nil && errb == nil && os.SameFile(ia, ib)
}
