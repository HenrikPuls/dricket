// SPDX-License-Identifier: GPL-3.0-or-later
package ticket

import (
	"strings"
	"testing"
	"time"

	"github.com/HenrikPuls/dricket/internal/frontmatter"
)

func TestExtensionKeyShape(t *testing.T) {
	for _, ok := range []string{"x-persona", "x-a", "x-0", "x-rank-2"} {
		if !ValidExtensionKey(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"x-", "x--a", "X-a", "x-A", "x_a", "persona", "x-a b", ""} {
		if ValidExtensionKey(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
	if cls, known := ClassOf("x-persona"); !known || cls != Extension {
		t.Errorf("ClassOf(x-persona) = %v, %v", cls, known)
	}
	if _, known := ClassOf("x-"); known {
		t.Error("x- is a known field")
	}
}

func TestExtensionFieldsSurviveParseAndRender(t *testing.T) {
	in := strings.Replace(example, "branch: dk/app-6842\n", "branch: dk/app-6842\nx-persona: ben\nfuture: 1\nx-rank: 5000\n", 1)
	tk := parseForTest(t, in)
	if v, ok := tk.Extension("x-persona"); !ok || v != "ben" {
		t.Fatalf("x-persona = %q, %v", v, ok)
	}
	out := tk.ToDoc().Render()
	if !strings.Contains(out, "x-persona: ben\nfuture: 1\nx-rank: 5000\n") {
		t.Fatalf("extension fields not preserved in order:\n%s", out)
	}
	// Only the unknown key is a warning; extensions are known.
	_, unknown, err := FromDoc(mustParse(t, in))
	if err != nil || len(unknown) != 1 || unknown[0] != "future" {
		t.Errorf("unknown = %v, %v", unknown, err)
	}
}

func TestSetExtension(t *testing.T) {
	tk := parseForTest(t, example)
	if !tk.SetExtension("x-a", "1") || !tk.SetExtension("x-b", "2") {
		t.Fatal("new fields reported unchanged")
	}
	if tk.SetExtension("x-a", "1") {
		t.Error("same value reported as change")
	}
	if !tk.SetExtension("x-a", "3") {
		t.Error("changed value reported unchanged")
	}
	if !tk.SetExtension("x-a", "") || tk.SetExtension("x-a", "") {
		t.Error("removal wrong")
	}
	if got := tk.ExtensionFields(); len(got) != 1 || got["x-b"] != "2" {
		t.Errorf("fields = %v", got)
	}
}

func TestLintChecksExtensionValueSize(t *testing.T) {
	tk := parseForTest(t, example)
	tk.SetExtension("x-big", strings.Repeat("a", MaxExtensionValue+1))
	found := false
	for _, f := range Lint(tk) {
		if f.Field == "x-big" && !f.Warning {
			found = true
		}
	}
	if !found {
		t.Error("oversized extension value passed lint")
	}
}

func TestLintWithCustomStages(t *testing.T) {
	set := StageSet{
		Values: []Stage{"inbox", "build", "done"}, Initial: "inbox",
		Terminal: []Stage{"done"}, Claim: []Stage{"build"},
	}
	tk := parseForTest(t, example)
	tk.Stage = "inbox"
	tk.Owner = ""
	tk.ClaimedAt, tk.LeaseUntil = time.Time{}, time.Time{}
	if errs := errorsOf(LintWith(tk, set, true)); len(errs) != 0 {
		t.Errorf("unclaimed inbox ticket: %v", errs)
	}
	tk.Stage = "open"
	if errs := errorsOf(LintWith(tk, set, true)); len(errs) != 1 || !strings.Contains(errs[0], "inbox, build, done") {
		t.Errorf("stage outside the vocabulary: %v", errs)
	}
	if errs := errorsOf(LintWith(tk, set, false)); len(errs) != 0 {
		t.Errorf("replay must not check the vocabulary: %v", errs)
	}
	// An unclaimed ticket may sit in a claim stage (waiting to be picked).
	tk.Stage = "build"
	if errs := errorsOf(LintWith(tk, set, true)); len(errs) != 0 {
		t.Errorf("unclaimed build ticket: %v", errs)
	}
}

func mustParse(t *testing.T, in string) frontmatter.Document {
	t.Helper()
	doc, err := frontmatter.Parse(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

func errorsOf(fs []Finding) []string {
	var out []string
	for _, f := range fs {
		if !f.Warning {
			out = append(out, f.String())
		}
	}
	return out
}
