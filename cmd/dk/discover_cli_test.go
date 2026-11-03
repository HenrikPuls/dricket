// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// zeroEnv clears every store/project source so a test only passes when
// discovery and bindings actually work.
func zeroEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DK_STORE", "")
	t.Setenv("DK_PROJECT", "")
	t.Setenv("DK_OWNER", "henrik")
	t.Setenv("DK_DATA_DIR", t.TempDir())
}

func TestCLIAddInsideStoreWithoutAnyFlags(t *testing.T) {
	zeroEnv(t)
	dir := filepath.Join(t.TempDir(), "store")
	if code, _, errMsg := runForTest(t, "init", dir, "app"); code != 0 {
		t.Fatalf("init = %d, %q", code, errMsg)
	}

	// From the store root: no --store, no DK_STORE, no --prefix.
	t.Chdir(dir)
	code, out, errMsg := runForTest(t, "add", "-c", "zero config")
	if code != 0 || !strings.Contains(out, "app-1") {
		t.Fatalf("add = %d, %q, %q", code, out, errMsg)
	}

	// From a nested subdirectory the walk-up still finds the store.
	nested := filepath.Join(dir, "tickets", "app", "0000")
	if err := os.MkdirAll(nested, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Chdir(nested)
	code, out, _ = runForTest(t, "add", "-c", "from deep inside", "--unclaimed")
	if code != 0 || !strings.Contains(out, "app-2") {
		t.Fatalf("nested add = %d, %q", code, out)
	}
}

func TestCLIDiscoveryHonoursBranchConfig(t *testing.T) {
	zeroEnv(t)
	dir := filepath.Join(t.TempDir(), "store")
	code, _, errMsg := runForTest(t, "init", "--branch", "work", dir, "app")
	if code != 0 {
		t.Fatalf("init = %d, %q", code, errMsg)
	}
	t.Chdir(dir)
	code, out, _ := runForTest(t, "add", "-c", "on the work branch")
	if code != 0 || !strings.Contains(out, "app-1") {
		t.Fatalf("add = %d, %q", code, out)
	}
}

func TestCLIUseBindsCodeRepo(t *testing.T) {
	zeroEnv(t)
	storeDir := filepath.Join(t.TempDir(), "store")
	if code, _, errMsg := runForTest(t, "init", storeDir, "app"); code != 0 {
		t.Fatalf("init = %d, %q", code, errMsg)
	}
	codeRepo := t.TempDir()
	t.Chdir(codeRepo)

	// Bind the store, then the project.
	if code, out, errMsg := runForTest(t, "use", "--store", storeDir+"#tickets"); code != 0 ||
		!strings.Contains(out, "bound store") {
		t.Fatalf("use --store = %d, %q, %q", code, out, errMsg)
	}
	if code, out, _ := runForTest(t, "use", "app"); code != 0 || !strings.Contains(out, "bound prefix app") {
		t.Fatalf("use app = %d, %q", code, out)
	}
	content, err := os.ReadFile(filepath.Join(codeRepo, ".dricket")) // #nosec G304 -- test scaffolding in t.TempDir
	if err != nil || !strings.Contains(string(content), "store") || !strings.Contains(string(content), `"app"`) {
		t.Fatalf(".dricket = %q, %v", content, err)
	}

	// `dk add` in the code repo lands in the bound store.
	code, out, errMsg := runForTest(t, "add", "-c", "from the code repo", "--unclaimed")
	if code != 0 || !strings.Contains(out, "app-1") {
		t.Fatalf("add from code repo = %d, %q, %q", code, out, errMsg)
	}
	// setup-agent resolves through the binding as well.
	code, out, _ = runForTest(t, "setup-agent")
	if code != 0 || !strings.Contains(out, "wrote the store pointer") {
		t.Fatalf("setup-agent = %d, %q", code, out)
	}

	// The ticket really is in the bound store.
	t.Chdir(storeDir)
	if code, out, _ = runForTest(t, "ls"); code != 0 || !strings.Contains(out, "app-1") {
		t.Fatalf("ls in store = %d, %q", code, out)
	}
}

func TestCLIPrefixPrecedence(t *testing.T) {
	zeroEnv(t)
	dir := filepath.Join(t.TempDir(), "store")
	runForTest(t, "init", dir, "app")
	t.Chdir(dir)

	// 1. --prefix beats everything.
	code, out, _ := runForTest(t, "add", "-c", "explicit", "--unclaimed", "--prefix", "ops")
	if code != 0 || !strings.Contains(out, "ops-1") {
		t.Fatalf("explicit prefix = %d, %q", code, out)
	}
	// 2. DK_PROJECT beats bindings.
	t.Setenv("DK_PROJECT", "ops")
	if code, out, _ = runForTest(t, "add", "-c", "from env", "--unclaimed"); code != 0 || !strings.Contains(out, "ops-2") {
		t.Fatalf("env prefix = %d, %q", code, out)
	}
	// 3. binding: dk use ops inside the store.
	if code, _, _ = runForTest(t, "use", "ops"); code != 0 {
		t.Fatalf("use ops failed")
	}
	if code, out, _ = runForTest(t, "add", "-c", "from binding", "--unclaimed"); code != 0 || !strings.Contains(out, "ops-3") {
		t.Fatalf("binding prefix = %d, %q", code, out)
	}
	// 4. no source at all is impossible here: the store has exactly one
	// registered prefix, so it auto-resolves (covered by the
	// empty-register store in TestCLIUsePrintsResolutionAndErrors).
}

func TestCLIUsePrintsResolutionAndErrors(t *testing.T) {
	zeroEnv(t)
	empty := t.TempDir()
	t.Chdir(empty)

	// Resolution without any store prints placeholders.
	code, out, _ := runForTest(t, "use")
	if code != 0 || !strings.Contains(out, "store:  -") {
		t.Fatalf("use (nothing) = %d, %q", code, out)
	}

	// add outside any store fails with the store hint.
	code, _, errMsg := runForTest(t, "add", "-c", "x")
	if code != exitUsage || !strings.Contains(errMsg, "no ticket store found") {
		t.Fatalf("add outside store = %d, %q; want exit 2 with store hint", code, errMsg)
	}

	// A store with an empty prefix register: add fails with the prefix
	// hint, and `dk use app` fixes it.
	dir := filepath.Join(empty, "store")
	if code, _, _ = runForTest(t, "init", dir); code != 0 {
		t.Fatalf("plain init failed")
	}
	t.Chdir(dir)
	code, _, errMsg = runForTest(t, "add", "-c", "no prefix yet")
	if code != exitUsage || !strings.Contains(errMsg, "no project prefix") {
		t.Fatalf("add without prefix = %d, %q; want exit 2 with prefix hint", code, errMsg)
	}
	if code, _, _ = runForTest(t, "use", "app"); code != 0 {
		t.Fatalf("use app failed")
	}
	if code, out, _ = runForTest(t, "add", "-c", "bound now"); code != 0 || !strings.Contains(out, "app-1") {
		t.Fatalf("add after use = %d, %q", code, out)
	}

	// A prefix is normalised to lower case (like every id), and an
	// invalid name is refused.
	t.Chdir(empty)
	if code, out, _ = runForTest(t, "use", "App"); code != 0 || !strings.Contains(out, `bound prefix app`) {
		t.Fatalf("use App = %d, %q; want normalised binding", code, out)
	}
	if code, _, _ = runForTest(t, "use", "app_"); code != exitUsage {
		t.Fatalf("use app_ = %d; want exit 2", code)
	}
}
