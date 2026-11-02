// SPDX-License-Identifier: GPL-3.0-or-later

package profile

import (
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

// Profile is one identity. OwnerID is the logical owner; GitName and
// GitEmail go into every commit the profile makes; SSHKey, when set, is
// used for pushes (GIT_SSH_COMMAND with IdentitiesOnly), which is the
// runner-account case of docs/CONCEPT.md, "Accounts per agent".
type Profile struct {
	Name     string
	OwnerID  string
	GitName  string
	GitEmail string
	SSHKey   string `toml:"ssh_key"`
}

// envGitConfigGlobal marks the value that isolates the profile from the
// user's global git configuration. The empty file is created by the
// store; profiles stay free of filesystem side effects.
const envGitConfigGlobal = "GIT_CONFIG_GLOBAL"

// Env returns the git identity environment for this profile, set for
// every git call as the concept prescribes. gitConfigGlobal is the path
// of an (empty) file that replaces the user's global git configuration;
// empty means no isolation and the ambient configuration applies.
func (p Profile) Env(gitConfigGlobal string) []string {
	env := []string{
		"GIT_AUTHOR_NAME=" + p.GitName,
		"GIT_AUTHOR_EMAIL=" + p.GitEmail,
		"GIT_COMMITTER_NAME=" + p.GitName,
		"GIT_COMMITTER_EMAIL=" + p.GitEmail,
	}
	if p.SSHKey != "" {
		// IdentitiesOnly keeps ssh from trying other keys first.
		env = append(env, "GIT_SSH_COMMAND=ssh -i "+p.SSHKey+" -o IdentitiesOnly=yes")
	}
	if gitConfigGlobal != "" {
		env = append(env, envGitConfigGlobal+"="+gitConfigGlobal)
	}
	return env
}

// Validate reports whether the profile is complete enough to commit.
func (p Profile) Validate() error {
	switch {
	case p.OwnerID == "":
		return fmt.Errorf("profile %q: owner id is empty", p.Name)
	case p.GitName == "":
		return fmt.Errorf("profile %q: git name is empty", p.Name)
	case p.GitEmail == "":
		return fmt.Errorf("profile %q: git email is empty", p.Name)
	}
	return nil
}

// Default derives a profile from the operating system: the owner id is
// user@host in lower case, as docs/CONCEPT.md prescribes for the default.
func Default() (Profile, error) {
	u, err := user.Current()
	if err != nil {
		return Profile{}, fmt.Errorf("current user: %w", err)
	}
	host, err := os.Hostname()
	if err != nil {
		return Profile{}, fmt.Errorf("hostname: %w", err)
	}
	name := u.Username
	owner := sanitize(strings.ToLower(name + "@" + host))
	return Profile{
		Name:     "default",
		OwnerID:  owner,
		GitName:  name,
		GitEmail: owner + "@dricket.invalid",
	}, nil
}

// userConfigPath is <user config dir>/dricket/config.toml, outside any
// repository. DK_CONFIG overrides the path wholesale (tests, setups with
// several identities on one machine).
func userConfigPath() (string, error) {
	if p := os.Getenv("DK_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("user config dir: %w", err)
	}
	return filepath.Join(dir, "dricket", "config.toml"), nil
}

// userConfig is the decoded config.toml. Explicit names are required, so
// a missing owner or identity is an error, not a silent default.
type userConfig struct {
	Profile map[string]struct {
		OwnerID  string `toml:"owner"`
		GitName  string `toml:"git_name"`
		GitEmail string `toml:"git_email"`
		SSHKey   string `toml:"ssh_key"`
	} `toml:"profile"`
}

// Resolve returns the named profile. The default profile is either the
// operating system identity (no config file) or the [profile.default]
// entry of the user config. A missing file or entry for an explicit name
// is an error: profiles are how runners get distinct credentials, so a
// typo must not fall back silently.
func Resolve(name string) (Profile, error) {
	path, err := userConfigPath()
	if err != nil {
		return Profile{}, err
	}
	data, err := os.ReadFile(path) // #nosec G304 -- path built from the user config dir
	if err != nil {
		if os.IsNotExist(err) {
			if name == "" || name == "default" {
				return Default()
			}
			return Profile{}, fmt.Errorf("profile %q: no user config at %s", name, path)
		}
		return Profile{}, err
	}
	var cfg userConfig
	if _, err := toml.Decode(string(data), &cfg); err != nil {
		return Profile{}, fmt.Errorf("%s: %w", path, err)
	}
	key := name
	if key == "" {
		key = "default"
	}
	entry, ok := cfg.Profile[key]
	if !ok {
		return Profile{}, fmt.Errorf("profile %q: no [profile.%s] section in %s", key, key, path)
	}
	p := Profile{
		Name:     key,
		OwnerID:  entry.OwnerID,
		GitName:  entry.GitName,
		GitEmail: entry.GitEmail,
		SSHKey:   entry.SSHKey,
	}
	if p.OwnerID == "" {
		d, derr := Default()
		if derr != nil {
			return Profile{}, derr
		}
		p.OwnerID = d.OwnerID
	}
	return p, p.Validate()
}

// sanitize keeps ids friendly: letters, digits, and the separators the
// concept uses in examples (dash, dot, at-sign, underscore).
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9',
			r == '-', r == '.', r == '@', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	return b.String()
}
