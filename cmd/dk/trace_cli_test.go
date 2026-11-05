// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/HenrikPuls/dricket/internal/trace"
)

// traceStore creates a local store with n unclaimed tickets, makes it the
// working directory and returns its path.
func traceStore(t *testing.T, n int) string {
	t.Helper()
	zeroEnv(t)
	dir := filepath.Join(t.TempDir(), "store")
	if code, _, errMsg := runForTest(t, "init", dir, "app"); code != 0 {
		t.Fatalf("init = %d, %q", code, errMsg)
	}
	t.Chdir(dir)
	for i := 1; i <= n; i++ {
		if code, _, errMsg := runForTest(t, "add", "-m", fmt.Sprintf("ticket number %d", i), "--unclaimed"); code != 0 {
			t.Fatalf("add %d = %d, %q", i, code, errMsg)
		}
	}
	return dir
}

// TestTraceKeepsStdoutAndExitCodeIdentical is the golden test of R-26 (trace):
// every read command prints the same bytes and exits the same with and
// without --trace / DK_TRACE=1; the trace goes to stderr only.
func TestTraceKeepsStdoutAndExitCodeIdentical(t *testing.T) {
	traceStore(t, 3)
	commands := [][]string{
		{"show", "app-1"},
		{"--json", "show", "app-2"},
		{"show", "app-99"},
		{"--json", "show", "not_an_id"},
		{"ls"},
		{"--json", "ls"},
		{"ls", "-p", "nope"},
		{"search", "number"},
		{"--json", "search", "number"},
		{"search", "no-such-text"},
		{"lint"},
		{"--json", "leases"},
		{"next"},
		{"validate"},
	}
	for _, args := range commands {
		wantCode, wantOut, wantErr := runForTest(t, args...)
		if strings.Contains(wantErr, "trace:") {
			t.Fatalf("dk %v: plain run wrote trace output: %q", args, wantErr)
		}
		withFlag := append([]string{"--trace"}, args...)
		code, out, errOut := runForTest(t, withFlag...)
		if code != wantCode || out != wantOut {
			t.Errorf("dk %v: --trace changed the result: exit %d vs %d, stdout %q vs %q", args, code, wantCode, out, wantOut)
		}
		if !hasSummary(errOut) {
			t.Errorf("dk %v: --trace wrote no summary: %q", args, errOut)
		}

		t.Setenv("DK_TRACE", "1")
		code, out, errOut = runForTest(t, args...)
		t.Setenv("DK_TRACE", "")
		if code != wantCode || out != wantOut {
			t.Errorf("dk %v: DK_TRACE changed the result: exit %d vs %d", args, code, wantCode)
		}
		if !hasSummary(errOut) {
			t.Errorf("dk %v: DK_TRACE wrote no summary: %q", args, errOut)
		}
		_ = wantErr
	}
}

// TestTraceJSONFormGoesToStderr: with --json the trace is one JSON object on
// stderr and stdout stays the data envelope.
func TestTraceJSONFormGoesToStderr(t *testing.T) {
	traceStore(t, 1)
	_, plain, _ := runForTest(t, "--json", "show", "app-1")
	code, out, errOut := runForTest(t, "--json", "--trace", "show", "app-1")
	if code != 0 || out != plain {
		t.Fatalf("stdout changed: exit %d, %q vs %q", code, out, plain)
	}
	var rep trace.Report
	if err := json.Unmarshal([]byte(strings.TrimSpace(errOut)), &rep); err != nil {
		t.Fatalf("stderr is not one JSON object: %v in %q", err, errOut)
	}
	if rep.SchemaVersion != trace.SchemaVersion || rep.GitProcesses == 0 || len(rep.Steps) == 0 {
		t.Fatalf("report = %+v", rep)
	}
	names := map[string]bool{}
	for _, s := range rep.Steps {
		names[s.Name] = true
	}
	for _, want := range []string{"discover store", "open store", "refresh head"} {
		if !names[want] {
			t.Errorf("step %q missing from %v", want, names)
		}
	}
}

