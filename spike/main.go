// SPDX-License-Identifier: GPL-3.0-or-later
// Command spike runs the dricket concurrency spike (R-1).
//
// It creates a local bare Git repository, starts N concurrent actor clones
// that perform random add, claim, update and release operations for a fixed
// duration using the write protocol of docs/CONCEPT.md, then replays the
// full history and checks the four invariants. Time is scaled: a lease of
// "4m" here stands in for the 4 h starting value of the concept, so lease
// expiry and takeover can be observed within minutes.
//
// Exit codes: 0 all invariants at zero, 1 invariant violation, 2 harness error.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// config holds every knob of a run. Numbers marked as starting values in
// docs/CONCEPT.md are configuration here, not constants.
type config struct {
	Clones       int           `json:"clones"`
	Duration     time.Duration `json:"duration"`
	Lease        time.Duration `json:"lease"`
	Grace        time.Duration `json:"grace"`
	CrashP       float64       `json:"crash_probability"`
	Seed         uint64        `json:"seed"`
	MaxAttempts  int           `json:"max_attempts"`
	TotalTimeout time.Duration `json:"total_timeout"`
	BackoffBase  time.Duration `json:"backoff_base"`
	BackoffCap   time.Duration `json:"backoff_cap"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "spike:", err)
		os.Exit(2)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("spike", flag.ContinueOnError)
	var (
		clones   = fs.Int("clones", 10, "number of concurrent actor clones")
		duration = fs.Duration("duration", 10*time.Minute, "run duration")
		lease    = fs.Duration("lease", 4*time.Minute, "claim lease (scaled time)")
		grace    = fs.Duration("grace", 20*time.Second, "takeover grace after lease expiry")
		crashP   = fs.Float64("crash", 0.05, "probability that a fresh claim is abandoned (dead runner)")
		seed     = fs.Uint64("seed", 1, "random seed")
		workdir  = fs.String("workdir", "", "working directory (default: fresh temp dir)")
		out      = fs.String("out", "spike-result.json", "result file (JSON)")
		keep     = fs.Bool("keep", false, "keep the working directory after the run")
	)
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg := &config{
		Clones:       *clones,
		Duration:     *duration,
		Lease:        *lease,
		Grace:        *grace,
		CrashP:       *crashP,
		Seed:         *seed,
		MaxAttempts:  8,
		TotalTimeout: 30 * time.Second,
		BackoffBase:  200 * time.Millisecond,
		BackoffCap:   5 * time.Second,
	}

	base := *workdir
	if base == "" {
		td := filepath.Join(os.TempDir(), "opencode")
		if err := os.MkdirAll(td, 0o755); err != nil {
			return err
		}
		d, err := os.MkdirTemp(td, "dricket-spike-")
		if err != nil {
			return err
		}
		base = d
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	if !*keep {
		defer os.RemoveAll(base)
	}
	fmt.Printf("spike: workdir %s\n", base)
	fmt.Printf("spike: %d clones, duration %s, lease %s, grace %s, crash %.2f, seed %d\n",
		cfg.Clones, cfg.Duration, cfg.Lease, cfg.Grace, cfg.CrashP, cfg.Seed)

	bare, err := setupBare(base, cfg.Seed)
	if err != nil {
		return err
	}

	m := newMetrics()
	acc := newAccepted()

	var wg sync.WaitGroup
	ctx, cancel := context.WithCancel(context.Background())
	timeout := time.AfterFunc(cfg.Duration, cancel)
	defer timeout.Stop()

	// Progress line every 30 seconds so long runs are observable.
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				_, _, pushed := m.snapshot()
				started := time.Since(m.started).Round(time.Second)
				fmt.Printf("spike: t=%s commits=%d\n", started, pushed)
			}
		}
	}()

	for i := 0; i < cfg.Clones; i++ {
		dir := filepath.Join(base, fmt.Sprintf("clone-%02d", i+1))
		if r := git(base, nil, "clone", "--quiet", bare, dir); r.err != nil {
			return fmt.Errorf("clone %d: %v: %s", i+1, r.err, r.stderr)
		}
		a := newActor(i, dir, cfg, m, acc, cfg.Seed)
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.run(ctx)
		}()
	}
	wg.Wait()

	runDur := time.Since(m.started)
	violations, err := verify(bare, cfg, acc)
	if err != nil {
		return err
	}
	report := buildReport(cfg, m, violations, runDur)
	if err := writeReport(*out, report); err != nil {
		return err
	}
	printReport(report)
	fmt.Printf("spike: result file %s\n", *out)
	if report.Result != "PASS" {
		os.Exit(1)
	}
	return nil
}

// setupBare creates the bare ticket repository with an empty seed commit on
// the tickets branch, then returns its path.
func setupBare(base string, seed uint64) (string, error) {
	bare := filepath.Join(base, "bare.git")
	if r := git(base, nil, "init", "--quiet", "--bare", "-b", "tickets", bare); r.err != nil {
		return "", fmt.Errorf("init bare: %v: %s", r.err, r.stderr)
	}
	seedDir := filepath.Join(base, "seed")
	if r := git(base, nil, "clone", "--quiet", bare, seedDir); r.err != nil {
		return "", fmt.Errorf("clone seed: %v: %s", r.err, r.stderr)
	}
	env := gitEnv(fmt.Sprintf("seed-%d", seed))
	if r := git(seedDir, env, "commit", "--quiet", "--allow-empty", "-m", "seed"); r.err != nil {
		return "", fmt.Errorf("seed commit: %v: %s", r.err, r.stderr)
	}
	if r := git(seedDir, nil, "push", "--quiet", "origin", "HEAD:refs/heads/tickets"); r.err != nil {
		return "", fmt.Errorf("seed push: %v: %s", r.err, r.stderr)
	}
	return bare, nil
}
