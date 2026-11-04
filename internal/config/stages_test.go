// SPDX-License-Identifier: GPL-3.0-or-later
package config

import (
	"strings"
	"testing"

	"github.com/HenrikPuls/dricket/internal/ticket"
)

const driveStages = `
[stages]
values    = ["inbox", "prioritized", "build", "commit", "done"]
initial   = "inbox"
terminal  = ["done"]
claim     = ["build", "commit"]
cancelled = "cancelled"
`

func TestDefaultsKeepTheBuiltInStages(t *testing.T) {
	c := Defaults()
	if !c.Stages.Legacy || !c.Stages.Valid("review") || c.Stages.Initial != "open" {
		t.Fatalf("default stage set = %+v", c.Stages)
	}
	c, err := Parse([]byte("[limits]\nmax_extensions = 1\n"))
	if err != nil || !c.Stages.Legacy {
		t.Fatalf("config without [stages]: %+v, %v", c.Stages, err)
	}
}

func TestParseStages(t *testing.T) {
	c, err := Parse([]byte(driveStages))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	set := c.Stages
	if set.Legacy || set.Initial != "inbox" || len(set.Values) != 5 {
		t.Fatalf("stage set = %+v", set)
	}
	if !set.Valid("build") || !set.Valid("cancelled") || set.Valid("open") {
		t.Error("vocabulary membership wrong")
	}
	if !set.IsTerminal("done") || !set.IsTerminal("cancelled") || set.IsTerminal("build") {
		t.Error("terminal stages wrong")
	}
	if set.AfterClaim("inbox", false) != "build" || set.AfterClaim("commit", false) != "commit" {
		t.Error("AfterClaim wrong")
	}
	if set.Pickable("inbox") || !set.Pickable("build") {
		t.Error("Pickable wrong: only claim stages are offered by next")
	}
	// The old agent table names stages that no longer exist; it is replaced.
	if _, old := c.Transitions["agent"]["open"]; old {
		t.Error("built-in agent transitions leaked into a custom vocabulary")
	}
	if !ticket.CanTransitionIn(set, c.Transitions, ticket.RoleAgent, "build", "commit") {
		t.Error("agent may not move build -> commit")
	}
	if ticket.CanTransitionIn(set, c.Transitions, ticket.RoleAgent, "build", "done") {
		t.Error("agent may move to a terminal stage")
	}
	if !ticket.CanTransitionIn(set, c.Transitions, ticket.RoleHuman, "build", "done") {
		t.Error("human may not move to done")
	}
}

func TestParseStagesRejectsBrokenTables(t *testing.T) {
	cases := map[string]string{
		"no values":        "[stages]\nclaim = [\"a\"]\n",
		"no claim":         "[stages]\nvalues = [\"a\", \"b\"]\n",
		"duplicate":        "[stages]\nvalues = [\"a\", \"a\"]\nclaim = [\"a\"]\n",
		"upper case":       "[stages]\nvalues = [\"A\"]\nclaim = [\"A\"]\n",
		"unknown initial":  "[stages]\nvalues = [\"a\"]\nclaim = [\"a\"]\ninitial = \"x\"\n",
		"unknown terminal": "[stages]\nvalues = [\"a\"]\nclaim = [\"a\"]\nterminal = [\"x\"]\n",
		"unknown claim":    "[stages]\nvalues = [\"a\"]\nclaim = [\"x\"]\n",
		"claim terminal":   "[stages]\nvalues = [\"a\", \"b\"]\nclaim = [\"b\"]\nterminal = [\"b\"]\n",
		"initial terminal": "[stages]\nvalues = [\"a\", \"b\"]\nclaim = [\"b\"]\nterminal = [\"a\"]\n",
		"unknown key":      "[stages]\nvalues = [\"a\"]\nclaim = [\"a\"]\nfoo = 1\n",
	}
	for name, in := range cases {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: parsed without error", name)
		}
	}
}

func TestTransitionsOverlayUsesTheConfiguredVocabulary(t *testing.T) {
	in := driveStages + "\n[transitions.agent]\nbuild = [\"commit\", \"done\"]\n"
	c, err := Parse([]byte(in))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !ticket.CanTransitionIn(c.Stages, c.Transitions, ticket.RoleAgent, "build", "done") {
		t.Error("explicit agent rule ignored")
	}
	_, err = Parse([]byte(driveStages + "\n[transitions.agent]\nbuild = [\"review\"]\n"))
	if err == nil || !strings.Contains(err.Error(), "unknown target stage") {
		t.Errorf("target outside the vocabulary: %v", err)
	}
}
