package main

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPrepareChainGrammar pins the `--then` grammar, which had no parse test at
// all despite being the newest and least conventional argument surface in the
// CLI.
//
// It is a GRAMMAR test rather than a behavior test because the risk here is
// divergence, not breakage: every other magus flag goes through Go's flag
// package via cmdParse, so it accepts `-f v`, `--f v`, `-f=v` and `--f=v`
// identically and rejects what it does not know. The chain verbs hand-roll their
// parsing, so each accepted form is a decision someone made once, and the only
// way it stays consistent with the rest of the CLI is if it is written down.
func TestPrepareChainGrammar(t *testing.T) {
	t.Run("verbs", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			argv []string
			verb string
			act  string
			path string
		}{
			{"value", []string{"value"}, "value", "", ""},
			{"outputs", []string{"outputs"}, "outputs", "", ""},
			{"file contents", []string{"file", "dist/a"}, "file", "", "dist/a"},
			{"file explicit contents", []string{"file", "dist/a", "contents"}, "file", "contents", "dist/a"},
			{"file history", []string{"file", "dist/a", "history"}, "file", "history", "dist/a"},
			{"file diff", []string{"file", "dist/a", "diff"}, "file", "diff", "dist/a"},
			{"file hash", []string{"file", "dist/a", "hash"}, "file", "hash", "dist/a"},
			// filepath.ToSlash is a no-op off Windows, and correctly so: a
			// backslash is a legal character in a POSIX filename, so rewriting it
			// would rename the artifact the caller asked for. Asserting
			// normalization here was a test bug, not a code bug.
			{"backslash is a literal character off Windows", []string{"file", `dist\a`}, "file", "", filepath.ToSlash(`dist\a`)},
		} {
			t.Run(tt.name, func(t *testing.T) {
				plan, proceed, err := prepareChain(tt.argv)
				require.NoError(t, err)
				assert.True(t, proceed)
				assert.Equal(t, tt.verb, plan.verb)
				assert.Equal(t, tt.act, plan.action)
				assert.Equal(t, tt.path, plan.path)
			})
		}
	})

	// Every flag elsewhere in magus accepts all four spellings, because Go's flag
	// package does. A hand-rolled reader that takes three of them is a trap: the
	// missing one fails in a way that looks like the value was wrong rather than
	// the syntax.
	t.Run("--path accepts every spelling the rest of the CLI does", func(t *testing.T) {
		for _, argv := range [][]string{
			{"outputs", "export", "--path", "out"},
			{"outputs", "export", "-path", "out"},
			{"outputs", "export", "--path=out"},
			{"outputs", "export", "-path=out"},
		} {
			t.Run(argv[2], func(t *testing.T) {
				plan, proceed, err := prepareChain(argv)
				require.NoError(t, err, "%v", argv)
				assert.True(t, proceed)
				assert.Equal(t, "export", plan.action)
				assert.Equal(t, "out", plan.dst)
			})
		}
	})

	t.Run("file export takes --path too", func(t *testing.T) {
		plan, proceed, err := prepareChain([]string{"file", "dist/a", "export", "--path", "out/a"})
		require.NoError(t, err)
		assert.True(t, proceed)
		assert.Equal(t, "export", plan.action)
		assert.Equal(t, "out/a", plan.dst)
	})

	// Rejection is the half that actually protects the caller. An argument the
	// grammar does not understand must be named, never dropped: a silently
	// ignored `-o json` is the exact failure the `--then` separator exists to
	// prevent, and it shipped that way on the export branch.
	t.Run("rejects", func(t *testing.T) {
		for name, argv := range map[string][]string{
			"no verb":                     {},
			"unknown verb":                {"bogus"},
			"value with trailing":         {"value", "extra"},
			"unknown after outputs":       {"outputs", "bogus"},
			"file without path":           {"file"},
			"unknown after file":          {"file", "dist/a", "bogus"},
			"trailing after history":      {"file", "dist/a", "history", "-o", "json"},
			"trailing after contents":     {"file", "dist/a", "contents", "-o", "json"},
			"outputs export without path": {"outputs", "export"},
			"file export without path":    {"file", "dist/a", "export"},
			"path flag with no value":     {"outputs", "export", "--path"},
			"trailing after export path":  {"outputs", "export", "--path", "out", "-o", "json"},
			"stray before export path":    {"outputs", "export", "stray", "--path", "out"},
		} {
			t.Run(name, func(t *testing.T) {
				_, proceed, err := prepareChain(argv)
				require.Error(t, err, "%v must be rejected, not accepted-and-ignored", argv)
				assert.False(t, proceed)
			})
		}
	})

	t.Run("help asks for nothing to run", func(t *testing.T) {
		for _, h := range []string{"-h", "--help", "help"} {
			_, proceed, err := prepareChain([]string{h})
			require.NoError(t, err)
			assert.False(t, proceed, "%s must not run the target", h)
		}
	})
}
