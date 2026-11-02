// SPDX-License-Identifier: GPL-3.0-or-later
package profile

import (
	"os"
	"path/filepath"
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
	env := p.Env("")
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
	if strings.Contains(joined, "GIT_SSH_COMMAND") || strings.Contains(joined, "GIT_CONFIG_GLOBAL") {
		t.Errorf("plain profile must not set ssh or isolation: %v", env)
	}
}

func TestEnvSSHKeyAndIsolation(t *testing.T) {
	p := Profile{Name: "runner", OwnerID: "orch-1.sub-2", GitName: "r", GitEmail: "r@x",
		SSHKey: `C:/keys/id_ed25519`}
	env := p.Env("C:/data/empty.gitconfig")
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, `GIT_SSH_COMMAND=ssh -i C:/keys/id_ed25519 -o IdentitiesOnly=yes`) {
		t.Errorf("env missing ssh command: %v", env)
	}
	if !strings.Contains(joined, "GIT_CONFIG_GLOBAL=C:/data/empty.gitconfig") {
		t.Errorf("env missing isolation: %v", env)
	}
}

func TestResolveFromUserConfig(t *testing.T) {
	// os.UserConfigDir reads APPDATA (Windows) / XDG_CONFIG_HOME (Unix).
	dir := t.TempDir()
	t.Setenv("APPDATA", dir)
	t.Setenv("XDG_CONFIG_HOME", dir)

	if _, err := Resolve("orch-1"); err == nil {
		t.Fatal("missing config file must be an error for an explicit name")
	}

	cfgDir := filepath.Join(dir, "dricket")
	if err := os.MkdirAll(cfgDir, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := `
[profile.orch-1]
owner = "orch-1.sub-2"
git_name = "Orch One"
git_email = "orch-1@invalid"
ssh_key = "/keys/orch.key"

[profile.default]
owner = "henrik"
git_name = "Henrik"
git_email = "henrik@invalid"
`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	p, err := Resolve("orch-1")
	if err != nil {
		t.Fatalf("resolve orch-1: %v", err)
	}
	if p.OwnerID != "orch-1.sub-2" || p.GitEmail != "orch-1@invalid" || p.SSHKey != "/keys/orch.key" {
		t.Errorf("resolved = %+v", p)
	}
	if err := p.Validate(); err != nil {
		t.Errorf("validate: %v", err)
	}

	// The default entry wins over the OS identity when present.
	if p, err := Resolve(""); err != nil || p.OwnerID != "henrik" {
		t.Errorf("default resolve = %+v, %v", p, err)
	}

	// A typo in the profile name must not fall back silently.
	if _, err := Resolve("orch_1"); err == nil {
		t.Error("unknown profile name must be an error")
	}
}
