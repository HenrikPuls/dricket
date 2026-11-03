// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
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
