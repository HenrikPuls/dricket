// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// gitMinMajor and gitMinMinor are the oldest git the tool relies on.
// GIT_CONFIG_GLOBAL (planned for profiles, stage v2) needs 2.32.
const (
	gitMinMajor = 2
	gitMinMinor = 32
)

// docCheck is one line of `dk doctor`.
type docCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

func runDoctor(e *env, args []string) (any, *cliError) {
	if len(args) != 0 {
		return nil, usageError("doctor: no arguments expected")
	}
	var checks []docCheck
	add := func(name string, ok bool, format string, a ...any) {
		checks = append(checks, docCheck{Name: name, OK: ok, Detail: fmt.Sprintf(format, a...)})
	}

	// git present and new enough.
	if major, minor, full, err := gitVersion(); err != nil {
		add("git", false, "not found on PATH: %v", err)
	} else if major*1000+minor < gitMinMajor*1000+gitMinMinor {
		add("git", false, "%s is too old, %d.%d or newer is required", full, gitMinMajor, gitMinMinor)
	} else {
		add("git", true, "%s (minimum %d.%d)", full, gitMinMajor, gitMinMinor)
	}

	// Identity: owner id and git author identity.
	if prof, err := e.identity(); err != nil {
		add("identity", false, "%v", err)
	} else {
		add("identity", true, "owner %s, git %s <%s>", prof.OwnerID, prof.GitName, prof.GitEmail)
	}

	// Store configuration.
	spec := e.store
	if spec == "" {
		spec = os.Getenv("DK_STORE")
	}
	if spec == "" {
		add("store", false, "no store configured (--store or DK_STORE)")
	} else {
		remote, branch, _ := strings.Cut(spec, "#")
		if branch == "" {
			branch = "tickets"
		}
		add("store", true, "%s (branch %s)", remote, branch)

		s, cerr := e.svc()
		if cerr != nil {
			add("remote", false, "%v", cerr)
		} else {
			ctx := context.Background()
			if err := s.Store.Refresh(ctx); err != nil {
				add("remote", false, "not reachable: %v", err)
			} else {
				add("remote", true, "reachable, head fetched")
				if err := s.Store.PushDryRun(ctx); err != nil {
					add("push", false, "dry-run push rejected: %v", err)
				} else {
					// A no-op push proves transport and credentials; branch
					// protection is only fully proven by a real write.
					add("push", true, "dry-run push accepted (branch protection only provable on a real write)")
				}
			}
			if dir, err := s.Store.StateDir(); err != nil {
				add("state", false, "%v", err)
			} else {
				probe := filepath.Join(dir, ".probe")
				if err := os.WriteFile(probe, []byte("dricket"), 0o600); err != nil {
					add("state", false, "data dir not writable: %v", err)
				} else {
					_ = os.Remove(probe)
					add("state", true, "writable: %s", dir)
				}
			}
		}
	}

	ok := true
	failed := 0
	for _, c := range checks {
		if !c.OK {
			ok = false
			failed++
		}
	}
	if !e.json {
		for _, c := range checks {
			status := "ok"
			if !c.OK {
				status = "FAIL"
			}
			e.outf("%-4s %-9s %s\n", status, c.Name, c.Detail)
		}
	}
	if !ok {
		return nil, &cliError{code: exitError, kind: "doctor", msg: fmt.Sprintf("%d check(s) failed", failed)}
	}
	return map[string]any{"checks": checks, "ok": true}, nil
}

// gitVersion reports the installed git version.
func gitVersion() (major, minor int, full string, err error) {
	out, err := exec.Command("git", "--version").Output() // #nosec G204 -- fixed command, no arguments
	if err != nil {
		return 0, 0, "", err
	}
	full = strings.TrimSpace(string(out))
	if _, err := fmt.Sscanf(full, "git version %d.%d", &major, &minor); err != nil {
		return 0, 0, full, fmt.Errorf("cannot parse %q", full)
	}
	return major, minor, full, nil
}
