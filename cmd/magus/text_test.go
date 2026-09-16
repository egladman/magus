package main

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTextPresenceCountsOccurrencesAndFiles is the fact refs reports beside a symbol
// miss: a name that is no symbol anywhere can still be in the tree many times, and
// saying `absent` without this states something false about the workspace.
func TestTextPresenceCountsOccurrencesAndFiles(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.WriteTree(map[string]string{
		"a.go":       "const Ref = \"NEEDLE_VALUE\"\n",
		"docs/b.md":  "export NEEDLE_VALUE=1\nand again NEEDLE_VALUE\n",
		"c/d/e.json": "{\"k\": \"nothing here\"}\n",
	})

	hits, files, searched, skipped, generated, classified, err := textPresence(context.Background(), w.Root(), "NEEDLE_VALUE", false, nil)
	require.NoError(t, err)
	assert.Equal(t, 3, hits, "two in the markdown, one in the go file")
	assert.Equal(t, 2, files)
	assert.Equal(t, 3, searched, "every file is read; the miss is a file with no match")
	assert.Zero(t, skipped)
	assert.Zero(t, generated, "no classify func: nothing can be marked generated")
	assert.False(t, classified, "no classify func: classification was never attempted")
}

// TestTextPresenceMarksGeneratedFilesByDefault pins the DEFAULT policy: a needle that
// lives only in a declared-output file must still be found, so the default includes
// generated files in the search and only marks how many of the matched files are
// generated, rather than hiding them: the one thing a raw grep cannot tell you.
func TestTextPresenceMarksGeneratedFilesByDefault(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.WriteTree(map[string]string{
		"src/a.go": "const Ref = \"NEEDLE_VALUE\"\n",
		"gen/b.go": "const Ref = \"NEEDLE_VALUE\"\n",
	})
	w.Magusfile(`import "magus";
magus\project({"outputs": ["gen/*"]});
export fun build(ctx: magus\Context, args: [str]) > void {}
`)

	// Resolved, not w.Root() verbatim: on macOS TMPDIR is under /var, a symlink to
	// /private/var, and magus.Open resolves it while the raw path would not; the
	// mismatch reads every file as escaping the workspace and classifies nothing.
	root, err := filepath.EvalSymlinks(w.Root())
	require.NoError(t, err)

	ctx := context.Background()
	m, err := magus.Open(ctx, root)
	require.NoError(t, err, "magus.Open")
	t.Cleanup(func() { _ = m.Close() })

	hits, files, searched, skipped, generated, classified, err := textPresence(ctx, root, "NEEDLE_VALUE", false, m.ClassifyFiles)
	require.NoError(t, err)
	assert.True(t, classified, "a loaded workspace must classify")
	assert.Equal(t, 2, hits)
	assert.Equal(t, 2, files, "both files matched; the generated one is marked, not hidden")
	assert.Equal(t, 3, searched, "src/a.go, gen/b.go, and the magusfile itself; the generated file was searched, not skipped")
	assert.Zero(t, skipped)
	assert.Equal(t, 1, generated, "one of the two matched files is declared output")
}

// TestTextPresenceExcludesGeneratedFilesWhenNoGenerated pins --no-generated: it removes
// declared-output files from the search entirely, and the removal is COUNTED rather
// than folded silently into skipped, which is reserved for binary/empty/oversized.
func TestTextPresenceExcludesGeneratedFilesWhenNoGenerated(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.WriteTree(map[string]string{
		"src/a.go": "const Ref = \"NEEDLE_VALUE\"\n",
		"gen/b.go": "const Ref = \"NEEDLE_VALUE\"\n",
	})
	w.Magusfile(`import "magus";
magus\project({"outputs": ["gen/*"]});
export fun build(ctx: magus\Context, args: [str]) > void {}
`)

	root, err := filepath.EvalSymlinks(w.Root())
	require.NoError(t, err)

	ctx := context.Background()
	m, err := magus.Open(ctx, root)
	require.NoError(t, err, "magus.Open")
	t.Cleanup(func() { _ = m.Close() })

	hits, files, searched, skipped, generated, classified, err := textPresence(ctx, root, "NEEDLE_VALUE", true, m.ClassifyFiles)
	require.NoError(t, err)
	assert.True(t, classified)
	assert.Equal(t, 1, hits, "only the hand-written file was searched")
	assert.Equal(t, 1, files)
	assert.Equal(t, 2, searched, "src/a.go and the magusfile; the generated file never reached the scanner")
	assert.Zero(t, skipped, "excluded-as-generated is not the same reason as skipped")
	assert.Equal(t, 1, generated, "one file was excluded because it is declared output")
}

