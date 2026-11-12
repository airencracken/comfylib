// SPDX-License-Identifier: AGPL-3.0-or-later
package displaytime

import (
	"html/template"
	"io"
	"sync"
)

const templateLimit = 32

type binding struct {
	name  string
	value *template.Template
}

// Templates binds immutable per-timezone formatters without shared mutable
// request state. At most 32 timezone variants are retained; in-flight renders
// remain valid when an old variant is evicted.
type Templates struct {
	mu      sync.Mutex
	source  *template.Template
	bind    func(Formatter) template.FuncMap
	entries []binding
}

// NewTemplates freezes a private, unexecuted copy of the parsed templates.
// bind may be nil for the standard Funcs or may supply app-specific helpers.
func NewTemplates(source *template.Template, bind func(Formatter) template.FuncMap) (*Templates, error) {
	frozen, err := source.Clone()
	if err != nil {
		return nil, err
	}
	return &Templates{source: frozen, bind: bind}, nil
}

func (t *Templates) forZone(zone string) (*template.Template, error) {
	f := New(zone)
	name := f.Name()
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, entry := range t.entries {
		if entry.name == name {
			copy(t.entries[i:], t.entries[i+1:])
			t.entries[len(t.entries)-1] = entry
			return entry.value, nil
		}
	}
	out, err := t.source.Clone()
	if err != nil {
		return nil, err
	}
	funcs := f.Funcs()
	if t.bind != nil {
		funcs = t.bind(f)
	}
	out.Funcs(funcs)
	if len(t.entries) == templateLimit {
		t.entries = t.entries[1:]
	}
	t.entries = append(t.entries, binding{name, out})
	return out, nil
}

// ExecuteTemplate renders a named template using one reader's timezone.
func (t *Templates) ExecuteTemplate(w io.Writer, zone, name string, value any) error {
	bound, err := t.forZone(zone)
	if err != nil {
		return err
	}
	return bound.ExecuteTemplate(w, name, value)
}

// Has reports whether a named template exists in the frozen source.
func (t *Templates) Has(name string) bool { return t.source.Lookup(name) != nil }
