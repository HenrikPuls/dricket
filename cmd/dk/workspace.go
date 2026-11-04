// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"flag"
	"os"

	"github.com/HenrikPuls/dricket/internal/service"
	"github.com/HenrikPuls/dricket/internal/store"
	"github.com/HenrikPuls/dricket/internal/ticket"
)

func selectedWorkspace(e *env) (*store.Workspace, *service.Service, *cliError) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
	}
	ws, err := store.OpenWorkspace(context.Background(), cwd)
	if err != nil {
		return nil, nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
	}
	s, cerr := e.svc()
	if cerr != nil {
		return nil, nil, cerr
	}
	workspaceRemote, workspaceBranch, err := ws.RemoteBranch(context.Background())
	if err != nil {
		return nil, nil, &cliError{code: exitError, kind: "error", msg: err.Error()}
	}
	storeRemote, storeBranch := s.Store.RemoteBranch()
	if normalizeSpec(workspaceRemote+"#"+workspaceBranch) != normalizeSpec(storeRemote+"#"+storeBranch) {
		return nil, nil, usageError("ticket checkout does not match the selected store")
	}
	return ws, s, nil
}

func runSync(e *env, args []string) (any, *cliError) {
	if len(args) != 0 {
		return nil, usageError("sync: no positional arguments expected")
	}
	ws, _, cerr := selectedWorkspace(e)
	if cerr != nil {
		return nil, cerr
	}
	if err := ws.Sync(context.Background()); err != nil {
		return nil, wrapErr(err)
	}
	_, branch, err := ws.RemoteBranch(context.Background())
	if err != nil {
		return nil, wrapErr(err)
	}
	if !e.json {
		e.outf("synced %s\n", branch)
	}
	return map[string]any{"branch": branch}, nil
}

func runRestore(e *env, args []string) (any, *cliError) {
	if len(args) != 1 {
		return nil, usageError("restore: exactly one path expected")
	}
	ws, _, cerr := selectedWorkspace(e)
	if cerr != nil {
		return nil, cerr
	}
	if err := ws.Restore(context.Background(), args[0]); err != nil {
		return nil, wrapErr(err)
	}
	if !e.json {
		e.outf("restored %s\n", args[0])
	}
	return map[string]any{"path": args[0]}, nil
}

func newCommitFlagSet() *flag.FlagSet {
	fs := newFlagSet("commit")
	fs.Bool("release", false, "free the claim in the same commit")
	fs.String("stage", "", "release target stage (with --release; default review with the built-in stages)")
	var message string
	fs.StringVar(&message, "m", "", "result note (with --release)")
	fs.StringVar(&message, "message", "", "result note (with --release)")
	return fs
}

func runCommit(e *env, args []string) (any, *cliError) {
	fs := newCommitFlagSet()
	if cerr := parseFlags(fs, args); cerr != nil {
		return nil, cerr
	}
	if fs.NArg() != 1 {
		return nil, usageError("commit: exactly one ticket id expected")
	}
	ws, s, cerr := selectedWorkspace(e)
	if cerr != nil {
		return nil, cerr
	}
	opts := service.CommitOptions{}
	if boolFlag(fs, "release") {
		opts = service.CommitOptions{Release: true, Stage: ticket.Stage(stringFlag(fs, "stage")), Note: textFlag(fs)}
	}
	result, err := s.Commit(context.Background(), ws, fs.Arg(0), opts)
	if err != nil {
		return nil, wrapErr(err)
	}
	if !result.Synced {
		e.msg("warning: commit %s was published, but the local checkout was not updated\n", result.Commit)
	}
	if !e.json {
		if result.Stage != "" {
			e.outf("committed %s (rev %d, commit %s, released -> %s)\n", result.ID, result.Rev, result.Commit, result.Stage)
		} else {
			e.outf("committed %s (rev %d, commit %s)\n", result.ID, result.Rev, result.Commit)
		}
	}
	return result, nil
}