// TestTextPresenceNeverSilentlyDropsGeneratedFilesWithoutAWorkspace is the fallback the
// task exists to guarantee: --no-generated with no workspace to classify against must
// not quietly search everything as if nothing had been asked, nor quietly search
// nothing: it must search everything and say classification could not run, so the
// caller can tell "nothing was generated" apart from "nothing could be told".
func TestTextPresenceNeverSilentlyDropsGeneratedFilesWithoutAWorkspace(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.WriteTree(map[string]string{
		"src/a.go": "const Ref = \"NEEDLE_VALUE\"\n",
		"gen/b.go": "const Ref = \"NEEDLE_VALUE\"\n",
	})

	ctx := context.Background()
	_, _, searched, _, generated, classified, err := textPresence(ctx, w.Root(), "NEEDLE_VALUE", true, nil)
	require.NoError(t, err)
	assert.False(t, classified, "no classify func: classification never ran")
	assert.Zero(t, generated, "nothing was excluded, because nothing could be classified")
	assert.Equal(t, 2, searched, "--no-generated with no classifier excludes nothing rather than guessing")
}

// TestTextPresenceFallsBackWhenClassifyFails covers a classify func that errors (a
// cancelled context, an unreadable workspace): the search must still complete rather
// than fail, and it must report classification as unavailable rather than "zero
// generated files found".
func TestTextPresenceFallsBackWhenClassifyFails(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("a.go", "const Ref = \"NEEDLE_VALUE\"\n")

	failing := func(context.Context, []string) ([]types.FileEntry, error) {
		return nil, assert.AnError
	}

	hits, _, searched, _, generated, classified, err := textPresence(context.Background(), w.Root(), "NEEDLE_VALUE", false, failing)
	require.NoError(t, err, "a failed classification must not fail the search")
	assert.Equal(t, 1, hits)
	assert.Equal(t, 1, searched)
	assert.False(t, classified)
	assert.Zero(t, generated)
}

// TestSearchableFilesSkipsMachineWrittenTrees pins the exclusions. They are named rather
// than inferred, so a wrong one here silently shrinks every answer.
func TestSearchableFilesSkipsMachineWrittenTrees(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.WriteTree(map[string]string{
		"kept.go":                   "package a",
		".git/objects/pack/x":       "binary-ish",
		".magus/outputs/y.out":      "captured log",
		"node_modules/pkg/index.js": "module.exports = {}",
	})

	paths, _, err := searchableFiles(w.Root(), nil)
	require.NoError(t, err)

	rels := make([]string, 0, len(paths))
	for _, p := range paths {
		rel, relErr := filepath.Rel(w.Root(), p)
		require.NoError(t, relErr)
		rels = append(rels, rel)
	}
	assert.Equal(t, []string{"kept.go"}, rels)
}

// TestSearchableFilesCountsWhatItDeclined is the property that keeps a short answer
// distinguishable from a small one. A count that hides its exclusions is the failure
// mode that sends a reader back to grep permanently.
func TestSearchableFilesCountsWhatItDeclined(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("kept.go", "package a")
	w.Write("empty.txt", "")
	big := make([]byte, maxSearchableFile+1)
	for i := range big {
		big[i] = 'x'
	}
	require.NoError(t, os.WriteFile(w.Path("huge.txt"), big, 0o644))

	paths, skipped, err := searchableFiles(w.Root(), nil)
	require.NoError(t, err)
	assert.Len(t, paths, 1)
	assert.Equal(t, 2, skipped, "the empty file and the oversized one are both declined, and both counted")
}

