// SPDX-License-Identifier: GPL-3.0-or-later

// Package trace records what one dk call does: named steps with their
// duration and every git process started inside them. It is the instrument
// behind `dk --trace` / DK_TRACE=1 (ADR-0014). The recorder is process-wide
// and off by default; when off, every function is a cheap no-op, so the
// traced and the untraced run do exactly the same work.
//
// The report goes to stderr only. Arguments are redacted before they are
// stored: commit messages, search patterns and URL credentials never reach
// the report (no user content in logs).
package trace

import (
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// SchemaVersion is the version of the JSON report form.
const SchemaVersion = 1

// Process is one git process a step started.
type Process struct {
	Args     []string      `json:"args"`
	Duration time.Duration `json:"-"`
	Millis   float64       `json:"ms"`
	ExitCode int           `json:"exit_code"`
}

// Step is one named stretch of work. Depth is its nesting level.
type Step struct {
	Name     string        `json:"name"`
	Depth    int           `json:"depth"`
	Duration time.Duration `json:"-"`
	Millis   float64       `json:"ms"`
	Git      []Process     `json:"git"`
}

type recorder struct {
	mu       sync.Mutex
	start    time.Time
	steps    []*Step
	open     []*Step // stack of open steps; the last one owns new processes
	phaseEnd func()  // closes the running Phase
}

var (
	mu     sync.RWMutex
	global *recorder
)

// Enable switches the recorder on; start is the moment the call began.
// Calling it again resets the recording.
func Enable(start time.Time) {
	mu.Lock()
	defer mu.Unlock()
	global = &recorder{start: start}
}

// Disable switches the recorder off and drops what it holds.
func Disable() {
	mu.Lock()
	defer mu.Unlock()
	global = nil
}

// Enabled reports whether a trace is being recorded.
func Enabled() bool {
	mu.RLock()
	defer mu.RUnlock()
	return global != nil
}

// EnvEnabled reports whether DK_TRACE asks for a trace: set and neither
// empty nor 0/false/no/off.
func EnvEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("DK_TRACE"))) {
	case "", "0", "false", "no", "off":
		return false
	}
	return true
}

func current() *recorder {
	mu.RLock()
	defer mu.RUnlock()
	return global
}

// Begin opens a step and returns the function that closes it. Steps nest;
// a git process belongs to the innermost open step.
func Begin(name string) func() {
	r := current()
	if r == nil {
		return func() {}
	}
	r.mu.Lock()
	st := &Step{Name: name, Depth: len(r.open)}
	t0 := time.Now()
	r.steps = append(r.steps, st)
	r.open = append(r.open, st)
	r.mu.Unlock()
	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		st.Duration = time.Since(t0)
		st.Millis = ms(st.Duration)
		for i := len(r.open) - 1; i >= 0; i-- {
			if r.open[i] == st {
				r.open = append(r.open[:i], r.open[i+1:]...)
				break
			}
		}
	}
}

// Phase ends the previous phase and starts a new top-level step; an empty
// name only ends it. It models a linear sequence (the write protocol's
// attempts) without a close call on every early return.
func Phase(name string) {
	r := current()
	if r == nil {
		return
	}
	r.mu.Lock()
	end := r.phaseEnd
	r.phaseEnd = nil
	r.mu.Unlock()
	if end != nil {
		end()
	}
	if name == "" {
		return
	}
	end = Begin(name)
	r.mu.Lock()
	r.phaseEnd = end
	r.mu.Unlock()
}

// Run runs cmd (a git process) and records it.
func Run(cmd *exec.Cmd) error {
	t0 := time.Now()
	err := cmd.Run()
	record(cmd, time.Since(t0), err)
	return err
}

// CombinedOutput runs cmd and records it.
func CombinedOutput(cmd *exec.Cmd) ([]byte, error) {
	t0 := time.Now()
	out, err := cmd.CombinedOutput()
	record(cmd, time.Since(t0), err)
	return out, err
}

