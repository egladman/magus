package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAgentAdoptionCmdCommandsFile exercises the command end to end through the
// generated --commands binder: a file of shell commands becomes the input, and
// the whole rendered report is compared so input that arrived truncated or
// empty cannot pass.
func TestAgentAdoptionCmdCommandsFile(t *testing.T) {
	for _, tt := range []struct {
		name     string
		commands string
		want     string
	}{
		{
			name:     "commands",
			commands: "grep -rn HandleFoo internal/\ncat foo.go\nmagus query kind=spell\n",
			want: `agent adoption over 3 commands:

  graph verbs (query/refs/explain/path/graph)          1
  text searches (grep/rg/ag)                           1
  graph : search ratio                             1 : 1

  repo-wide search over source (refs/query)            1
  search over prose .md (docsection)                   0
  file reads via shell (cat/head/tail/sed)             1

top repo-wide patterns that look like symbols, with the graph query to try:
       1  HandleFoo  ->  magus refs HandleFoo
`,
		},
		{
			name:     "empty file",
			commands: "",
			want: `agent adoption over 0 commands:

  graph verbs (query/refs/explain/path/graph)          0
  text searches (grep/rg/ag)                           0
  graph : search ratio                               n/a

  repo-wide search over source (refs/query)            0
  search over prose .md (docsection)                   0
  file reads via shell (cat/head/tail/sed)             0
`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// bindDisplayFlags seeds -o from the ambient global, so a format left
			// behind by another test would decide this one's rendering.
			prev := global.output
			t.Cleanup(func() { global.output = prev })
			global.output = ""

			path := filepath.Join(t.TempDir(), "commands.txt")
			require.NoError(t, os.WriteFile(path, []byte(tt.commands), 0o644))

			var err error
			out := captureStdout(t, func() { err = agentAdoptionCommand([]string{"--commands", path}) })
			require.NoError(t, err)
			require.Equal(t, tt.want, out)
		})
	}
}

func TestAgentAdoptionCmdMissingCommandsFile(t *testing.T) {
	err := agentAdoptionCommand([]string{"--commands", filepath.Join(t.TempDir(), "absent.txt")})
	require.ErrorIs(t, err, fs.ErrNotExist)
	assert.Contains(t, err.Error(), "agent adoption: ")
}