// TestTraceShowsEveryStepOfAWrite: a write names its attempt phases and
// the process count in the summary matches the processes listed.
func TestTraceShowsEveryStepOfAWrite(t *testing.T) {
	traceStore(t, 0)
	code, _, errOut := runForTest(t, "--trace", "add", "-m", "traced add", "--unclaimed")
	if code != 0 {
		t.Fatalf("add = %d, %q", code, errOut)
	}
	for _, want := range []string{"attempt 1: refresh private copy", "attempt 1: commit", "attempt 1: publish", "git commit", "git: "} {
		if !strings.Contains(errOut, want) {
			t.Errorf("trace lacks %q:\n%s", want, errOut)
		}
	}
	if strings.Contains(errOut, "traced add") {
		t.Errorf("trace leaked the commit message / ticket title:\n%s", errOut)
	}
}

func readProcesses(t *testing.T, args ...string) int {
	t.Helper()
	code, _, errOut := runForTest(t, append([]string{"--json", "--trace"}, args...)...)
	if code != 0 {
		t.Fatalf("dk %v = %d, %q", args, code, errOut)
	}
	var rep trace.Report
	if err := json.Unmarshal([]byte(strings.TrimSpace(errOut)), &rep); err != nil {
		t.Fatalf("trace json: %v in %q", err, errOut)
	}
	return rep.GitProcesses
}

// TestReadProcessBudget pins R-27: a read costs discovery (one rev-parse,
// the config read, the origin lookup) plus its own reads, and `ls` costs the
// same however many tickets there are. No private working copy is touched.
func TestReadProcessBudget(t *testing.T) {
	dir := traceStore(t, 2)
	few := readProcesses(t, "ls")
	for i := 3; i <= 12; i++ {
		if code, _, errMsg := runForTest(t, "add", "-m", fmt.Sprintf("more %d", i), "--unclaimed"); code != 0 {
			t.Fatalf("add = %d, %q", code, errMsg)
		}
	}
	many := readProcesses(t, "ls")
	if few != many {
		t.Errorf("ls process count depends on ticket count: %d with 2 tickets, %d with 12", few, many)
	}
	if many > 5 {
		t.Errorf("ls started %d git processes; want at most 5", many)
	}
	if got := readProcesses(t, "show", "app-1"); got > 4 {
		t.Errorf("show started %d git processes; want at most 4", got)
	}
	_ = dir
}

// TestParallelReadsDoNotFail is the regression test of the parallel-read
// failure (`reset to local head: exit status 128`): several real dk processes
// read one store at the same time, repeatedly. DK_TEST_BIN selects a binary
// (to prove the test fails on an old build); by default it builds one.
func TestParallelReadsDoNotFail(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and starts real processes")
	}
	pkgDir, _ := os.Getwd()
	traceStore(t, 3)
	store, _ := os.Getwd()
	bin := os.Getenv("DK_TEST_BIN")
	if bin == "" {
		bin = filepath.Join(t.TempDir(), "dk-under-test.exe")
		build := exec.Command("go", "build", "-o", bin, ".") // #nosec G204 G702 -- fixed test build of this package
		build.Dir = pkgDir
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("go build: %v\n%s", err, out)
		}
	}
	dataDir := os.Getenv("DK_DATA_DIR")
	const readers, rounds = 8, 3
	for round := 0; round < rounds; round++ {
		var wg sync.WaitGroup
		errs := make(chan string, readers)
		for i := 0; i < readers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				args := [][]string{{"show", "app-1"}, {"ls"}, {"--json", "ls"}, {"search", "ticket"}}[i%4]
				cmd := exec.Command(bin, args...) // #nosec G204 G702 -- binary built by this test
				cmd.Dir = store
				cmd.Env = append(os.Environ(), "DK_DATA_DIR="+dataDir, "DK_OWNER=henrik", "DK_STORE=", "DK_PROJECT=")
				if out, err := cmd.CombinedOutput(); err != nil {
					errs <- fmt.Sprintf("dk %v: %v: %s", args, err, strings.TrimSpace(string(out)))
				}
			}(i)
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			t.Errorf("round %d: %s", round, e)
		}
	}
}

// hasSummary accepts the text summary line or the JSON form's totals.
func hasSummary(stderr string) bool {
	return strings.Contains(stderr, " processes, ") || strings.Contains(stderr, `"git_processes":`)
}
