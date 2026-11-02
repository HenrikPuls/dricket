// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"
)

// runReport is the machine-readable result of one spike run.
type runReport struct {
	Config         config             `json:"config"`
	Ops            map[string]opStats `json:"ops"`
	Latency        map[string]pct     `json:"latency_ms"`
	Commits        int                `json:"commits"`
	CommitsPerHour float64            `json:"commits_per_hour"`
	DurationSec    float64            `json:"duration_sec"`
	Violations     []string           `json:"violations"`
	Result         string             `json:"result"`
}

func buildReport(cfg *config, m *metrics, violations []string, dur time.Duration) runReport {
	ops, lat, pushed := m.snapshot()
	r := runReport{
		Config:      *cfg,
		Ops:         ops,
		Latency:     lat,
		Commits:     pushed,
		DurationSec: dur.Seconds(),
		Violations:  violations,
		Result:      "PASS",
	}
	if dur.Seconds() > 0 {
		r.CommitsPerHour = float64(pushed) / dur.Seconds() * 3600
	}
	if len(violations) > 0 {
		r.Result = "FAIL"
	}
	return r
}

func writeReport(path string, r runReport) error {
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func printReport(r runReport) {
	fmt.Printf("result:      %s\n", r.Result)
	fmt.Printf("duration:    %s (%.0f s)\n", r.Config.Duration, r.DurationSec)
	fmt.Printf("commits:     %d (%.0f per hour)\n", r.Commits, r.CommitsPerHour)
	fmt.Println("operations:")
	names := make([]string, 0, len(r.Ops))
	for k := range r.Ops {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		s := r.Ops[k]
		l := r.Latency[k]
		fmt.Printf("  %-8s attempts %4d  ok %4d  idem %3d  rejects %4d  precond-fail %4d  final-fail %2d  p50 %6.0f ms  p95 %6.0f ms\n",
			k, s.Attempts, s.Success, s.Idempotent, s.PushReject, sumPrecond(s), s.FinalFail, l.P50, l.P95)
		if len(s.Precond) > 0 {
			var reasons []string
			for rk, rv := range s.Precond {
				reasons = append(reasons, fmt.Sprintf("%s=%d", rk, rv))
			}
			sort.Strings(reasons)
			fmt.Printf("            precond reasons: %s\n", strings.Join(reasons, ", "))
		}
	}
	if len(r.Violations) == 0 {
		fmt.Println("invariants:  duplicate-ids 0, two-owners 0, lost-updates 0, identity 0")
	} else {
		fmt.Printf("invariants:  %d VIOLATIONS\n", len(r.Violations))
		for i, v := range r.Violations {
			if i >= 50 {
				fmt.Printf("             ... and %d more\n", len(r.Violations)-50)
				break
			}
			fmt.Printf("             %s\n", v)
		}
	}
}

func sumPrecond(s opStats) int {
	n := 0
	for _, v := range s.Precond {
		n += v
	}
	return n
}
