package types

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTargetGraphProjectLabel(t *testing.T) {
	// A meaningful RelPath wins (the pre-collapsed, never-"." name).
	assert.Equal(t, "libs/api", TargetGraphProject{Path: "libs/api", RelPath: "libs/api"}.Label())

	// RelPath "." or empty falls back to the shared never-"." rule on Path.
	assert.Equal(t, "libs/api", TargetGraphProject{Path: "libs/api", RelPath: "."}.Label())
	assert.Equal(t, "api", TargetGraphProject{Path: "api"}.Label())

	// Root project with no RelPath and Path "." falls back to the shared sentinel.
	assert.Equal(t, "(workspace root)", TargetGraphProject{Path: "."}.Label())
}

func TestModuleMethodEntryToMap(t *testing.T) {
	m := ModuleMethodEntry{Name: "glob", Doc: "list files", Buzz: "fs.glob(pat)", BuzzStdlib: "glob(pat)"}
	assert.Equal(t, map[string]any{
		"name":       "glob",
		"doc":        "list files",
		"buzz":       "fs.glob(pat)",
		"buzzStdlib": "glob(pat)",
	}, m.ToMap())
}

func TestModuleFieldEntryToMap(t *testing.T) {
	f := ModuleFieldEntry{Name: "name", Type: "string", Doc: "repo name"}
	assert.Equal(t, map[string]any{
		"name": "name",
		"type": "string",
		"doc":  "repo name",
	}, f.ToMap())
}

func TestModuleEntryToMap(t *testing.T) {
	// fields/methods are always present, nested as []any of each entry's ToMap.
	e := ModuleEntry{
		Name:    "vcs",
		Doc:     "version control",
		Fields:  []ModuleFieldEntry{{Name: "name", Type: "string"}},
		Methods: []ModuleMethodEntry{{Name: "commit", Buzz: "vcs.commit()"}},
	}
	assert.Equal(t, map[string]any{
		"name": "vcs",
		"doc":  "version control",
		"fields": []any{
			map[string]any{"name": "name", "type": "string", "doc": ""},
		},
		"methods": []any{
			map[string]any{"name": "commit", "doc": "", "buzz": "vcs.commit()", "buzzStdlib": ""},
		},
	}, e.ToMap())
}

func TestModuleEntryToMapEmpty(t *testing.T) {
	// Empty (summary) view: fields/methods are present but empty, never nil.
	got := ModuleEntry{Name: "fs"}.ToMap()
	assert.Equal(t, map[string]any{
		"name":    "fs",
		"doc":     "",
		"fields":  []any{},
		"methods": []any{},
	}, got)
}
