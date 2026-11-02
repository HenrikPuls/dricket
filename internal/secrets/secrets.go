// SPDX-License-Identifier: GPL-3.0-or-later

// Package secrets scans untrusted ticket text for likely credentials
// before the tool pushes them (docs/CONCEPT.md, "Secrets"). The scan is
// deliberately conservative and explicitly incomplete: it prevents the
// most common accident, never replaces rotation, and its findings name
// the pattern and the line but never echo the matched content back.
package secrets

import (
	"fmt"
	"regexp"
	"strings"
)

// pattern is one named detector. The match itself is never reported.
type pattern struct {
	name string
	re   *regexp.Regexp
}

// patterns are the starting set. All anchors are chosen so that ordinary
// ticket prose does not trigger: keys must appear verbatim.
var patterns = []pattern{
	// PEM and OpenSSH private key blocks.
	{"private-key-block", regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)},
	// AWS access key ids (secret keys are too generic to detect safely).
	{"aws-access-key-id", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	// GitHub tokens (classic gh?_ and fine-grained github_pat_).
	{"github-token", regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}|github_pat_[A-Za-z0-9_]{20,}`)},
	// GitLab personal access tokens.
	{"gitlab-token", regexp.MustCompile(`glpat-[A-Za-z0-9_-]{20,}`)},
	// Slack tokens.
	{"slack-token", regexp.MustCompile(`xox[abprs]-[A-Za-z0-9-]{10,}`)},
	// OpenAI-style project keys.
	{"openai-style-key", regexp.MustCompile(`sk-[A-Za-z0-9_-]{20,}`)},
	// Stripe live keys (test keys pk_test are allowed).
	{"stripe-live-key", regexp.MustCompile(`sk_live_[A-Za-z0-9]{16,}`)},
}

// Finding is one scan hit. Line counts from 1; the matched text is
// deliberately not part of the finding.
type Finding struct {
	Pattern string
	Line    int
}

func (f Finding) String() string {
	return fmt.Sprintf("%s at line %d", f.Pattern, f.Line)
}

// Scan reports every likely secret in text, in order. Empty text has no
// findings.
func Scan(text string) []Finding {
	if text == "" {
		return nil
	}
	var out []Finding
	for i, line := range strings.Split(text, "\n") {
		for _, p := range patterns {
			if p.re.MatchString(line) {
				out = append(out, Finding{Pattern: p.name, Line: i + 1})
			}
		}
	}
	return out
}
