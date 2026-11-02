// SPDX-License-Identifier: GPL-3.0-or-later

// Package profile holds the identity a dricket process pushes as: the
// logical owner id (DK_OWNER, unique per claim-capable process) and the git
// author and committer identity. Credential sources (ssh key, token) are
// stage v2; until then the ambient git configuration provides them.
package profile

import (
	"fmt"
	"os"
	"os/user"
	"strings"
)

// Profile is one identity. OwnerID is the logical owner; GitName and
// GitEmail go into every commit the profile makes.
type Profile struct {
	Name     string
	OwnerID  string
	GitName  string
	GitEmail string
}

// Env returns the git identity environment for this profile, set for every
// git call as the concept prescribes.
func (p Profile) Env() []string {
	return []string{
		"GIT_AUTHOR_NAME=" + p.GitName,
		"GIT_AUTHOR_EMAIL=" + p.GitEmail,
		"GIT_COMMITTER_NAME=" + p.GitName,
		"GIT_COMMITTER_EMAIL=" + p.GitEmail,
	}
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
