// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"os"
	"path/filepath"
)

// samePath reports whether a and b name the same existing file or directory.
// Git prints canonical paths (on Windows the long form, never an 8.3 short
// name such as RUNNER~1), while callers hold whatever spelling the OS handed
// them, so a string comparison is not enough. Paths that cannot be inspected
// fall back to a cleaned string comparison.
func samePath(a, b string) bool {
	if filepath.Clean(a) == filepath.Clean(b) {
		return true
	}
	ai, aerr := os.Stat(a)
	bi, berr := os.Stat(b)
	if aerr != nil || berr != nil {
		return false
	}
	return os.SameFile(ai, bi)
}
