package review

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/libs/clikit/exit"
)

// The viewer is the DEFAULT at a terminal, so this table is about FALLBACK rather than
// refusal, and that distinction is the whole point of it.
//
// Each row used to be a usage error, correctly: the flag was opt-in, so asking for the viewer
// somewhere it cannot draw was a mistake worth naming. Now nobody asked. A script running
// `magus diff -o json`, a patch file, a watch loop are ordinary invocations, and any one of
// them erroring because a default changed would be the worst kind of regression: it breaks
// callers that were never using the feature.
//
// The terminal is an ARGUMENT rather than a probe, which is what makes this testable with no pty.
func TestWantsTUIFallsBackRatherThanRefusing(t *testing.T) {
	workingTree := Source{Kind: SourceWorkingTree, Label: "the working tree"}
	stdin := Source{Kind: SourceStdin, Label: "a patch on stdin"}
	patchFile := Source{Kind: SourceFile, Path: "x.patch", Label: "the patch in x.patch"}
	revRange := Source{Kind: SourceRevRange, Base: "main", Head: "topic", Label: "the range main...topic"}
	atTerminal := TUITerm{Reads: true, Paints: true}

	tests := []struct {
		name    string
		gate    TUIGate
		src     Source
		text    bool
		term    TUITerm
		enabled bool
		noTUI   bool
		want    bool
	}{
		{
			name:    "a person at a terminal gets the viewer",
			src:     workingTree,
			text:    true,
			term:    atTerminal,
			enabled: true,
			want:    true,
		},
		{
			// The whole point of --rev. A range is a tree state magus can address, so it keeps the
			// viewer where a handed-over patch cannot; without this the branch review that --rev
			// exists for would degrade to the one-shot report it was built to replace.
			name:    "a revision range keeps the viewer",
			src:     revRange,
			text:    true,
			term:    atTerminal,
			enabled: true,
			want:    true,
		},
		{
			name:    "--no-tui is the one explicit answer",
			noTUI:   true,
			src:     workingTree,
			text:    true,
			term:    atTerminal,
			enabled: true,
		},
		{
			name: "config can turn it off for every run",
			src:  workingTree,
			text: true,
			term: atTerminal,
		},
		{
			name:    "a patch file has no working tree to coordinate a session over",
			src:     patchFile,
			text:    true,
			term:    atTerminal,
			enabled: true,
		},
		{
			name:    "stdin, same reason - this is the git-pager path",
			src:     stdin,
			text:    true,
			term:    atTerminal,
			enabled: true,
		},
		{
			name:    "--watch drives the terminal itself",
			gate:    TUIGate{Watch: true},
			src:     workingTree,
			text:    true,
			term:    atTerminal,
			enabled: true,
		},
		{
			name:    "a machine-readable format is a script, and scripts get the report",
			src:     workingTree,
			term:    atTerminal,
			enabled: true,
		},
		{
			name:    "--impact asked for a report, which the viewer has nowhere to put",
			gate:    TUIGate{Impact: true},
			src:     workingTree,
			text:    true,
			term:    atTerminal,
			enabled: true,
		},
		{
			name:    "--ack records and returns",
			gate:    TUIGate{Ack: true},
			src:     workingTree,
			text:    true,
			term:    atTerminal,
			enabled: true,
		},
		{
			name:    "no terminal at all: CI, an agent, a pipe",
			src:     workingTree,
			text:    true,
			enabled: true,
		},
		{
			// `magus diff > file`. stdin is still a keyboard, so the stdout probe is the only
			// thing that can catch this one.
			name:    "a redirected stdout is caught here, not deep inside the viewer",
			src:     workingTree,
			text:    true,
			term:    TUITerm{Reads: true},
			enabled: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gate := tc.gate
			gate.Tui, gate.Enabled, gate.Text = !tc.noTUI, tc.enabled, tc.text
			assert.Equal(t, tc.want, WantsTUI(gate, tc.src, tc.term))
		})
	}
}

// TestWantsTUIKeepsTheViewerForUnreadAndThread. Both narrow what the viewer shows rather than
// asking for an answer it has nowhere to put, so at a terminal the viewer still opens.
func TestWantsTUIKeepsTheViewerForUnreadAndThread(t *testing.T) {
	term := TUITerm{Reads: true, Paints: true}
	src := Source{Kind: SourceWorkingTree}

	// --unread and --thread are not part of the gate: neither one changes the answer.
	assert.True(t, WantsTUI(TUIGate{Tui: true, Enabled: true, Text: true}, src, term))
	assert.False(t, WantsTUI(TUIGate{Tui: true, Enabled: true}, src, term), "-o json prints the record")
	assert.False(t, WantsTUI(TUIGate{Enabled: true, Text: true}, src, term))
}

