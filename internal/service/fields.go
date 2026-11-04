// SPDX-License-Identifier: GPL-3.0-or-later

package service

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/HenrikPuls/dricket/internal/config"
	"github.com/HenrikPuls/dricket/internal/frontmatter"
	"github.com/HenrikPuls/dricket/internal/store"
	"github.com/HenrikPuls/dricket/internal/ticket"
)

// FieldValue is one key=value pair of `dk set` or `dk add --set`.
type FieldValue struct {
	Key   string
	Value string
}

// SetResult reports the changed fields of one `set` commit. For exactly
// one pair the JSON keeps the original shape {id, key, value, rev} and adds
// fields; for several pairs only {id, rev, fields} is present.
type SetResult struct {
	ID     string
	Key    string
	Value  string
	Rev    int
	Fields map[string]string
}

// MarshalJSON keeps the single-pair shape of earlier versions.
func (r SetResult) MarshalJSON() ([]byte, error) {
	if len(r.Fields) == 1 {
		return json.Marshal(struct {
			ID     string            `json:"id"`
			Key    string            `json:"key"`
			Value  string            `json:"value"`
			Rev    int               `json:"rev"`
			Fields map[string]string `json:"fields"`
		}{r.ID, r.Key, r.Value, r.Rev, r.Fields})
	}
	return json.Marshal(struct {
		ID     string            `json:"id"`
		Rev    int               `json:"rev"`
		Fields map[string]string `json:"fields"`
	}{r.ID, r.Rev, r.Fields})
}

// Set changes one field. State and identity fields are rejected: they
// change only through commands. IfVersion guards against races (exit 12).
func (s *Service) Set(ctx context.Context, id, key, value string, ifVersion int) (SetResult, error) {
	var guard *int
	if ifVersion > 0 {
		guard = &ifVersion
	}
	return s.SetMany(ctx, id, []FieldValue{{Key: key, Value: value}}, guard)
}

// SetMany changes several free or extension fields in one commit with one
// rev bump. An extension (`x-<name>`) with an empty value is removed. All
// pairs are validated before anything is written. ifVersion nil means not
// guarded; 0 is a real version (a ticket never written after its add).
func (s *Service) SetMany(ctx context.Context, id string, pairs []FieldValue, ifVersion *int) (SetResult, error) {
	id = strings.ToLower(strings.TrimSpace(id))
	path, err := PathFor(id)
	if err != nil {
		return SetResult{}, err
	}
	if len(pairs) == 0 {
		return SetResult{}, errf(KindInvalid, "set needs at least one key=value")
	}
	cfg, cerr := s.config(ctx)
	if cerr != nil {
		return SetResult{}, cerr
	}
	clean := make([]FieldValue, 0, len(pairs))
	seen := map[string]bool{}
	for _, p := range pairs {
		v, err := cleanFieldValue(cfg, p.Key, p.Value)
		if err != nil {
			return SetResult{}, err
		}
		if seen[p.Key] {
			return SetResult{}, errf(KindInvalid, "%s given twice", p.Key)
		}
		seen[p.Key] = true
		clean = append(clean, FieldValue{Key: p.Key, Value: v})
	}
	texts := make([]string, 0, len(clean))
	keys := make([]string, 0, len(clean))
	for _, p := range clean {
		texts = append(texts, p.Value)
		keys = append(keys, p.Key)
	}
	if err := s.scanSecrets(ctx, texts...); err != nil {
		return SetResult{}, err
	}

	var out SetResult
	spec := store.WriteSpec{
		Message: "dricket: set " + strings.Join(keys, ", ") + " on " + id,
		Check: func(h store.Head) error {
			tk, _, err := loadTicket(h, path)
			if err != nil {
				return err
			}
			if ifVersion != nil && tk.Rev != *ifVersion {
				return errf(KindConflict, "ticket %s is at rev %d, expected %d", id, tk.Rev, *ifVersion)
			}
			if tk.Owner != "" && tk.Owner != s.Owner {
				return errf(KindClaimed, "ticket %s is claimed by %s", id, tk.Owner)
			}
			out = SetResult{ID: id, Rev: tk.Rev + 1}
			return nil
		},
		Mutate: func(w store.Worktree) error {
			content, err := w.ReadFile(path)
			if err != nil {
				return err
			}
			doc, err := frontmatter.Parse(content)
			if err != nil {
				return err
			}
			tk, _, err := ticket.FromDoc(doc)
			if err != nil {
				return err
			}
			out.Fields = map[string]string{}
			for _, p := range clean {
				if err := applyField(&tk, p.Key, p.Value); err != nil {
					return err
				}
				out.Fields[p.Key] = fieldValue(tk, p.Key)
				out.Key, out.Value = p.Key, out.Fields[p.Key]
			}
			tk.Rev++
			appendLog(&tk, s.Owner, "set "+strings.Join(keys, ", "), s.Now())
			if err := s.checkLint(tk, id); err != nil {
				return err
			}
			return w.WriteFile(path, tk.ToDoc().Render())
		},
	}
	if err := s.Store.Write(ctx, spec); err != nil {
		return SetResult{}, err
	}
	return out, nil
}

// cleanFieldValue validates one pair against the field classes and returns
// the normalised value. State and identity fields change only through
// commands; extension fields are checked for shape and size.
func cleanFieldValue(cfg *config.Config, key, value string) (string, error) {
	cls, known := ticket.ClassOf(key)
	if !known {
		if strings.HasPrefix(key, ticket.ExtensionPrefix) {
			return "", errf(KindInvalid, "%q is not a valid extension key (x-<name>: lower case letters, digits and dashes)", key)
		}
		return "", errf(KindInvalid, "unknown field %q", key)
	}
	switch cls {
	case ticket.Extension:
		value = strings.TrimSpace(value)
		if err := ticket.CheckExtensionValue(key, value); err != nil {
			return "", errf(KindInvalid, "%v", err)
		}
		return value, nil
	case ticket.Free:
	default:
		return "", errf(KindInvalid, "%s is not a free field; it changes only through a command", key)
	}
	value = sanitize(strings.TrimSpace(value), MaxBody)
	if key == "priority" {
		// The allowed values come from dricket.toml.
		if err := cfg.CheckPriority(strings.ToLower(value)); err != nil {
			return "", errf(KindInvalid, "%v", err)
		}
		value = strings.ToLower(value)
	}
	return value, nil
}

// applyField writes one validated pair into the ticket.
func applyField(tk *ticket.Ticket, key, value string) error {
	if cls, _ := ticket.ClassOf(key); cls == ticket.Extension {
		tk.SetExtension(key, value)
		return nil
	}
	switch key {
	case "title":
		if value == "" {
			return errf(KindInvalid, "title must not be empty")
		}
		tk.Title = value
	case "parent":
		tk.Parent = strings.ToLower(value)
	case "branch":
		tk.Branch = value
	case "priority":
		tk.Priority = value
	case "labels", "depends", "touches", "repos":
		var list []string
		for _, p := range strings.Split(value, ",") {
			if p = strings.TrimSpace(p); p != "" {
				list = append(list, p)
			}
		}
		if key == "depends" {
			for _, d := range list {
				if !ticket.ValidID(d) {
					return errf(KindInvalid, "%q is not a ticket id", d)
				}
			}
		}
		setList(tk, key, list)
	}
	return nil
}
