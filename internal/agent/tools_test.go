package agent

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func writeToolFiles(t *testing.T, root string, files []HarnessAgentFile) {
	t.Helper()
	for _, f := range files {
		path := filepath.Join(root, f.Path)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(f.Content), 0o644))
	}
}

// The directory is one file per capability, a tool name a line in sorted order, and the
// version: an empty file for a capability no row has, so "none" reads apart from "missing".
func TestHarnessToolsFilesAreOneFilePerCapability(t *testing.T) {
	tools := HarnessTools{Dir: "host/magus/tools", Rows: []HarnessTool{
		{Name: "Shell", Capability: ToolRuns},
		{Name: "Read", Capability: ToolReads},
		{Name: "Glob", Capability: ToolReads},
		{Name: "Todo", Capability: ToolInert},
	}}
	assert.Equal(t, []HarnessAgentFile{
		{Path: "host/magus/tools/runs", Content: "Shell\n"},
		{Path: "host/magus/tools/reads", Content: "Glob\nRead\n"},
		{Path: "host/magus/tools/writes", Content: ""},
		{Path: "host/magus/tools/spawns", Content: ""},
		{Path: "host/magus/tools/network", Content: ""},
		{Path: "host/magus/tools/inert", Content: "Todo\n"},
		{Path: "host/magus/tools/version", Content: "1\n"},
	}, tools.Files())
}

func TestReadToolRowsReadsBackWhatFilesWrote(t *testing.T) {
	root := t.TempDir()
	writeToolFiles(t, root, HarnessTools{Dir: "tools", Rows: []HarnessTool{
		{Name: "Shell", Capability: ToolRuns},
		{Name: "Browse", Capability: ToolNetwork},
	}}.Files())

	rows := ReadToolRows(root, "tools")
	require.NoError(t, rows.Err)
	assert.Equal(t, ToolRuns, rows.Capability("Shell"))
	assert.Equal(t, ToolNetwork, rows.Capability("Browse"))
	assert.Empty(t, rows.Capability("Stranger"))
}

// Every way to have no table names the install that writes one, and knows no tool.
func TestReadToolRowsSaysWhyThereAreNone(t *testing.T) {
	root := t.TempDir()
	stale := HarnessTools{Dir: "stale", Rows: []HarnessTool{{Name: "Shell", Capability: ToolRuns}}}.Files()
	stale[len(stale)-1].Content = "0\n"
	writeToolFiles(t, root, stale)

	for name, tc := range map[string]struct{ dir, says string }{
		"no directory named": {"", "names no --tool-rows directory"},
		"never installed":    {"missing", "no tool rows at missing"},
		"another layout":     {"stale", "are version 0 and this magus reads version 1"},
	} {
		rows := ReadToolRows(root, tc.dir)
		require.Error(t, rows.Err, name)
		assert.Contains(t, rows.Err.Error(), tc.says, name)
		assert.Contains(t, rows.Err.Error(), "magus agent harness install", name)
		assert.Empty(t, rows.Capability("Shell"), name)
	}
}

func TestValidateHarnessToolsRefusesWhatTheGuardCannotRead(t *testing.T) {
	for name, tc := range map[string]struct {
		tools HarnessTools
		err   string
	}{
		"absolute dir":       {HarnessTools{Dir: "/abs"}, "dir must be a workspace-relative path"},
		"unknown capability": {HarnessTools{Dir: "d", Rows: []HarnessTool{{Name: "X", Capability: "flies"}}}, `row 1 (X): capability "flies" is none of runs, reads, writes, spawns, network, inert`},
		"two rows":           {HarnessTools{Dir: "d", Rows: []HarnessTool{{Name: "X", Capability: ToolRuns}, {Name: "X", Capability: ToolInert}}}, "row 2: X is declared twice"},
		"a name with a line": {HarnessTools{Dir: "d", Rows: []HarnessTool{{Name: "X\nY", Capability: ToolRuns}}}, "row 1: name must be one non-empty line"},
	} {
		assert.EqualError(t, validateHarnessTools(tc.tools), tc.err, name)
	}
	assert.NoError(t, validateHarnessTools(HarnessTools{Dir: "d", Rows: []HarnessTool{{Name: "X", Capability: ToolRuns}}}))
}
