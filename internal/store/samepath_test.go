// SPDX-License-Identifier: GPL-3.0-or-later

package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSamePath(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o750); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other")
	if err := os.Mkdir(other, 0o750); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", sub, sub, true},
		{"dot segments", sub, filepath.Join(dir, ".", "other", "..", "sub"), true},
		{"different directories", sub, other, false},
		{"missing path", sub, filepath.Join(dir, "missing"), false},
	}
	for _, c := range cases {
		if got := samePath(c.a, c.b); got != c.want {
			t.Errorf("%s: samePath(%q, %q) = %v; want %v", c.name, c.a, c.b, got, c.want)
		}
	}
}
