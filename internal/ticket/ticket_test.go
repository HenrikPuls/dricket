// SPDX-License-Identifier: GPL-3.0-or-later
package ticket

import (
	"strings"
	"testing"
	"time"

	"github.com/HenrikPuls/dricket/internal/frontmatter"
)

// example is the front-matter example of docs/CONCEPT.md.
const example = `---
id: app-6842
created_at: 2026-11-02T09:14:00Z
created_by: alice
schema_version: 1
title: Login error on empty password
stage: in-progress
owner: orch-1.sub-2
kind: agent
size: M
claimed_at: 2026-11-02T10:02:11Z
lease_until: 2026-11-02T18:02:11Z
epoch: 3
rev: 11
extensions: 0
labels: bug, auth
depends: app-6830
branch: dk/app-6842
---

## Description

Text.
`

func parseForTest(t *testing.T, in string) Ticket {
	t.Helper()
	doc, err := frontmatter.Parse(in)
	if err != nil {
		t.Fatalf("frontmatter parse: %v", err)
	}
	tk, _, err := FromDoc(doc)
	if err != nil {
		t.Fatalf("ticket FromDoc: %v", err)
	}
	return tk
}

func TestFromDocExample(t *testing.T) {
	tk := parseForTest(t, example)
	if tk.ID != "app-6842" || tk.Owner != "orch-1.sub-2" || tk.Epoch != 3 || tk.Rev != 11 {
		t.Fatalf("got %+v", tk)
	}
	if tk.Size != SizeM || tk.Kind != KindAgent || tk.Stage != StageInProgress {
		t.Fatalf("got %+v", tk)
	}
	if len(tk.Labels) != 2 || tk.Labels[0] != "bug" || tk.Labels[1] != "auth" {
		t.Errorf("labels = %v", tk.Labels)
	}
	if len(tk.Depends) != 1 || tk.Depends[0] != "app-6830" {
		t.Errorf("depends = %v", tk.Depends)
	}
	want, _ := time.Parse(time.RFC3339, "2026-11-02T18:02:11Z")
	if !tk.LeaseUntil.Equal(want) {
		t.Errorf("lease_until = %v", tk.LeaseUntil)
	}
}

func TestFromDocUnknownKeysAndTypes(t *testing.T) {
	in := strings.Replace(example, "labels: bug, auth", "labels: bug\nfuture_field: x", 1)
	doc, err := frontmatter.Parse(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	tk, unknown, err := FromDoc(doc)
	if err != nil {
		t.Fatalf("FromDoc: %v", err)
	}
	if len(unknown) != 1 || unknown[0] != "future_field" {
		t.Errorf("unknown = %v", unknown)
	}
	if len(tk.Labels) != 1 {
		t.Errorf("labels = %v", tk.Labels)
	}

	bad := strings.Replace(example, "epoch: 3", "epoch: three", 1)
	doc2, _ := frontmatter.Parse(bad)
	if _, _, err := FromDoc(doc2); err == nil || !strings.Contains(err.Error(), "epoch") {
		t.Errorf("err = %v, want an epoch type error", err)
	}
}

func TestLintClean(t *testing.T) {
	tk := parseForTest(t, example)
	if fs := Lint(tk); len(fs) != 0 {
		for _, f := range fs {
			t.Errorf("unexpected finding: %s", f)
		}
	}
}

func TestLintFindings(t *testing.T) {
	cases := []struct {
		name  string
		mut   func(in string) string
		field string
		want  string
	}{
		{"owner without lease", func(s string) string {
			return strings.Replace(s, "lease_until: 2026-11-02T18:02:11Z", "lease_until:", 1)
		}, "lease_until", "required while the ticket is claimed"},
		{"lease without owner", func(s string) string {
			s = strings.Replace(s, "owner: orch-1.sub-2", "owner:", 1)
			s = strings.Replace(s, "claimed_at: 2026-11-02T10:02:11Z", "claimed_at:", 1)
			s = strings.Replace(s, "epoch: 3", "epoch: 0", 1)
			return strings.Replace(s, "lease_until: 2026-11-02T18:02:11Z", "lease_until: 2026-11-02T20:00:00Z", 1)
		}, "lease_until", "set while the ticket is not claimed"},
		{"bad size", func(s string) string { return strings.Replace(s, "size: M", "size: XL", 1) }, "size", "not one of S, M, L"},
		{"bad stage", func(s string) string { return strings.Replace(s, "stage: in-progress", "stage: wip", 1) }, "stage", "not one of"},
		{"missing title", func(s string) string { return strings.Replace(s, "title: Login error on empty password", "title:", 1) }, "title", "required"},
		{"bad id", func(s string) string { return strings.Replace(s, "id: app-6842", "id: App_6842", 1) }, "id", "form prefix-number"},
		{"epoch 0 claimed", func(s string) string { return strings.Replace(s, "epoch: 3", "epoch: 0", 1) }, "epoch", "at least 1"},
		{"in-progress unclaimed", func(s string) string {
			s = strings.Replace(s, "owner: orch-1.sub-2", "owner:", 1)
			s = strings.Replace(s, "claimed_at: 2026-11-02T10:02:11Z", "claimed_at:", 1)
			s = strings.Replace(s, "lease_until: 2026-11-02T18:02:11Z", "lease_until:", 1)
			return strings.Replace(s, "epoch: 3", "epoch: 0", 1)
		}, "stage", "not claimed"},
		{"unknown schema", func(s string) string { return strings.Replace(s, "schema_version: 1", "schema_version: 99", 1) }, "schema_version", "unknown schema version"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tk := parseForTest(t, c.mut(example))
			found := false
			for _, f := range Lint(tk) {
				if f.Field == c.field && strings.Contains(f.Message, c.want) {
					found = true
				}
			}
			if !found {
				t.Fatalf("no finding on %s containing %q", c.field, c.want)
			}
		})
	}
}

