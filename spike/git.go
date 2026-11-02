// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
)

// gitResult holds the outcome of one git invocation. stdout is data, stderr
// is messages, err is non-nil when the command failed.
type gitResult struct {
	stdout string
	stderr string
	err    error
}

// git runs the git CLI with the given working directory, optional extra
// environment and arguments. Arguments are passed as a list; no shell is
// involved, matching ADR-0002 and the untrusted-input rule.
func git(dir string, env []string, args ...string) gitResult {
	full := append([]string{"-c", "core.autocrlf=false"}, args...)
	cmd := exec.Command("git", full...)
	if dir != "" {
		cmd.Dir = dir
	}
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	rerr := cmd.Run()
	return gitResult{stdout: out.String(), stderr: errb.String(), err: rerr}
}

// nonFastForward reports whether a failed push was rejected because it was
// not a fast-forward, the only retryable case in the write protocol.
func nonFastForward(r gitResult) bool {
	if r.err == nil {
		return false
	}
	s := r.stderr + r.stdout
	return strings.Contains(s, "non-fast-forward") || strings.Contains(s, "(fetch first)")
}

// gitEnv builds the identity environment for one actor, as profiles will do
// in the product (GIT_AUTHOR_* and GIT_COMMITTER_* per call).
func gitEnv(actor string) []string {
	name := "spike-" + actor
	email := name + "@dricket-spike.invalid"
	return []string{
		"GIT_AUTHOR_NAME=" + name,
		"GIT_AUTHOR_EMAIL=" + email,
		"GIT_COMMITTER_NAME=" + name,
		"GIT_COMMITTER_EMAIL=" + email,
	}
}

// lines splits command output into non-empty trimmed lines.
func lines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		l = strings.TrimSpace(strings.TrimRight(l, "\r"))
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}
