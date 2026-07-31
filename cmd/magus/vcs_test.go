package main

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
)

// TestStatusPaths pins the porcelain parse.
//
// This exists because the bug it prevents was silent and total: DirtyFiles
// returns status LINES despite its name, every existing caller only tested the
// result for emptiness, and handing those lines to the classifier unparsed made
// every entry look like " M foo". Nothing matched a declared glob, so the first
// working version would have reported the entire workspace as one undeclared
// blob - or, with --untracked, staged it.
func TestStatusPaths(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  []string
	}{
		{"modified", []string{" M cmd/magus/agent.go"}, []string{"cmd/magus/agent.go"}},
		{"staged add", []string{"A  docs/new.md"}, []string{"docs/new.md"}},
		{"untracked", []string{"?? scratch.txt"}, []string{"scratch.txt"}},
		{"both columns", []string{"MM internal/agent/catalog.go"}, []string{"internal/agent/catalog.go"}},
		// A rename must stage the NEW name; the old one no longer exists.
		{"rename", []string{"R  old/path.go -> new/path.go"}, []string{"new/path.go"}},
		// git quotes a path containing whitespace or unusual bytes.
		{"quoted path", []string{` M "docs/a file.md"`}, []string{"docs/a file.md"}},
		{"several", []string{" M a.go", "?? b.go"}, []string{"a.go", "b.go"}},
		{"clean tree", nil, []string{}},
		{"short line ignored", []string{"x"}, []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, statusPaths(tt.lines))
		})
	}
}

// TestClassifyForStaging pins the grouping, and with it the staging doctrine:
// a generated output belongs in the SAME commit as the source that moved it, so
// both are staged; an undeclared path is the hazard `git add -A` poses, so it is
// reported instead.
func TestClassifyForStaging(t *testing.T) {
	out := []types.FileEntry{
		{Path: "cmd/magus/agent.go", Role: "source"},
		{Path: "MAGUS.md", Role: "output"},
		{Path: "docs/gen/index.html", Role: "output"},
		{Path: "scratch.txt", Role: "unclaimed"},
		{Path: "notes.md", Role: ""},
	}

	sources, outputs, undeclared := classifyForStaging(out)
	assert.Equal(t, []string{"cmd/magus/agent.go"}, sources)
	assert.Equal(t, []string{"MAGUS.md", "docs/gen/index.html"}, outputs)
	assert.Equal(t, []string{"scratch.txt", "notes.md"}, undeclared,
		"anything not declared source or output is undeclared, including an empty role")
}
