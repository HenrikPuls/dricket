// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/lariscus/dricket/internal/store"
)

func TestUnknownPushJSONError(t *testing.T) {
	var out, errOut bytes.Buffer
	e := &env{stdout: &out, stderr: &errOut, json: true}
	err := wrapErr(&store.WriteError{Outcome: store.PushUnknown, Err: errors.New("reconciliation unavailable")})
	if code := e.fail(err); code != 15 {
		t.Fatalf("exit = %d, want 15", code)
	}
	var response struct {
		Error struct {
			Kind     string `json:"kind"`
			ExitCode int    `json:"exit_code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Error.Kind != "outcome-unknown" || response.Error.ExitCode != 15 {
		t.Fatalf("error = %+v", response.Error)
	}
}
