// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lariscus/dricket/internal/trace"
)

func gitProcesses(t *testing.T) int {
	t.Helper()
	rep, ok := trace.Snapshot()
	if !ok {
		t.Fatal("trace not enabled")
	}
	return rep.GitProcesses
}

func TestLocalReadsLeavePrivateWorktreeAlone(t *testing.T) {
	ctx := context.Background()
	dir := initDiscoveryStore(t, "tickets")
	commitVisibleFile(t, dir, "tickets/app/0000/app-1/ticket.md", "one\n", "one")
	st := openStore(t, dir, "reader", nil)

	trace.Enable(time.Now())
	defer trace.Disable()
	if err := st.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := st.ReadFile(ctx, "tickets/app/0000/app-1/ticket.md")
	if err != nil || got != "one\n" {
		t.Fatalf("ReadFile = %q, %v", got, err)
	}
	if n := gitProcesses(t); n != 1 {
		t.Errorf("Refresh + ReadFile started %d git processes; want exactly 1 (one git show)", n)
	}
	if _, err := st.List(ctx, "tickets/"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReadAll(ctx, "tickets/", "/ticket.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(st.Dir()); err == nil {
		t.Errorf("a read created the private worktree %s", st.Dir())
	}
	if out := runGit(t, dir, "worktree", "list", "--porcelain"); strings.Count(out, "worktree ") != 1 {
		t.Errorf("reads added a worktree:\n%s", out)
	}
}

func TestReadAllMatchesReadFileInTwoProcesses(t *testing.T) {
	ctx := context.Background()
	dir := initDiscoveryStore(t, "tickets")
	want := map[string]string{
		"tickets/app/0000/app-1/ticket.md": "plain\n",
		"tickets/app/0000/app-2/ticket.md": "no trailing newline",
		"tickets/app/0000/app-3/ticket.md": "",
		"tickets/app/0000/app-4/ticket.md": "tabs\tand\n\nblank lines\n\n\n",
		"tickets/app/0000/app-5/ticket.md": "unicode ✓ ünï\n",
		"tickets/app/0000/app-6/notes.txt": "not a ticket\n",
		"tickets/öö/0000/öö-1/ticket.md":   "non-ascii path\n",
	}
	for p, c := range want {
		commitVisibleFile(t, dir, p, c, "add "+filepath.Base(filepath.Dir(p)))
	}
	st := openStore(t, dir, "reader", nil)
	if err := st.Refresh(ctx); err != nil {
		t.Fatal(err)
	}

	trace.Enable(time.Now())
	defer trace.Disable()
	got, err := st.ReadAll(ctx, "tickets/", "/ticket.md")
	if err != nil {
		t.Fatal(err)
	}
	if n := gitProcesses(t); n != 2 {
		t.Errorf("ReadAll started %d git processes; want 2 (ls-tree, cat-file --batch)", n)
	}
	seen := 0
	for _, f := range got {
		single, err := st.ReadFile(ctx, f.Path)
		if err != nil || single != f.Content {
			t.Errorf("%s: ReadAll %q != ReadFile %q (%v)", f.Path, f.Content, single, err)
		}
		if want[f.Path] != f.Content {
			t.Errorf("%s: content %q, want %q", f.Path, f.Content, want[f.Path])
		}
		if !strings.HasSuffix(f.Path, "/ticket.md") {
			t.Errorf("suffix filter let %s through", f.Path)
		}
		seen++
	}
	if seen != 6 {
		t.Errorf("ReadAll returned %d ticket files; want 6", seen)
	}
}

func TestReadsOnAMissingBranchAreEmpty(t *testing.T) {
	ctx := context.Background()
	dir := initDiscoveryStore(t, "tickets")
	st := openStore(t, dir, "reader", func(o *Options) { o.Branch = "does-not-exist" })
	if err := st.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if files, err := st.ReadAll(ctx, "tickets/", "/ticket.md"); err != nil || len(files) != 0 {
		t.Errorf("ReadAll = %v, %v; want empty", files, err)
	}
	if paths, err := st.List(ctx, "tickets/"); err != nil || len(paths) != 0 {
		t.Errorf("List = %v, %v; want empty", paths, err)
	}
	if _, err := st.ReadFile(ctx, "tickets/app/0000/app-1/ticket.md"); !errors.Is(err, ErrNoBranch) {
		t.Errorf("ReadFile error = %v; want ErrNoBranch", err)
	}
	if hits, err := st.Grep(ctx, "x", ""); err != nil || len(hits) != 0 {
		t.Errorf("Grep = %v, %v; want empty", hits, err)
	}
	if commits, err := st.Commits(ctx); err != nil || len(commits) != 0 {
		t.Errorf("Commits = %v, %v; want empty", commits, err)
	}
}

func TestReadThenWriteThenReadSeesTheWrite(t *testing.T) {
	ctx := context.Background()
	dir := initDiscoveryStore(t, "tickets")
	st := openStore(t, dir, "rw", nil)
	path := "tickets/app/0000/app-1/ticket.md"
	if err := st.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReadFile(ctx, path); !errors.Is(err, ErrNotExist) {
		t.Fatalf("before the write: %v; want ErrNotExist", err)
	}
	if err := st.Write(ctx, writeFileSpec(path, "written\n", nil)); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := st.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := st.ReadFile(ctx, path); err != nil || got != "written\n" {
		t.Errorf("after the write: %q, %v", got, err)
	}
	if files, err := st.ReadAll(ctx, "tickets/", "/ticket.md"); err != nil || len(files) != 1 {
		t.Errorf("ReadAll after write = %v, %v", files, err)
	}
}

func TestPinHoldsOneStateWhileTheBranchMoves(t *testing.T) {
	ctx := context.Background()
	dir := initDiscoveryStore(t, "tickets")
	path := "tickets/app/0000/app-1/ticket.md"
	runGit(t, dir, "switch", "--quiet", "tickets")
	commitVisibleFile(t, dir, path, "before\n", "before")
	st := openStore(t, dir, "pin", nil)
	if err := st.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if err := st.Pin(ctx); err != nil {
		t.Fatal(err)
	}
	commitVisibleFile(t, dir, path, "after\n", "after")
	if got, _ := st.ReadFile(ctx, path); got != "before\n" {
		t.Errorf("pinned read = %q; want before", got)
	}
	if err := st.Refresh(ctx); err != nil {
		t.Fatal(err)
	}
	if got, _ := st.ReadFile(ctx, path); got != "after\n" {
		t.Errorf("read after a new Refresh = %q; want after", got)
	}
}

func TestInspectRootWithProbeSkipsProcesses(t *testing.T) {
	ctx := context.Background()
	dir := initDiscoveryStore(t, "tickets")
	nested := filepath.Join(dir, "tickets", "app", "0000")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatal(err)
	}
	probe := NewProbe()

	trace.Enable(time.Now())
	defer trace.Disable()
	for level := nested; ; level = filepath.Dir(level) {
		d, ok, err := InspectRootWith(ctx, probe, level)
		if err != nil {
			t.Fatal(err)
		}
		if ok {
			if !samePath(d.Dir, dir) {
				t.Fatalf("found %s; want %s", d.Dir, dir)
			}
			break
		}
		if level == filepath.Dir(level) {
			t.Fatal("no store root found")
		}
	}
	// nested (1 rev-parse) and its two parents below the root cost nothing
	// more; the root costs rev-parse + config read + origin lookup.
	if n := gitProcesses(t); n != 4 {
		t.Errorf("walk from %d levels deep started %d git processes; want 4", 3, n)
	}
	if top, ok := probe.root(dir); !ok || !samePath(top, dir) {
		t.Errorf("probe forgot the root: %q %v", top, ok)
	}
	// Open then asks git nothing about the repository.
	before := gitProcesses(t)
	opts := Options{Remote: dir, Branch: "tickets", DataDir: t.TempDir(), Probe: probe, Profile: initOpts(dir).Profile}
	if _, err := Open(ctx, opts); err != nil {
		t.Fatalf("open: %v", err)
	}
	if after := gitProcesses(t); after != before {
		t.Errorf("Open with a probe started %d git processes; want 0", after-before)
	}
	_ = fmt.Sprint
}
