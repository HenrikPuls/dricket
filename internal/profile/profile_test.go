// SPDX-License-Identifier: GPL-3.0-or-later
package profile

import (
	"strings"
	"testing"
)

func TestDefault(t *testing.T) {
	p, err := Default()
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if p.OwnerID == "" || !strings.Contains(p.OwnerID, "@") {
		t.Errorf("owner id = %q, want user@host", p.OwnerID)
	}
	if p.OwnerID != strings.ToLower(p.OwnerID) {
		t.Errorf("owner id %q is not lower case", p.OwnerID)
	}
	if err := p.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}
}

func TestValidateRequiresEverything(t *testing.T) {
	cases := map[string]Profile{
		"no owner": {Name: "n", GitName: "a", GitEmail: "a@x"},
		"no name":  {Name: "n", OwnerID: "o", GitEmail: "a@x"},
		"no email": {Name: "n", OwnerID: "o", GitName: "a"},
	}
	for name, p := range cases {
		if err := p.Validate(); err == nil {
			t.Errorf("%s: validation passed", name)
		}
	}
}

func TestEnvCarriesIdentity(t *testing.T) {
	p := Profile{Name: "n", OwnerID: "orch-1.sub-2", GitName: "Orch One", GitEmail: "orch@x"}
	env := p.Env()
	joined := strings.Join(env, "\n")
	for _, want := range []string{
		"GIT_AUTHOR_NAME=Orch One",
		"GIT_AUTHOR_EMAIL=orch@x",
		"GIT_COMMITTER_NAME=Orch One",
		"GIT_COMMITTER_EMAIL=orch@x",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("env missing %q in %v", want, env)
		}
	}
}
