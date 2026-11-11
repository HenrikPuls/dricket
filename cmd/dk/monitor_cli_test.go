// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lariscus/dricket/internal/frontmatter"
	"github.com/lariscus/dricket/internal/profile"
	"github.com/lariscus/dricket/internal/store"
	"github.com/lariscus/dricket/internal/ticket"
)

// expireLease rewrites a ticket's lease into the past, as if its runner
// had died long ago.
func expireLease(t *testing.T, bare, id string) {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(ctx, store.Options{
		Remote:  bare,
		Branch:  "tickets",
		DataDir: t.TempDir(),
		Profile: profile.Profile{Name: "exp", OwnerID: "expirer", GitName: "exp", GitEmail: "exp@invalid"},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	path := "tickets/app/0000/" + id + "/ticket.md"
	spec := store.WriteSpec{
		Message: "test: expire lease",
		Mutate: func(w store.Worktree) error {
			content, err := w.ReadFile(path)
			if err != nil {
				return err
			}
			doc, err := frontmatter.Parse(content)
			if err != nil {
				return err
			}
			tk, _, err := ticket.FromDoc(doc)
			if err != nil {
				return err
			}
			tk.LeaseUntil = time.Now().Add(-2 * time.Hour)
			return w.WriteFile(path, tk.ToDoc().Render())
		},
	}
	if err := st.Write(ctx, spec); err != nil {
		t.Fatalf("expire: %v", err)
	}
}

func TestCLIExtendAndLeases(t *testing.T) {
	bare := setupBareCLI(t)

	code, _, errMsg := runForTest(t, "add", "--prefix", "app", "-m", "long task")
	if code != 0 {
		t.Fatalf("add = %d, %q", code, errMsg)
	}

	code, out, errMsg := runForTest(t, "extend", "--message", "tests take longer", "app-1")
	if code != 0 || !strings.Contains(out, "extended app-1 (extensions 1/3") {
		t.Fatalf("extend = %d, %q, %q", code, out, errMsg)
	}

	code, _, errMsg = runForTest(t, "extend", "app-1")
	if code != exitUsage {
		t.Fatalf("extend without reason = %d, %q; want 2", code, errMsg)
	}

	code, out, errMsg = runForTest(t, "leases")
	if code != 0 || !strings.Contains(out, "app-1") || !strings.Contains(out, "ok") {
		t.Fatalf("leases = %d, %q, %q", code, out, errMsg)
	}

	code, _, _ = runForTest(t, "leases", "--check")
	if code != 0 {
		t.Fatalf("leases --check (healthy) = %d; want 0", code)
	}

	expireLease(t, bare, "app-1")

	code, out, _ = runForTest(t, "leases", "--stale")
	if code != 0 || !strings.Contains(out, "app-1") || !strings.Contains(out, "expired") {
		t.Fatalf("leases --stale = %d, %q", code, out)
	}

	code, _, errMsg = runForTest(t, "leases", "--check")
	if code != exitLeasesExpired || !strings.Contains(errMsg, "expired lease") {
		t.Fatalf("leases --check (expired) = %d, %q; want 20", code, errMsg)
	}
}

func TestCLINoteInbox(t *testing.T) {
	setupBareCLI(t)
	runForTest(t, "add", "--prefix", "app", "-m", "watched task")

	code, out, errMsg := runForTest(t, "note", "-m", "root cause found", "app-1")
	if code != 0 || !strings.Contains(out, "noted on app-1 (rev 2, claim kept)") {
		t.Fatalf("note = %d, %q, %q", code, out, errMsg)
	}

	code, out, errMsg = runForTest(t, "note", "app-1", "-m", "second observation")
	if code != 0 || !strings.Contains(out, "noted on app-1") {
		t.Fatalf("note = %d, %q, %q", code, out, errMsg)
	}

	code, out, _ = runForTest(t, "inbox", "app-1")
	if code != 0 || !strings.Contains(out, "first sight") {
		t.Fatalf("inbox first = %d, %q", code, out)
	}

	runForTest(t, "note", "app-1", "-m", "third observation")
	code, out, _ = runForTest(t, "inbox", "app-1")
	if code != 0 || !strings.Contains(out, "note app-1: third observation") {
		t.Fatalf("inbox second = %d, %q", code, out)
	}
}

func TestCLIDoctorOk(t *testing.T) {
	setupBareCLI(t)
	code, out, errMsg := runForTest(t, "doctor")
	if code != 0 {
		t.Fatalf("doctor = %d, %q; checks:\n%s", code, errMsg, out)
	}
	for _, want := range []string{"ok   git", "ok   identity", "ok   store", "ok   remote", "ok   push", "ok   state"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output misses %q:\n%s", want, out)
		}
	}
}

func TestCLIDoctorWithoutStore(t *testing.T) {
	t.Setenv("DK_STORE", "")
	t.Setenv("DK_DATA_DIR", t.TempDir())
	t.Setenv("DK_OWNER", "someone")
	code, out, _ := runForTest(t, "doctor")
	if code != exitError {
		t.Fatalf("doctor without store = %d; want 1", code)
	}
	if !strings.Contains(out, "FAIL store") {
		t.Errorf("doctor output = %q", out)
	}
}

func TestCLIIsolatesWorkingCopiesPerOwner(t *testing.T) {
	setupBareCLI(t)
	dataDir := filepath.Join(t.TempDir(), "shared")
	t.Setenv("DK_DATA_DIR", dataDir)

	// Same store, two owners: both must work concurrently.
	t.Setenv("DK_OWNER", "alice")
	code, _, errMsg := runForTest(t, "add", "--prefix", "app", "-m", "from alice", "--unclaimed")
	if code != 0 {
		t.Fatalf("alice add = %d, %q", code, errMsg)
	}
	t.Setenv("DK_OWNER", "bob")
	code, _, errMsg = runForTest(t, "claim", "app-1")
	if code != 0 {
		t.Fatalf("bob claim = %d, %q", code, errMsg)
	}
	t.Setenv("DK_OWNER", "alice")
	code, out, _ := runForTest(t, "show", "app-1")
	if code != 0 || !strings.Contains(out, "owner: bob") {
		t.Fatalf("alice sees bob's claim: %d, %q", code, out)
	}
}
