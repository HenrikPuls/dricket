// SPDX-License-Identifier: GPL-3.0-or-later

// Package frontmatter implements the strict front-matter parser of
// docs/CONCEPT.md: a deliberately narrow, flat YAML subset. Only the first
// --- block counts, one `key: value` per line, no nesting, no duplicate
// keys, no tabs in keys, no multi-line values, no control characters.
// Everything after the closing --- is the body. Error messages name the
// line and the expectation.
package frontmatter

import (
	"fmt"
	"strings"
)

// Field is one `key: value` line of the front matter.
type Field struct {
	Key   string
	Value string
	Line  int // 1-based line in the whole document
}

// Document is a parsed front-matter block plus the body.
type Document struct {
	Fields []Field
	Body   string
}

// ParseError names the offending line and the expectation.
type ParseError struct {
	Line int
	Msg  string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.Msg)
}

func errAt(line int, format string, a ...any) error {
	return &ParseError{Line: line, Msg: fmt.Sprintf(format, a...)}
}

// Parse reads the front matter and body of a ticket file. The header lines
// tolerate CRLF (a trailing \r is stripped per line); the body is kept
// verbatim so that Parse and Render are exactly inverse.
func Parse(s string) (Document, error) {
	lines := strings.Split(s, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return Document{}, errAt(1, "expected the document to start with '---'")
	}
	var doc Document
	closed := false
	for i := 1; i < len(lines); i++ {
		raw := strings.TrimSuffix(lines[i], "\r")
		if strings.TrimSpace(raw) == "---" {
			closed = true
			doc.Body = strings.Join(lines[i+1:], "\n")
			break
		}
		// The final element after a trailing newline is an empty string,
		// not a line of the document; it must not shadow the missing
		// closing delimiter with a misleading "empty line" error.
		if raw == "" && i == len(lines)-1 {
			continue
		}
		lineNo := i + 1
		if err := checkLine(raw, lineNo); err != nil {
			return Document{}, err
		}
		key, value, _ := strings.Cut(raw, ":")
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		for _, f := range doc.Fields {
			if f.Key == key {
				return Document{}, errAt(lineNo, "duplicate key %q (first seen on line %d)", key, f.Line)
			}
		}
		doc.Fields = append(doc.Fields, Field{Key: key, Value: value, Line: lineNo})
	}
	if !closed {
		return Document{}, errAt(len(lines), "expected a closing '---' for the front matter")
	}
	return doc, nil
}

// checkLine enforces the strict shape of one front-matter line.
func checkLine(raw string, lineNo int) error {
	if raw == "" {
		return errAt(lineNo, "expected 'key: value', found an empty line (multi-line values are not supported)")
	}
	if strings.ContainsRune(raw, '\t') {
		return errAt(lineNo, "expected 'key: value' without tabs")
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return errAt(lineNo, "expected printable characters, found a control character")
		}
	}
	key, _, ok := strings.Cut(raw, ":")
	if !ok {
		return errAt(lineNo, "expected 'key: value', found no colon (multi-line values are not supported)")
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return errAt(lineNo, "expected a key before ':'")
	}
	if strings.ContainsAny(key, " \t") {
		return errAt(lineNo, "expected a key without spaces, found %q", key)
	}
	return nil
}

// Get returns the value of a key; ok is false when the key is absent.
// Duplicates are a parse error, so at most one entry matches.
func (d Document) Get(key string) (string, bool) {
	for _, f := range d.Fields {
		if f.Key == key {
			return f.Value, true
		}
	}
	return "", false
}

// Render writes the document in its canonical form: fields in stored
// order, one `key: value` per line, body verbatim.
func (d Document) Render() string {
	var b strings.Builder
	b.WriteString("---\n")
	for _, f := range d.Fields {
		fmt.Fprintf(&b, "%s: %s\n", f.Key, f.Value)
	}
	b.WriteString("---\n")
	b.WriteString(d.Body)
	return b.String()
}
