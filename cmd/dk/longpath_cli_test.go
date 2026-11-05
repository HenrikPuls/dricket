// SPDX-License-Identifier: GPL-3.0-or-later

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A data directory of about 150 characters pushes git's own paths (the
// private clone's .git/objects/pack/pack-<sha>.keep) past MAX_PATH on Windows.
// dk passes core.longpaths=true so that git can still write them. Beyond
// roughly 190 characters git cannot even change into the working copy; that
// limit is documented, not fixable here.
func TestCLILongDataDirOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("MAX_PATH only applies on Windows")
	}
	setupBareCLI(t)
	// Neutral git config so a user's global core.longpaths cannot hide the bug.
	noConfig := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(noConfig, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", noConfig)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")

	base := t.TempDir()
	pad := 150 - len(base) - 2
	if pad < 1 {
		t.Skipf("temp dir %q is already too long", base)
	}
	dataDir := filepath.Join(base, strings.Repeat("d", pad))
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		t.Skipf("cannot create the long data dir: %v", err)
	}
	t.Setenv("DK_DATA_DIR", dataDir)

	if code, _, errMsg := runForTest(t, "ls"); code != 0 {
		t.Fatalf("ls with a %d-character data dir = %d, %q", len(dataDir), code, errMsg)
	}
}