// TestSearchableFilesHonorsScopes is the path scope `refs --text` grew so a search can
// be narrowed the way grep's trailing operands narrow one. Without it the only text
// search magus offered was the whole workspace, so a reader asking about one directory
// had no magus answer at all.
func TestSearchableFilesHonorsScopes(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.WriteTree(map[string]string{
		"internal/guard/shell.go": "package guard",
		"internal/hint/hint.go":   "package hint",
		"cmd/magus/main.go":       "package main",
	})

	rels := func(paths []string) []string {
		out := make([]string, 0, len(paths))
		for _, p := range paths {
			rel, err := filepath.Rel(w.Root(), p)
			require.NoError(t, err)
			out = append(out, rel)
		}
		sort.Strings(out)
		return out
	}

	paths, _, err := searchableFiles(w.Root(), []string{w.Path("internal/guard")})
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join("internal", "guard", "shell.go")}, rels(paths),
		"a directory scope searches that directory and nothing beside it")

	paths, _, err = searchableFiles(w.Root(), []string{w.Path("cmd/magus/main.go")})
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join("cmd", "magus", "main.go")}, rels(paths),
		"a file scope is a scope of one")

	// Overlapping scopes are an ordinary way to write the argument list, and a file
	// counted twice would report its matches twice.
	paths, _, err = searchableFiles(w.Root(), []string{w.Path("internal"), w.Path("internal/guard")})
	require.NoError(t, err)
	assert.Equal(t, []string{
		filepath.Join("internal", "guard", "shell.go"),
		filepath.Join("internal", "hint", "hint.go"),
	}, rels(paths), "an overlapping scope reads each file once")
}

// TestResolveSearchScopesRefusesOutsideTheWorkspace pins the containment check. A scope
// that silently resolved to nothing would print "no matches" for a search that never
// looked where the reader asked, which is worse than the error.
func TestResolveSearchScopesRefusesOutsideTheWorkspace(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("internal/guard/shell.go", "package guard")

	scopes, err := resolveSearchScopes(w.Root(), nil)
	require.NoError(t, err)
	assert.Nil(t, scopes, "no operands means the whole workspace, not an empty search")

	scopes, err = resolveSearchScopes(w.Root(), []string{w.Path("internal/guard")})
	require.NoError(t, err)
	assert.Equal(t, []string{w.Path("internal/guard")}, scopes)

	_, err = resolveSearchScopes(w.Root(), []string{filepath.Join(w.Root(), "..")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "outside the workspace")

	_, err = resolveSearchScopes(w.Root(), []string{w.Path("nope")})
	require.Error(t, err, "a scope that does not exist is an error, not an empty search")
}

// TestTextPresenceSkipsBinary covers the NUL-byte heuristic: a match inside a binary
// yields a line nobody can read and a column nobody can act on.
func TestTextPresenceSkipsBinary(t *testing.T) {
	w := testkit.NewWorkspace(t)
	w.Write("text.txt", "NEEDLE here\n")
	require.NoError(t, os.WriteFile(w.Path("blob.bin"), []byte("NEEDLE\x00padding"), 0o644))

	hits, files, _, _, _, _, err := textPresence(context.Background(), w.Root(), "NEEDLE", false, nil)
	require.NoError(t, err)
	assert.Equal(t, 1, hits)
	assert.Equal(t, 1, files, "the binary holds the bytes but is not a searchable file")
}

// TestTextPresenceNotes pins the exact accounting clause refs prints beside a symbol
// miss, verbatim: one parenthetical extending the existing line, never a second line,
// and never silent about a filter that could not run.
func TestTextPresenceNotes(t *testing.T) {
	cases := []struct {
		name                        string
		skipped, generated, matched int
		classified, noGenerated     bool
		want                        []string
	}{
		{
			name: "nothing to report",
		},
		{
			name:    "skipped only",
			skipped: 2,
			want:    []string{"2 skipped: binary, empty, or over 1048576 bytes"},
		},
		{
			name:       "marked by default",
			classified: true,
			generated:  1,
			matched:    3,
			want:       []string{"1 of 3 generated: declared output, not hand-edited"},
		},
		{
			name:        "excluded on --no-generated",
			classified:  true,
			noGenerated: true,
			generated:   2,
			want:        []string{"2 generated file(s) excluded: declared output"},
		},
		{
			name:        "--no-generated with no workspace: says so, excludes nothing",
			noGenerated: true,
			classified:  false,
			want:        []string{"generated-file exclusion unavailable: no workspace loaded, nothing excluded"},
		},
		{
			name:        "skipped AND excluded joins as one clause",
			skipped:     3,
			classified:  true,
			noGenerated: true,
			generated:   1,
			want: []string{
				"3 skipped: binary, empty, or over 1048576 bytes",
				"1 generated file(s) excluded: declared output",
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := textPresenceNotes(tc.skipped, tc.generated, tc.matched, tc.classified, tc.noGenerated)
			assert.Equal(t, tc.want, got)
		})
	}
}
