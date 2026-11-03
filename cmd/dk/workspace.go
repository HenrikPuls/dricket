// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"context"
	"os"

	"github.com/HenrikPuls/dricket/internal/service"
	"github.com/HenrikPuls/dricket/internal/store"
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

func runCommit(e *env, args []string) (any, *cliError) {
	if len(args) != 1 {
		return nil, usageError("commit: exactly one ticket id expected")
	}
	ws, s, cerr := selectedWorkspace(e)
	if cerr != nil {
		return nil, cerr
	}
	result, err := s.Commit(context.Background(), ws, args[0])
	if err != nil {
		return nil, wrapErr(err)
	}
	if !result.Synced {
		e.msg("warning: remote commit %s succeeded, but the local checkout was not updated\n", result.Commit)
	}
	if !e.json {
		e.outf("committed %s (rev %d, commit %s)\n", result.ID, result.Rev, result.Commit)
	}
	return result, nil
}