func TestClassOf(t *testing.T) {
	for key, want := range map[string]Class{
		"id": Identity, "created_at": Identity, "schema_version": Identity,
		"owner": State, "epoch": State, "stage": State,
		"title": Free, "labels": Free, "branch": Free,
	} {
		got, ok := ClassOf(key)
		if !ok || got != want {
			t.Errorf("ClassOf(%q) = %v, %t; want %v, true", key, got, ok, want)
		}
	}
	if _, ok := ClassOf("nope"); ok {
		t.Error("unknown key reported as known")
	}
}

func TestValidID(t *testing.T) {
	for id, ok := range map[string]bool{
		"app-6842": true, "app-7": true, "svc-api-12": true,
		"app": false, "app-": false, "-7": false, "App-7": false, "app_7": false, "app 7": false,
	} {
		if got := ValidID(id); got != ok {
			t.Errorf("ValidID(%q) = %t, want %t", id, got, ok)
		}
	}
}

func TestToDocRoundTrip(t *testing.T) {
	tk := parseForTest(t, example)
	doc := tk.ToDoc()
	tk2, unknown, err := FromDoc(doc)
	if err != nil {
		t.Fatalf("FromDoc(ToDoc): %v", err)
	}
	if len(unknown) != 0 {
		t.Errorf("unknown after round trip: %v", unknown)
	}
	if tk2.ID != tk.ID || tk2.Owner != tk.Owner || tk2.Epoch != tk.Epoch ||
		tk2.Rev != tk.Rev || tk2.Size != tk.Size || tk2.Stage != tk.Stage ||
		!tk2.LeaseUntil.Equal(tk.LeaseUntil) || len(tk2.Labels) != len(tk.Labels) {
		t.Fatalf("round trip changed the ticket: %+v vs %+v", tk, tk2)
	}
	if _, err := frontmatter.Parse(doc.Render()); err != nil {
		t.Fatalf("rendered doc does not parse: %v", err)
	}
}

func TestCanTransition(t *testing.T) {
	cases := []struct {
		role Role
		from Stage
		to   Stage
		ok   bool
	}{
		{RoleAgent, StageOpen, StageInProgress, true},
		{RoleAgent, StageInProgress, StageReview, true},
		{RoleAgent, StageInProgress, StageBlocked, true},
		{RoleAgent, StageReview, StageDone, false},
		{RoleAgent, StageReview, StageCancelled, false},
		{RoleAgent, StageOpen, StageDone, false},
		{RoleHuman, StageReview, StageDone, true},
		{RoleHuman, StageDone, StageOpen, true},
		{RoleAdmin, StageReview, StageDone, true},
		{RoleAgent, StageInProgress, "wip", false},
	}
	for _, c := range cases {
		if got := CanTransition(c.role, c.from, c.to); got != c.ok {
			t.Errorf("CanTransition(%s, %s -> %s) = %t, want %t", c.role, c.from, c.to, got, c.ok)
		}
	}
}
