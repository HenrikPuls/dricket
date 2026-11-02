// SPDX-License-Identifier: GPL-3.0-or-later
package frontmatter

import (
	"errors"
	"strings"
	"testing"
)

const valid = `---
id: app-6842
title: Login error on empty password
owner:
---

## Description

Text.
`

func TestParseValid(t *testing.T) {
	d, err := Parse(valid)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, _ := d.Get("id"); got != "app-6842" {
		t.Errorf("id = %q", got)
	}
	if got, ok := d.Get("owner"); !ok || got != "" {
		t.Errorf("owner = %q, ok=%t", got, ok)
	}
	if !strings.HasPrefix(d.Body, "\n## Description") {
		t.Errorf("body = %q", d.Body)
	}
}

func TestParseErrors(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"no opening", "id: x\n---\n", "line 1"},
		{"no closing", "---\nid: x\n", "closing"},
		{"duplicate key", "---\nid: a\nid: b\n---\n", "duplicate key \"id\""},
		{"tab in key", "---\nid\tname: x\n---\n", "tabs"},
		{"no colon", "---\nid: x\ncontinuation\n---\n", "multi-line values"},
		{"empty line", "---\nid: x\n\nowner: y\n---\n", "multi-line values"},
		{"empty key", "---\n: value\n---\n", "key"},
		{"space in key", "---\nbad key: value\n---\n", "key without spaces"},
		{"control char", "---\nid: a\x00b\n---\n", "control character"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := Parse(c.in)
			if err == nil {
				t.Fatalf("expected an error")
			}
			var pe *ParseError
			if !errors.As(err, &pe) || pe.Line == 0 {
				t.Fatalf("error %v does not name a line", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not contain %q", err.Error(), c.want)
			}
		})
	}
}

func TestRenderRoundTrip(t *testing.T) {
	d, err := Parse(valid)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	d2, err := Parse(d.Render())
	if err != nil {
		t.Fatalf("re-parse: %v", err)
	}
	if len(d2.Fields) != len(d.Fields) {
		t.Fatalf("field count %d, want %d", len(d2.Fields), len(d.Fields))
	}
	for i := range d.Fields {
		if d.Fields[i].Key != d2.Fields[i].Key || d.Fields[i].Value != d2.Fields[i].Value {
			t.Errorf("field %d: %+v vs %+v", i, d.Fields[i], d2.Fields[i])
		}
	}
	if d.Body != d2.Body {
		t.Errorf("body %q vs %q", d.Body, d2.Body)
	}
}

func TestFirstBlockOnly(t *testing.T) {
	in := "---\nid: a\n---\nbody with --- \nand: more\n"
	d, err := Parse(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if _, ok := d.Get("and"); ok {
		t.Error("second block was parsed as front matter")
	}
	if !strings.Contains(d.Body, "and: more") {
		t.Errorf("body = %q", d.Body)
	}
}

func TestCRLF(t *testing.T) {
	in := "---\r\nid: app-1\r\n---\r\nbody\r\n"
	d, err := Parse(in)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if got, _ := d.Get("id"); got != "app-1" {
		t.Errorf("id = %q", got)
	}
}

func FuzzParse(f *testing.F) {
	f.Add(valid)
	f.Add("---\nid: app-1\n---\nbody\n")
	f.Add("---\n---\n")
	f.Add("")
	f.Add("---\nkey: \x00\x01\tvalue\n---\n")
	f.Add("---\nk: v\nk: v\n---\n")
	f.Fuzz(func(t *testing.T, s string) {
		d, err := Parse(s)
		if err != nil {
			var pe *ParseError
			if !errors.As(err, &pe) || pe.Line <= 0 {
				t.Fatalf("error %q does not name a line", err)
			}
			return
		}
		// Every parsed document must round-trip through Render unchanged.
		d2, err := Parse(d.Render())
		if err != nil {
			t.Fatalf("rendered document no longer parses: %v", err)
		}
		if len(d2.Fields) != len(d.Fields) || d2.Body != d.Body {
			t.Fatalf("round trip changed the document: %+v vs %+v", d, d2)
		}
		for i := range d.Fields {
			if d.Fields[i].Key != d2.Fields[i].Key || d.Fields[i].Value != d2.Fields[i].Value {
				t.Fatalf("round trip changed field %d: %+v vs %+v", i, d.Fields[i], d2.Fields[i])
			}
		}
	})
}
