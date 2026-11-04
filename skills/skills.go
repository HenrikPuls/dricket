// SPDX-License-Identifier: GPL-3.0-or-later

// Package skills embeds the canonical Agent Skills shipped by dricket.
package skills

import _ "embed"

// Dricket is the canonical dricket Agent Skill installed by dk setup-agent.
//
//go:embed dricket/SKILL.md
var Dricket string
