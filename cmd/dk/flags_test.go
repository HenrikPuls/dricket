// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"strings"
	"testing"
)

func TestParseFlagsInterspersed(t *testing.T) {
	for _, args := range [][]string{
		{"app-1", "--size", "M"},
		{"app-1", "--size=M"},
		{"--size", "M", "app-1"},
	} {
		fs := newFlagSet("claim")
		size := fs.String("size", "S", "size")
		if cerr := parseFlags(fs, args); cerr != nil {
			t.Errorf("parse %v: %v", args, cerr)
			continue
		}
		if *size != "M" || fs.NArg() != 1 || fs.Arg(0) != "app-1" {
			t.Errorf("parse %v: size=%q args=%v", args, *size, fs.Args())
		}
	}
}

func TestParseFlagsBooleanDoesNotConsumeValue(t *testing.T) {
	fs := newFlagSet("add")
	unclaimed := fs.Bool("unclaimed", false, "unclaimed")
	priority := fs.String("priority", "normal", "priority")
	if cerr := parseFlags(fs, []string{"--unclaimed", "--priority", "high"}); cerr != nil {
		t.Fatalf("parse: %v", cerr)
	}
	if !*unclaimed || *priority != "high" {
		t.Fatalf("unclaimed=%v priority=%q", *unclaimed, *priority)
	}
}

func TestParseFlagsErrorsAndTerminator(t *testing.T) {
	for _, args := range [][]string{
		{"app-1", "--size"},
		{"app-1", "--unknown", "x"},
	} {
		fs := newFlagSet("claim")
		fs.String("size", "M", "size")
		if cerr := parseFlags(fs, args); cerr == nil {
			t.Errorf("parse %v succeeded; want usage error", args)
		}
	}

	fs := newFlagSet("search")
	if cerr := parseFlags(fs, []string{"--", "-literal"}); cerr != nil {
		t.Fatalf("terminator: %v", cerr)
	}
	if fs.NArg() != 1 || fs.Arg(0) != "-literal" {
		t.Fatalf("terminator args = %v", fs.Args())
	}
}

func TestCLIClaimAcceptsSizeAfterID(t *testing.T) {
	setupBareCLI(t)
	if code, _, _ := runForTest(t, "add", "--prefix", "app", "-m", "x", "--unclaimed"); code != 0 {
		t.Fatalf("add = %d", code)
	}
	if code, out, errMsg := runForTest(t, "claim", "app-1", "--size", "S"); code != 0 {
		t.Fatalf("claim with trailing size = %d, %q, %q", code, out, errMsg)
	}
}

func TestRunRespectTerminatorForGlobalLookingQueries(t *testing.T) {
	setupBareCLI(t)
	for _, query := range []string{"--version", "--json"} {
		code, out, _ := runForTest(t, "search", "--", query)
		if code != exitError || strings.Contains(out, "version:") || strings.Contains(out, "schema_version") {
			t.Errorf("search -- %s = %d, %q; query was consumed globally", query, code, out)
		}
	}
}

func TestRunDoesNotStealGlobalLookingOptionValues(t *testing.T) {
	setupBareCLI(t)
	if code, _, _ := runForTest(t, "add", "--prefix", "app", "-m", "x"); code != 0 {
		t.Fatal("add failed")
	}
	for _, text := range []string{"--json", "--help"} {
		code, out, errMsg := runForTest(t, "note", "app-1", "-m", text)
		if code != 0 || !strings.Contains(out, "noted on app-1") {
			t.Errorf("note value %s = %d, %q, %q; value was consumed", text, code, out, errMsg)
		}
	}
}

func TestRunStillAcceptsGlobalJSONAfterCommand(t *testing.T) {
	setupBareCLI(t)
	code, out, _ := runForTest(t, "add", "--prefix", "app", "-m", "x", "--unclaimed", "--json")
	if code != 0 || !strings.Contains(out, `"schema_version":1`) {
		t.Fatalf("trailing global JSON = %d, %q", code, out)
	}
}
