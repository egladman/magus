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

func TestModuleMethodEntryBuzzObject(t *testing.T) {
	m := ModuleMethodEntry{Name: "glob", Doc: "list files", Buzz: "fs.glob(pat)", BuzzStdlib: "glob(pat)"}
	assert.Equal(t, BuzzObject{
		"name":       "glob",
		"doc":        "list files",
		"buzz":       "fs.glob(pat)",
		"buzzStdlib": "glob(pat)",
	}, m.BuzzObject())
}

func TestModuleFieldEntryBuzzObject(t *testing.T) {
	f := ModuleFieldEntry{Name: "name", Type: "string", Doc: "repo name"}
	assert.Equal(t, BuzzObject{
		"name": "name",
		"type": "string",
		"doc":  "repo name",
	}, f.BuzzObject())
}

func TestModuleEntryBuzzObject(t *testing.T) {
	// fields/methods are always present, nested as []any of each entry's BuzzObject.
	e := ModuleEntry{
		Name:    "vcs",
		Doc:     "version control",
		Fields:  []ModuleFieldEntry{{Name: "name", Type: "string"}},
		Methods: []ModuleMethodEntry{{Name: "commit", Buzz: "vcs.commit()"}},
	}
	assert.Equal(t, BuzzObject{
		"name": "vcs",
		"doc":  "version control",
		"fields": []any{
			BuzzObject{"name": "name", "type": "string", "doc": ""},
		},
		"methods": []any{
			BuzzObject{"name": "commit", "doc": "", "buzz": "vcs.commit()", "buzzStdlib": ""},
		},
	}, e.BuzzObject())
}

func TestModuleEntryBuzzObjectEmpty(t *testing.T) {
	// Empty (summary) view: fields/methods are present but empty, never nil.
	got := ModuleEntry{Name: "fs"}.BuzzObject()
	assert.Equal(t, BuzzObject{
		"name":    "fs",
		"doc":     "",
		"fields":  []any{},
		"methods": []any{},
	}, got)
}

// TestNewFileReportOverlaps pins the set-level half of describe file: one row per
// declaration that covers SEVERAL of the classified paths, listing them. A caller
// splitting paths across concurrent authors reads this instead of intersecting the
// per-entry claims itself, so the grouping key (project, target, role, glob) has to
// be exact - two claims that differ only by target are two declarations.
func TestNewFileReportOverlaps(t *testing.T) {
	t.Parallel()
	gen := FileClaim{Project: "docs", Target: "generate", Role: "output", Glob: "docs/gen/**"}
	pub := FileClaim{Project: "docs", Target: "publish", Role: "output", Glob: "docs/gen/**"}
	src := FileClaim{Project: ".", Role: "source", Glob: "**/*.go"}
	covering := func(c FileClaim, paths ...string) FileClaim {
		c.Paths = paths
		return c
	}

	for _, tc := range []struct {
		name  string
		files []FileEntry
		want  []FileClaim
	}{
		{
			name:  "one path cannot overlap itself",
			files: []FileEntry{{Path: "docs/gen/a.html", Claims: []FileClaim{gen}}},
		},
		{
			name: "one declaration over two paths",
			files: []FileEntry{
				{Path: "docs/gen/a.html", Claims: []FileClaim{gen}},
				{Path: "docs/gen/b.html", Claims: []FileClaim{gen}},
			},
			want: []FileClaim{covering(gen, "docs/gen/a.html", "docs/gen/b.html")},
		},
		{
			name: "a declaration only one path carries is dropped",
			files: []FileEntry{
				{Path: "docs/gen/a.html", Claims: []FileClaim{gen, src}},
				{Path: "docs/gen/b.html", Claims: []FileClaim{gen}},
			},
			want: []FileClaim{covering(gen, "docs/gen/a.html", "docs/gen/b.html")},
		},
		{
			name: "same glob, different target, two declarations",
			files: []FileEntry{
				{Path: "docs/gen/a.html", Claims: []FileClaim{gen, pub}},
				{Path: "docs/gen/b.html", Claims: []FileClaim{gen, pub}},
			},
			want: []FileClaim{
				covering(gen, "docs/gen/a.html", "docs/gen/b.html"),
				covering(pub, "docs/gen/a.html", "docs/gen/b.html"),
			},
		},
		{
			// `describe file a.go a.go` classifies the path twice; one path named
			// twice is not two paths that collide.
			name: "a repeated path counts once",
			files: []FileEntry{
				{Path: "main.go", Claims: []FileClaim{src}},
				{Path: "main.go", Claims: []FileClaim{src}},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := NewFileReport(tc.files)
			assert.Equal(t, tc.want, got.Overlaps, "Overlaps")
			assert.Equal(t, len(tc.files), got.Count, "Count")
			assert.Equal(t, FileDefinition, got.Definition, "Definition")
		})
	}
}
