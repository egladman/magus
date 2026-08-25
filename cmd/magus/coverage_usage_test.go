package main

import (
	"flag"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUsagePrintersNameTheirSurface pins what a reader who typed `-h` is actually
// left with. The assertion is deliberately not byte equality: prose is meant to be
// rewritten, but a usage block that stops naming a subcommand or a flag has stopped
// being usage, and that is the regression worth catching.
//
// Every printer here writes to os.Stderr, which is the convention across the package:
// help is not the command's output, so it must not land in a pipe that expects data.
func TestUsagePrintersNameTheirSurface(t *testing.T) {
	tests := []struct {
		name  string
		print func()
		want  []string
	}{
		{
			name:  "affected",
			print: affectedUsage,
			want:  []string{"Usage: magus affected", "--explain", "--impact", "--plan", "--bisect", "--base"},
		},
		{
			name:  "buzz",
			print: buzzUsage,
			want:  []string{"Usage: magus buzz", "-e <code>", "-t, -test", "--embedded", "--no-autoload", "lsp"},
		},
		{
			name:  "chain",
			print: chainUsage,
			want:  []string{"--then", "outputs export --path <dir>", "file <path> history", "file <path> diff", "value"},
		},
		{
			name:  "completion",
			print: completionUsage,
			want:  []string{"Usage: magus completion", "bash", "zsh", "fish", "powershell"},
		},
		{
			name:  "describe",
			print: describeUsage,
			want:  []string{"Usage: magus describe", "spell", "charm", "target", "graph", "project", "workspace", "module", "mcp-tool", "file", "tool"},
		},
		{
			name:  "diff",
			print: func() { diffUsage(os.Stderr) },
			want:  []string{"Usage: magus diff", "--generated", "--no-tui", "magus graph build"},
		},
		{
			name:  "graph",
			print: graphUsage,
			want:  []string{"Usage: magus graph", "build", "deps", "export", "stats", "diff"},
		},
		{
			name:  "man",
			print: manUsage,
			want:  []string{"Usage: magus man install", "--dir", "--dry-run"},
		},
		{
			name:  "memory",
			print: memoryUsage,
			want:  []string{"Usage: magus memory", "ls", "get", "put", "delete", "verify", "magus_memory"},
		},
		{
			name:  "notes",
			print: notesUsage,
			want:  []string{"Usage: magus notes", "ls", "get", "edit", "verify", "capture", "promote", "knowledge.notes.shared"},
		},
		{
			name:  "self",
			print: selfCmdUsage,
			want:  []string{"Usage: magus self", "update", "refresh", "registry", "install-shorthand", "magus init"},
		},
		{
			name:  "install-shorthand",
			print: installShorthandUsage,
			want:  []string{"Usage: magus self install-shorthand", "--dir", "--force", shorthandName},
		},
		{
			name:  "server",
			print: serverUsage,
			want:  []string{"magus server", "start", "stop", "reload", "job", "MAGUS_DAEMON_ADDRESS", daemonDefaultAddr()},
		},
		{
			name:  "server job",
			print: serverJobUsage,
			want:  []string{"magus server job <name>", "Jobs:"},
		},
		{
			name:  "vcs",
			print: func() { vcsUsage(os.Stderr) },
			want:  []string{"Usage: magus vcs", "add", "resolve", "checkpoint", "merge-driver"},
		},
		{
			name:  "vcs add",
			print: func() { vcsAddUsage(os.Stderr) },
			want:  []string{"Usage: magus vcs add", "--dry-run", "--untracked", "git add -A"},
		},
		{
			name:  "vcs resolve",
			print: func() { vcsResolveUsage(os.Stderr) },
			want:  []string{"Usage: magus vcs resolve", "--against <ref>"},
		},
		{
			name:  "vcs checkpoint",
			print: func() { vcsCheckpointUsage(os.Stderr) },
			want:  []string{"Usage: magus vcs checkpoint", "-o name", "graph diff --rev"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out := captureStderr(t, tc.print)
			require.NotEmpty(t, out)
			for _, want := range tc.want {
				assert.Contains(t, out, want)
			}
			assertPlainASCII(t, out)
		})
	}
}

// TestUsagePrintersThatReturnAnExitPath covers the two printers that also decide the
// exit status. The status is the load-bearing half: `magus run -h` asking for help is
// not a failure, and merge-driver's usage is reached by a caller that must not be told
// the driver failed.
func TestUsagePrintersThatReturnAnExitPath(t *testing.T) {
	t.Run("run target usage asks for the help exit", func(t *testing.T) {
		var err error
		out := captureStderr(t, func() { err = targetUsage() })
		assert.ErrorIs(t, err, flag.ErrHelp)
		assert.Contains(t, out, "Usage: magus run <target>")
		assert.Contains(t, out, "<spell>::<target>")
		assert.Contains(t, out, "fmt -> format")
		assertPlainASCII(t, out)
	})

	t.Run("merge driver usage succeeds", func(t *testing.T) {
		var err error
		out := captureStderr(t, func() { err = mergeDriverUsage() })
		assert.NoError(t, err)
		assert.Contains(t, out, "magus vcs merge-driver %O %A %B %L %P")
		assert.Contains(t, out, "magus init")
		assert.Contains(t, out, "magus vcs resolve")
		assertPlainASCII(t, out)
	})
}

// assertPlainASCII enforces the workspace rule that user-facing message strings carry
// no em-dashes, curly quotes, or other non-ASCII. Help text is the surface most likely
// to acquire them, and a terminal that cannot render one prints a replacement glyph.
func assertPlainASCII(t *testing.T, s string) {
	t.Helper()
	for i, r := range s {
		if r > 127 {
			t.Errorf("non-ASCII %q at byte %d in: %s", r, i, strings.SplitN(s[i:], "\n", 2)[0])
			return
		}
	}
}
