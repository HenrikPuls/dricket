// SPDX-License-Identifier: GPL-3.0-or-later
package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func runForTest(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errb bytes.Buffer
	// run writes to os.Stdout/os.Stderr; redirect them for the test.
	oldOut, oldErr := stdout, stderr
	stdout, stderr = &out, &errb
	defer func() { stdout, stderr = oldOut, oldErr }()
	code := run(args)
	return code, out.String(), errb.String()
}

func TestVersionPlain(t *testing.T) {
	code, out, errMsg := runForTest(t, "version")
	if code != 0 {
		t.Fatalf("code = %d, stderr = %q", code, errMsg)
	}
	if !strings.Contains(out, "version: dev") {
		t.Errorf("out = %q", out)
	}
}

func TestVersionFlagJSON(t *testing.T) {
	code, out, _ := runForTest(t, "--json", "--version")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	var got struct {
		SchemaVersion int `json:"schema_version"`
		Data          struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v in %q", err, out)
	}
	if got.SchemaVersion != 1 || got.Data.Version != "dev" {
		t.Errorf("got %+v", got)
	}
}

func TestUnknownCommand(t *testing.T) {
	code, _, errMsg := runForTest(t, "frobnicate")
	if code != exitUsage {
		t.Fatalf("code = %d, want %d", code, exitUsage)
	}
	if !strings.Contains(errMsg, "unknown command") {
		t.Errorf("stderr = %q", errMsg)
	}
}

func TestUnknownCommandJSON(t *testing.T) {
	code, out, _ := runForTest(t, "--json", "frobnicate")
	if code != exitUsage {
		t.Fatalf("code = %d", code)
	}
	var got struct {
		SchemaVersion int `json:"schema_version"`
		Error         struct {
			Kind     string `json:"kind"`
			ExitCode int    `json:"exit_code"`
			Message  string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("json: %v in %q", err, out)
	}
	if got.Error.ExitCode != exitUsage || got.Error.Kind != "usage" {
		t.Errorf("got %+v", got)
	}
}

func TestNoArguments(t *testing.T) {
	code, _, errMsg := runForTest(t)
	if code != exitUsage {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(errMsg, "Usage:") {
		t.Errorf("stderr = %q", errMsg)
	}
}

func TestHelp(t *testing.T) {
	code, out, _ := runForTest(t, "--help")
	if code != 0 {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(out, "Usage:") || !strings.Contains(out, "Commands:") {
		t.Errorf("out = %q", out)
	}
}

func TestStoreFlagRequiresValue(t *testing.T) {
	code, _, errMsg := runForTest(t, "--store")
	if code != exitUsage || !strings.Contains(errMsg, "requires a value") {
		t.Fatalf("code = %d, stderr = %q", code, errMsg)
	}
}

func TestQuietStillReportsErrors(t *testing.T) {
	code, _, errMsg := runForTest(t, "-q", "frobnicate")
	if code != exitUsage {
		t.Fatalf("code = %d", code)
	}
	if !strings.Contains(errMsg, "unknown command") {
		t.Errorf("stderr = %q", errMsg)
	}
}
