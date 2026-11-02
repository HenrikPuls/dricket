// SPDX-License-Identifier: GPL-3.0-or-later
package secrets

import (
	"strings"
	"testing"
)

func TestScanFindsKnownTokens(t *testing.T) {
	text := strings.Join([]string{
		"See the fix below",
		"use AKIAIOSFODNN7EXAMPLE for the bucket",
		"and the token ghp_abcdefghijklmnopqrstuvwxyz012345",
		"nothing to see here",
		"glpat-ABCDEFGHIJKLMNOPQRST",
	}, "\n")
	fs := Scan(text)
	if len(fs) != 3 {
		t.Fatalf("findings = %+v, want 3", fs)
	}
	if fs[0].Pattern != "aws-access-key-id" || fs[0].Line != 2 {
		t.Errorf("first = %+v", fs[0])
	}
	if fs[1].Pattern != "github-token" || fs[1].Line != 3 {
		t.Errorf("second = %+v", fs[1])
	}
	if fs[2].Pattern != "gitlab-token" || fs[2].Line != 5 {
		t.Errorf("third = %+v", fs[2])
	}
}

func TestScanPrivateKeys(t *testing.T) {
	for _, line := range []string{
		"-----BEGIN RSA PRIVATE KEY-----",
		"-----BEGIN OPENSSH PRIVATE KEY-----",
		"-----BEGIN PRIVATE KEY-----",
	} {
		fs := Scan(line)
		if len(fs) != 1 || fs[0].Pattern != "private-key-block" {
			t.Errorf("Scan(%q) = %+v", line, fs)
		}
	}
}

func TestScanIgnoresProse(t *testing.T) {
	ok := []string{
		"",
		"the api key is stored in the environment",
		"we use sk- keys at work",         // too short
		"ghp tokens start with ghp_",      // suffix too short
		"aws access keys start with AKIA", // too short
		"a password variable is not a credential",
		"xoxb friends forever",
	}
	for _, line := range ok {
		if fs := Scan(line); len(fs) != 0 {
			t.Errorf("Scan(%q) = %+v, want none", line, fs)
		}
	}
}

func TestScanReportsNoSecretContent(t *testing.T) {
	// AWS documentation example key, not a credential.
	secret := "AKIAIOSFODNN7EXAMPLE" // #nosec G101 -- docs placeholder
	out := ""
	for _, f := range Scan("leak: " + secret) {
		out += f.String()
	}
	if strings.Contains(out, secret) {
		t.Error("finding must never echo the matched content")
	}
}

func TestScanEmpty(t *testing.T) {
	if fs := Scan(""); fs != nil {
		t.Errorf("Scan(empty) = %+v, want nil", fs)
	}
}