// TestDiffInputFromArgs pins the refusal that matters most: a git ref typed by someone
// arriving from `git diff <ref>` must not be swallowed into a plausible listing of their
// own uncommitted edits under exit 0.
func TestScopeFromArgs(t *testing.T) {
	t.Run("no argument narrows nothing", func(t *testing.T) {
		got, err := ScopeFromArgs(nil)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("positionals are paths, taken at face value", func(t *testing.T) {
		// Deliberately not checked against the working tree: a path that exists in the
		// changeset and not on disk is exactly what a range review is for.
		got, err := ScopeFromArgs([]string{"internal/ledger/", "types/diff.go", "gone.go"})
		require.NoError(t, err)
		assert.Equal(t, []string{"internal/ledger/", "types/diff.go", "gone.go"}, got)
	})

	t.Run("a bare dash is a source, and says where it moved", func(t *testing.T) {
		_, err := ScopeFromArgs([]string{"-"})
		require.Error(t, err)
		assert.Equal(t, exit.Usage, exit.Code(err))
		assert.Contains(t, err.Error(), "--patch -")
	})

	t.Run("something flag-shaped is a typo, not a path", func(t *testing.T) {
		_, err := ScopeFromArgs([]string{"--revv"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "--rev")
	})
}

// TestDiffSourceFromFlags pins the rule the positionals depend on: the changeset is named by a
// flag, and exactly one flag may name it.
func TestDiffSourceFromFlags(t *testing.T) {
	t.Run("nothing named reads the working tree", func(t *testing.T) {
		in, err := SourceFromFlags("", "")
		require.NoError(t, err)
		assert.Equal(t, Source{Kind: SourceWorkingTree, Label: "the working tree"}, in)
	})

	t.Run("--patch - reads stdin", func(t *testing.T) {
		in, err := SourceFromFlags("-", "")
		require.NoError(t, err)
		assert.Equal(t, Source{Kind: SourceStdin, Label: "a patch on stdin"}, in)
	})

	t.Run("--patch names a file", func(t *testing.T) {
		in, err := SourceFromFlags("change.patch", "")
		require.NoError(t, err)
		assert.Equal(t, Source{Kind: SourceFile, Path: "change.patch", Label: "the patch in change.patch"}, in)
	})

	t.Run("--rev reads a range", func(t *testing.T) {
		in, err := SourceFromFlags("", "main...topic")
		require.NoError(t, err)
		assert.Equal(t, Source{Kind: SourceRevRange, Base: "main", Head: "topic", Label: "the range main...topic"}, in)
	})

	t.Run("two sources are refused rather than ranked", func(t *testing.T) {
		_, err := SourceFromFlags("-", "main...topic")
		require.Error(t, err)
		assert.Equal(t, exit.Usage, exit.Code(err))
		assert.Contains(t, err.Error(), "only one may be given")
	})
}

func TestRevRangeFromFlag(t *testing.T) {
	t.Run("base...head is the accepted spelling", func(t *testing.T) {
		in, err := revRange("main...feat/audience")

		require.NoError(t, err)
		assert.Equal(t, Source{
			Kind:  SourceRevRange,
			Base:  "main",
			Head:  "feat/audience",
			Label: "the range main...feat/audience",
		}, in)
	})

	// The positive case above is what keeps this honest: every refusal below would also pass
	// against a parser that rejected everything, and then --rev would be unusable while green.
	t.Run("two dots are refused rather than read as three", func(t *testing.T) {
		_, err := revRange("main..feat/audience")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "three dots")
		assert.Contains(t, err.Error(), "the branch author did not write")
	})

	t.Run("a missing end is refused rather than defaulted", func(t *testing.T) {
		for _, rev := range []string{"main...", "...feat/audience"} {
			_, err := revRange(rev)

			require.Error(t, err, rev)
			assert.Contains(t, err.Error(), "needs both ends", rev)
		}
	})

	t.Run("a bare ref is refused, because it names one end of two", func(t *testing.T) {
		_, err := revRange("feat/audience")

		require.Error(t, err)
		assert.Contains(t, err.Error(), "base...head")
	})
}

func TestAddressableSeparatesTreeStatesFromHandedOverPatches(t *testing.T) {
	// The distinction --ack and the viewer both turn on: magus can re-derive and re-digest a
	// tree state, and cannot say what a patch on stdin describes.
	assert.True(t, Source{Kind: SourceWorkingTree}.Addressable())
	assert.True(t, Source{Kind: SourceRevRange}.Addressable())
	assert.False(t, Source{Kind: SourceStdin}.Addressable())
	assert.False(t, Source{Kind: SourceFile}.Addressable())
}

// Wiring magus as GIT_EXTERNAL_DIFF used to dead-end on "takes at most one patch argument, got
// 7", accurate, and useless to the person who just tried the integration. The refusal has to
// name the working setting, because reaching it means they are configuring git right now.
func TestGitExternalDiffIsRefusedWithTheSettingThatWorks(t *testing.T) {
	t.Setenv("GIT_DIFF_PATH_TOTAL", "2")
	seven := []string{"f.txt", "/tmp/blob/f.txt", "abc", "100644", "f.txt", "def", "100644"}

	_, err := ScopeFromArgs(seven)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "pager.diff")
	assert.Contains(t, err.Error(), "magus diff -")
	assert.Contains(t, err.Error(), "f.txt", "the file git was asking about")

	// Seven positionals with no git around are seven paths, which is now a legitimate scope.
	// Inventing a git explanation for them would send an ordinary caller to the wrong page.
	t.Setenv("GIT_DIFF_PATH_TOTAL", "")
	got, err := ScopeFromArgs(seven)
	require.NoError(t, err)
	assert.Equal(t, seven, got)
}
