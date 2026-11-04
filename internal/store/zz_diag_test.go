package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// temporary CI diagnostics for the 8.3 short-name discovery failures
func TestZZDiagShortPath(t *testing.T) {
	t.Logf("os.TempDir() = %q", os.TempDir())
	dir := initDiscoveryStore(t, "tickets")
	t.Logf("dir          = %q", dir)
	root, err := runGitEnv(context.Background(), dir, Options{}, "rev-parse", "--show-toplevel")
	t.Logf("show-toplevel = %q err=%v", strings.TrimSpace(root), err)
	ai, aerr := os.Stat(strings.TrimSpace(root))
	bi, berr := os.Stat(dir)
	t.Logf("stat gitroot err=%v dir err=%v samefile=%v", aerr, berr, ai != nil && bi != nil && os.SameFile(ai, bi))
	ares, areserr := filepath.EvalSymlinks(strings.TrimSpace(root))
	t.Logf("EvalSymlinks(gitroot) = %q err=%v", ares, areserr)
	bres, breserr := filepath.EvalSymlinks(dir)
	t.Logf("EvalSymlinks(dir)     = %q err=%v", bres, breserr)
	d, ok, ierr := InspectRoot(context.Background(), dir)
	t.Logf("InspectRoot = %+v ok=%v err=%v", d, ok, ierr)
}
