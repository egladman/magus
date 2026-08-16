package cache

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// rewriteMain replaces the test project's source from inside a running target,
// standing in for a formatter or an autofix charm writing over its own inputs.
func rewriteMain(t *testing.T, root, body string) func(context.Context) error {
	t.Helper()
	return func(_ context.Context) error {
		writeMain(t, root, body)
		return nil
	}
}

func TestRunReportsUndeclaredSourceMutation(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main // recieve")

	_, err := c.Run(t.Context(), makeStep(root), rewriteMain(t, root, "package main // receive"))

	require.Error(t, err, "a target that rewrote its own declared source must fail")
	assert.ErrorContains(t, err, string(types.UndeclaredSourceModified))
	assert.ErrorContains(t, err, "test/pkg/main.go", "the diagnostic names the file that moved")
}

// The declaration is what separates a formatter from the footgun: ctx.modifiesExistingFiles
// folds its globs into Sources, so without Step.Updates the two are indistinguishable.
func TestRunAllowsDeclaredSourceMutation(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main // recieve")

	step := makeStep(root)
	step.Updates = []string{"test/pkg/*.go"}

	_, err := c.Run(t.Context(), step, rewriteMain(t, root, "package main // receive"))

	require.NoError(t, err, "a declared in-place edit is the API working, not a finding")
}

// A target that leaves its sources alone must stay silent, or every passing run in the
// workspace grows a diagnostic and the code stops meaning anything.
func TestRunQuietWhenSourcesUntouched(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")

	_, err := c.Run(t.Context(), makeStep(root), func(_ context.Context) error { return nil })

	require.NoError(t, err)
}

// Rewriting a source with byte-identical content is not a mutation anyone can observe
// through a cache key, and reporting it would train readers to ignore MGS4007.
func TestRunIgnoresIdenticalRewrite(t *testing.T) {
	root, _, c := newMutableCache(t)
	const body = "package main"
	writeMain(t, root, body)

	_, err := c.Run(t.Context(), makeStep(root), rewriteMain(t, root, body))

	require.NoError(t, err)
}

// A failing target has already said why it failed; MGS4007 must not bury that.
func TestRunSourceMutationDeferredToRunError(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main // recieve")

	boom := assert.AnError
	_, err := c.Run(t.Context(), makeStep(root), func(_ context.Context) error {
		writeMain(t, root, "package main // receive")
		return boom
	})

	require.ErrorIs(t, err, boom, "the target's own failure is what surfaces")
	assert.NotContains(t, err.Error(), string(types.UndeclaredSourceModified))
}

func TestMutatedSourcesDeletionCountsCreationDoesNot(t *testing.T) {
	before := sourceFingerprint{"a.go": "h1", "b.go": "h2"}
	after := sourceFingerprint{"b.go": "h2", "c.go": "h3"}

	assert.Equal(t, []string{"a.go"}, mutatedSources(before, after, nil, nil),
		"a deleted input is a write; a created one is MGS1028's question, not this one")
}

func TestMutatedSourcesHonoursUpdateGlobs(t *testing.T) {
	before := sourceFingerprint{"docs/a.md": "h1", "src/b.go": "h2"}
	after := sourceFingerprint{"docs/a.md": "changed", "src/b.go": "changed"}

	assert.Equal(t, []string{"src/b.go"},
		mutatedSources(before, after, []string{"docs/**/*.md"}, nil))
}

// ctx.needs composes targets, so a chained target's declared output is written inside
// the outer step's window. Those bytes have an owner, and blaming whichever target was
// on the outside is how `lint` got accused of generating a spell doc.
func TestMutatedSourcesIgnoresAnyTargetsDeclaredOutput(t *testing.T) {
	before := sourceFingerprint{"docs/gen-page.md": "h1", "docs/authored.md": "h2"}
	after := sourceFingerprint{"docs/gen-page.md": "regenerated", "docs/authored.md": "reformatted"}

	assert.Equal(t, []string{"docs/authored.md"},
		mutatedSources(before, after, nil, []string{"docs/gen-page.md"}))
}

// The cap keeps a formatter over a large tree from printing hundreds of paths, while
// still saying how many it withheld.
func TestJoinCappedNamesTheRemainder(t *testing.T) {
	assert.Equal(t, "a, b", joinCapped([]string{"a", "b"}, 5))
	assert.Equal(t, "a, b (and 2 more)", joinCapped([]string{"a", "b", "c", "d"}, 2))
}

// fingerprintSources must read the tree, not a memo: it runs on both sides of a target
// that executes, where a remembered pre-run hash is exactly the wrong answer.
func TestFingerprintSourcesSeesWritesBetweenCalls(t *testing.T) {
	root, _, c := newMutableCache(t)
	writeMain(t, root, "package main")
	step := makeStep(root)

	before, err := c.fingerprintSources(t.Context(), &step)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(
		filepath.Join(root, "test", "pkg", "main.go"), []byte("package main // edited"), 0o644))

	after, err := c.fingerprintSources(t.Context(), &step)
	require.NoError(t, err)

	assert.NotEqual(t, before["test/pkg/main.go"], after["test/pkg/main.go"])
}