func record(cmd *exec.Cmd, d time.Duration, err error) {
	r := current()
	if r == nil {
		return
	}
	code := 0
	if err != nil {
		code = -1
		if ee, ok := err.(*exec.ExitError); ok { //nolint:errorlint // exact type is what carries the code
			code = ee.ExitCode()
		}
	}
	p := Process{Args: Redact(cmd.Args[min(1, len(cmd.Args)):]), Duration: d, Millis: ms(d), ExitCode: code}
	r.mu.Lock()
	defer r.mu.Unlock()
	if n := len(r.open); n > 0 {
		r.open[n-1].Git = append(r.open[n-1].Git, p)
		return
	}
	// A process outside any step gets its own anonymous step so the
	// process count is always complete.
	r.steps = append(r.steps, &Step{Name: "(no step)", Git: []Process{p}})
}

// Redact removes leading `-C dir` / `-c key=value` pairs (noise), hides the
// value of -m, the pattern of `grep`, and the credentials of URLs.
func Redact(args []string) []string {
	i := 0
	for i+1 < len(args) && (args[i] == "-C" || args[i] == "-c") {
		i += 2
	}
	out := make([]string, 0, len(args)-i)
	grep := false
	afterDash := false
	for j := i; j < len(args); j++ {
		a := args[j]
		switch {
		case j == i && a == "grep":
			grep = true
			out = append(out, a)
		case a == "-m" && j+1 < len(args):
			out = append(out, a, "<message>")
			j++
		case grep && a == "--" && !afterDash && j+1 < len(args):
			afterDash = true
			out = append(out, a, "<pattern>")
			j++
		default:
			out = append(out, stripUserinfo(a))
		}
	}
	return out
}

func stripUserinfo(a string) string {
	if !strings.Contains(a, "://") {
		return a
	}
	u, err := url.Parse(a)
	if err != nil || u.User == nil {
		return a
	}
	u.User = nil
	return u.String()
}

func ms(d time.Duration) float64 {
	return float64(d.Microseconds()) / 1000
}

// Summary totals a recording.
type Summary struct {
	GitProcesses int     `json:"git_processes"`
	GitMillis    float64 `json:"git_ms"`
	TotalMillis  float64 `json:"total_ms"`
}

// Report is the JSON form of a recording.
type Report struct {
	SchemaVersion int     `json:"trace_schema_version"`
	Steps         []*Step `json:"steps"`
	Summary
}

// Snapshot returns the recording so far; ok is false when tracing is off.
func Snapshot() (Report, bool) {
	r := current()
	if r == nil {
		return Report{}, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	rep := Report{SchemaVersion: SchemaVersion, Steps: append([]*Step{}, r.steps...)}
	var gitTotal time.Duration
	for _, s := range r.steps {
		if s.Git == nil {
			s.Git = []Process{}
		}
		rep.GitProcesses += len(s.Git)
		for _, p := range s.Git {
			gitTotal += p.Duration
		}
	}
	rep.GitMillis = ms(gitTotal)
	rep.TotalMillis = ms(time.Since(r.start))
	return rep, true
}

// Write renders the report: one line per step and per git process, then the
// summary line; with asJSON a single JSON object on one line. Nothing is
// written when tracing is off.
func Write(w io.Writer, asJSON bool) {
	rep, ok := Snapshot()
	if !ok {
		return
	}
	if asJSON {
		enc := json.NewEncoder(w)
		enc.SetEscapeHTML(false)
		_ = enc.Encode(rep)
		return
	}
	var b strings.Builder
	for _, s := range rep.Steps {
		pad := strings.Repeat("  ", s.Depth)
		fmt.Fprintf(&b, "trace: %s%-*s %8.1f ms\n", pad, max(1, 34-len(pad)), s.Name, s.Millis)
		for _, p := range s.Git {
			fmt.Fprintf(&b, "trace: %s  git %s  (%.1f ms%s)\n", pad, strings.Join(p.Args, " "), p.Millis, exitNote(p.ExitCode))
		}
	}
	fmt.Fprintf(&b, "trace: git: %d processes, %.0f ms of %.0f ms\n", rep.GitProcesses, rep.GitMillis, rep.TotalMillis)
	_, _ = io.WriteString(w, b.String())
}

func exitNote(code int) string {
	if code == 0 {
		return ""
	}
	return fmt.Sprintf(", exit %d", code)
}
