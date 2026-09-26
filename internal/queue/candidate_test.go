package queue

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/merge3"
	"github.com/egladman/magus/internal/queue/types"
	magustypes "github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
)

// A change stacked on one that merged as a squash deletes a line the one beneath added.
// Predicting its merge from any base older than the commit its candidate was built onto
// reads that deletion as never having happened and brings the line back; before, the
// provider's own merge did exactly that.
func TestPredictMergesTheCandidateFromTheCommitItWasBuiltOnto(t *testing.T) {
	d := newDoubles(t)
	onto, tip, cand := head("onto"), head("tip"), head("cand")
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, onto, tip).Return([]string{"docs/a.md"}, nil)
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, onto, cand).Return([]string{"lib/x.txt"}, nil)
	d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Base: onto, Ours: tip, Theirs: cand}).
		Return(magustypes.TreeMergeResult{Tree: "predicted"}, nil)
	got, err := predict(t.Context(), d.vcs, clone.Root, tip, onto, cand, change("2"))
	require.NoError(t, err)
	assert.Equal(t, "predicted", got)
}

func TestPredictOntoTheTipItWasBuiltOntoIsTheCandidatesTree(t *testing.T) {
	d := newDoubles(t)
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, head("cand")).Return("tree", nil)
	got, err := predict(t.Context(), d.vcs, clone.Root, base, base, head("cand"), change("2"))
	require.NoError(t, err)
	assert.Equal(t, "tree", got)
}

// A path the candidate and what merged since both changed is a combination nobody
// validated.
func TestPredictReportsAPathBothTheCandidateAndTheTipChanged(t *testing.T) {
	d := newDoubles(t)
	onto, tip, cand := head("onto"), head("tip"), head("cand")
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, onto, tip).Return([]string{"a", "shared"}, nil)
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, onto, cand).Return([]string{"shared", "b"}, nil)
	_, err := predict(t.Context(), d.vcs, clone.Root, tip, onto, cand, change("2"))
	conf, ok := asConflict(err)
	require.True(t, ok, "%v", err)
	assert.Equal(t, []string{"shared"}, conf.paths)
}

// A candidate for a change stacked on a squashed one merges from its stack base: the
// queue records the stack base as merged into what it builds onto, taking none of its
// content, and merges as its own identity so every job builds the same commit.
func TestBuildMergeRecordsTheStackBaseOfASquashedChangeBeneath(t *testing.T) {
	d := newDoubles(t)
	parent := change("1")
	c := stacked("2", parent)
	c.Below = ""
	onto := head("tip")
	d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, parent.Head, onto).Return(false, nil)
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, parent.Head).Return(nil)
	d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, onto).Return("tip tree", nil)
	recorded := head("recorded")
	d.vcs.EXPECT().CommitTree(mock.Anything, clone.Root, magustypes.TreeCommit{
		CommitMeta: magustypes.CommitMeta{Message: "merge queue: #2 is stacked on " + parent.Head[:12], Author: candidateIdentity, Committer: candidateIdentity, Date: when},
		Tree:       "tip tree", Parents: []string{onto, parent.Head}}).Return(recorded, nil)
	d.vcs.EXPECT().CreateCheckout(mock.Anything, clone.Root, mock.Anything, recorded).RunAndReturn(makeCheckout)
	d.vcs.EXPECT().StartMerge(mock.Anything, mock.Anything, c.Head, candidateIdentity).Return(nil)
	d.vcs.EXPECT().Conflicts(mock.Anything, mock.Anything).Return(nil, nil)
	d.vcs.EXPECT().Commit(mock.Anything, mock.Anything, magustypes.CheckoutCommit{CommitMeta: queueMeta("merge queue: candidate #2", when)}).Return(head("cand"), nil)
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, onto, head("cand")).Return([]string{"lib/x.txt"}, nil)

	b, err := buildMerge(t.Context(), d.vcs, candidateSpec{clone: clone, facts: d.facts, onto: onto, change: c, scratch: t.TempDir(), date: when})
	require.NoError(t, err)
	assert.Equal(t, head("cand"), b.Commit)
	assert.Equal(t, []string{"lib/x.txt"}, b.touched)
	box := filepath.Dir(b.Dir)
	assert.Equal(t, types.Candidate{Commit: head("cand"), Change: "2", Dir: filepath.Join(box, "checkout"), Home: filepath.Join(box, "home"), TempDir: filepath.Join(box, "tmp")}, b.Candidate)
	for _, dir := range []string{box, b.Dir, b.Home, b.TempDir} {
		info, err := os.Stat(dir)
		require.NoError(t, err)
		assert.Equal(t, os.ModeDir|0o700, info.Mode(), "%s is private to the candidate", dir)
	}
}

// A hook's tools can leave its box unremovable as it stands: Go leaves its module cache
// read-only, and a hook can take its owner's rights away from its checkout or a
// directory it cannot then list. Discarding the candidate removes all of it.
func TestDiscardRemovesABoxAHookLeftUnwritable(t *testing.T) {
	d := newDoubles(t)
	d.vcs.EXPECT().CreateCheckout(mock.Anything, clone.Root, mock.Anything, base).RunAndReturn(makeCheckout)
	d.vcs.EXPECT().RemoveCheckout(mock.Anything, clone.Root, mock.Anything).
		RunAndReturn(func(_ context.Context, _, dir string) error { return os.RemoveAll(dir) })
	cand, err := checkout(t.Context(), d.vcs, clone.Root, t.TempDir(), "candidate-1", base)
	require.NoError(t, err)
	mod := filepath.Join(cand.Home, "go", "pkg", "mod", "example.com", "m@v1.0.0")
	require.NoError(t, os.MkdirAll(mod, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mod, "m.go"), []byte("package m\n"), 0o444))
	for _, dir := range []string{mod, filepath.Dir(mod)} {
		require.NoError(t, os.Chmod(dir, 0o555))
	}
	unlistable := filepath.Join(cand.TempDir, "a", "b")
	require.NoError(t, os.MkdirAll(unlistable, 0o700))
	require.NoError(t, os.Chmod(filepath.Dir(unlistable), 0o300))
	require.NoError(t, os.Chmod(cand.Dir, 0))

	require.NoError(t, discard(t.Context(), d.vcs, clone.Root, cand))
	assert.NoDirExists(t, filepath.Dir(cand.Dir))
}

// makeWritable goes on past what it cannot read, and never changes a mode through a
// link a hook left in the box.
func TestMakeWritableSkipsWhatItCannotReadAndFollowsNoLink(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	locked := filepath.Join(dir, "a", "b")
	require.NoError(t, os.MkdirAll(locked, 0o755))
	require.NoError(t, os.Symlink(outside, filepath.Join(locked, "out")))
	require.NoError(t, os.Chmod(outside, 0o500))
	t.Cleanup(func() { _ = os.Chmod(outside, 0o700) })
	require.NoError(t, os.Chmod(locked, 0o555))
	require.NoError(t, os.Chmod(dir, 0o500))

	makeWritable(dir)
	for _, d := range []string{dir, filepath.Join(dir, "a"), locked} {
		info, err := os.Stat(d)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o700), info.Mode().Perm()&0o700, d)
	}
	info, err := os.Stat(outside)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o500), info.Mode().Perm(), "the link's target keeps its mode")
	makeWritable(filepath.Join(dir, "missing"))
}

func TestMergeInSettlesConflictsInGeneratedFilesAndRefusesTheRest(t *testing.T) {
	c := change("1")
	for name, tc := range map[string]struct {
		conflicts []magustypes.Conflict
		outputs   map[string]types.Writes
		wantErr   bool
		settle    func(d doubles)
	}{
		"a content conflict in an output takes the change's side": {
			conflicts: []magustypes.Conflict{{Path: "gen/a.go", Kind: magustypes.ConflictKindContent}},
			outputs:   map[string]types.Writes{"gen/a.go": {Output: true}},
			settle: func(d doubles) {
				d.vcs.EXPECT().KeepIncoming(mock.Anything, "/co", []string{"gen/a.go"}).Return(nil)
				d.vcs.EXPECT().MarkResolved(mock.Anything, "/co", []string{"gen/a.go"}).Return(nil)
			},
		},
		"an output one side deleted stays deleted": {
			conflicts: []magustypes.Conflict{{Path: "gen/b.go", Kind: magustypes.ConflictKindDeleted}},
			outputs:   map[string]types.Writes{"gen/b.go": {Output: true}},
			settle: func(d doubles) {
				d.vcs.EXPECT().RemoveConflicts(mock.Anything, "/co", []string{"gen/b.go"}).Return(nil)
			},
		},
		"a source conflict is the author's": {
			conflicts: []magustypes.Conflict{{Path: "a.go", Kind: magustypes.ConflictKindContent}, {Path: "gen/a.go", Kind: magustypes.ConflictKindContent}},
			outputs:   map[string]types.Writes{"gen/a.go": {Output: true}},
			wantErr:   true,
			settle: func(d doubles) {
				d.sides(base, c.Head, "a.go", "x\n", "x1\n", "x2\n")
				d.vcs.EXPECT().AbortMerge(mock.Anything, "/co").Return(nil)
				d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, c.Head, base, []string{"a.go"}).Return(nil, nil)
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			d.vcs.EXPECT().StartMerge(mock.Anything, "/co", c.Head, candidateIdentity).Return(nil)
			d.vcs.EXPECT().Conflicts(mock.Anything, "/co").Return(tc.conflicts, nil)
			d.facts.EXPECT().Classify(mock.Anything, conflictPaths(tc.conflicts)).Return(tc.outputs, nil)
			tc.settle(d)
			settled, resolved, err := mergeIn(t.Context(), d.vcs, candidateSpec{clone: clone, facts: d.facts, onto: base, change: c}, "/co", base)
			if tc.wantErr {
				conf, ok := asConflict(err)
				require.True(t, ok, "%v", err)
				assert.Equal(t, []string{"a.go"}, conf.paths)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, conflictPaths(tc.conflicts), settled)
			assert.Empty(t, resolved)
		})
	}
}

// sides answers the three versions of path auto-resolution reads: at the merge base, the
// base's side and the change's.
func (d doubles) sides(ours, theirs, path, was, oursContent, theirsContent string) {
	d.vcs.EXPECT().MergeBase(mock.Anything, mock.Anything, ours, theirs).Return(head("mb"), true, nil)
	d.vcs.EXPECT().ReadFileAt(mock.Anything, mock.Anything, head("mb"), path).Return(was, nil)
	d.vcs.EXPECT().ReadFileAt(mock.Anything, mock.Anything, ours, path).Return(oursContent, nil)
	d.vcs.EXPECT().ReadFileAt(mock.Anything, mock.Anything, theirs, path).Return(theirsContent, nil)
}

// genOutput is the build tool's answer that gen/a.go is generated and nothing else is.
var genOutput = map[string]types.Writes{"gen/a.go": {Output: true}}

// chVerdict is the build tool's line on CHANGELOG.md, and chNote how a report names its
// kind-2 merge of "a\np\nz\n" and "a\nq\nz\n".
const (
	chVerdict = `CHANGELOG.md: prose (matches "**/*.md" (built-in default))`
	chNote    = "auto-resolved " + chVerdict + "; CHANGELOG.md#(preamble): kind 2"
)

// allows answers the build tool's classification of path's merge.
func (d doubles) allows(path, was, merged, verdict string, ok bool) {
	d.facts.EXPECT().AutoResolvable(mock.Anything, path, []byte(was), []byte(merged)).Return(verdict, ok, nil)
}

// A conflicted source file merge3 settles and the build tool allows is settled in the
// checkout from its three versions, beside the generated file that takes the change's
// side, and the report names its class and each region's location and kind.
func TestMergeInAutoResolvesALowRiskSourceFile(t *testing.T) {
	c := change("1")
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "CHANGELOG.md"), []byte("<<<<<<< markers\n"), 0o640))
	d := newDoubles(t)
	conflicts := []magustypes.Conflict{{Path: "CHANGELOG.md", Kind: magustypes.ConflictKindContent}, {Path: "gen/a.go", Kind: magustypes.ConflictKindContent}}
	d.vcs.EXPECT().StartMerge(mock.Anything, dir, c.Head, candidateIdentity).Return(nil)
	d.vcs.EXPECT().Conflicts(mock.Anything, dir).Return(conflicts, nil)
	d.facts.EXPECT().Classify(mock.Anything, conflictPaths(conflicts)).Return(genOutput, nil)
	d.sides(base, c.Head, "CHANGELOG.md", "a\nz\n", "a\np\nz\n", "a\nq\nz\n")
	d.allows("CHANGELOG.md", "a\nz\n", "a\np\nq\nz\n", chVerdict, true)
	d.vcs.EXPECT().MarkResolved(mock.Anything, dir, []string{"CHANGELOG.md"}).Return(nil)
	d.vcs.EXPECT().KeepIncoming(mock.Anything, dir, []string{"gen/a.go"}).Return(nil)
	d.vcs.EXPECT().MarkResolved(mock.Anything, dir, []string{"gen/a.go"}).Return(nil)

	settled, resolved, err := mergeIn(t.Context(), d.vcs, candidateSpec{clone: clone, facts: d.facts, onto: base, change: c}, dir, base)
	require.NoError(t, err)
	assert.Equal(t, []string{"gen/a.go"}, settled)
	assert.Equal(t, []settledSource{{path: "CHANGELOG.md", verdict: chVerdict, res: merge3.Resolution{Content: []byte("a\np\nq\nz\n"),
		Regions: []merge3.Region{{Locations: []magustypes.Location{{Path: "CHANGELOG.md", Declaration: magustypes.Preamble}}, Kind: merge3.BothAdded}}}}}, resolved)
	assert.Equal(t, chNote, resolvedNote(resolved))
	written, err := os.ReadFile(filepath.Join(dir, "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Equal(t, "a\np\nq\nz\n", string(written), "ours, then theirs")
	info, err := os.Stat(filepath.Join(dir, "CHANGELOG.md"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o640), info.Mode().Perm(), "the file keeps its mode")
}

// Only what merge3 settles and the build tool allows is settled; everything else stays
// the author's, with the reason: the location merge3 could not settle, or the build
// tool's verdict.
func TestMergeInLeavesWhatAutoResolutionDoesNotSettle(t *testing.T) {
	c := change("1")
	for name, tc := range map[string]struct {
		kind     magustypes.ConflictKind
		sides    func(d doubles)
		declined []string
	}{
		"both sides edited one line": {kind: magustypes.ConflictKindContent,
			sides:    func(d doubles) { d.sides(base, c.Head, "CHANGELOG.md", "a\nx\n", "a\nx1\n", "a\nx2\n") },
			declined: []string{"CHANGELOG.md#(preamble): not settled"}},
		"the build tool declines it": {kind: magustypes.ConflictKindContent,
			sides: func(d doubles) {
				d.sides(base, c.Head, "CHANGELOG.md", "a\nz\n", "a\np\nz\n", "a\nq\nz\n")
				d.allows("CHANGELOG.md", "a\nz\n", "a\np\nq\nz\n", "CHANGELOG.md: code (why)", false)
			},
			declined: []string{"CHANGELOG.md: code (why)"}},
		"no one merge base": {kind: magustypes.ConflictKindContent,
			sides: func(d doubles) {
				d.vcs.EXPECT().MergeBase(mock.Anything, mock.Anything, base, c.Head).Return("", false, nil)
			},
			declined: []string{"CHANGELOG.md: the sides share no one merge base"}},
		"the base lacks the file": {kind: magustypes.ConflictKindContent,
			sides: func(d doubles) {
				d.vcs.EXPECT().MergeBase(mock.Anything, mock.Anything, base, c.Head).Return(head("mb"), true, nil)
				d.vcs.EXPECT().ReadFileAt(mock.Anything, mock.Anything, head("mb"), "CHANGELOG.md").Return("", errors.New("does not exist"))
			},
			declined: []string{"CHANGELOG.md: absent at the merge base"}},
		"one side deleted it": {kind: magustypes.ConflictKindDeleted, sides: func(doubles) {}},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			conflicts := []magustypes.Conflict{{Path: "CHANGELOG.md", Kind: tc.kind}}
			d.vcs.EXPECT().StartMerge(mock.Anything, "/co", c.Head, candidateIdentity).Return(nil)
			d.vcs.EXPECT().Conflicts(mock.Anything, "/co").Return(conflicts, nil)
			d.facts.EXPECT().Classify(mock.Anything, []string{"CHANGELOG.md"}).Return(genOutput, nil)
			tc.sides(d)
			d.vcs.EXPECT().AbortMerge(mock.Anything, "/co").Return(nil)
			d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, c.Head, base, []string{"CHANGELOG.md"}).Return(nil, nil)
			_, _, err := mergeIn(t.Context(), d.vcs, candidateSpec{clone: clone, facts: d.facts, onto: base, change: c}, "/co", base)
			conf, ok := asConflict(err)
			require.True(t, ok, "%v", err)
			assert.Equal(t, []string{"CHANGELOG.md"}, conf.paths)
			assert.Equal(t, tc.declined, conf.declined)
		})
	}
}

// Writing through a symlink the change controls would land wherever the link points.
func TestMergeInRefusesToAutoResolveASymlink(t *testing.T) {
	c := change("1")
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "target")
	require.NoError(t, os.WriteFile(outside, []byte("untouched\n"), 0o600))
	require.NoError(t, os.Symlink(outside, filepath.Join(dir, "CHANGELOG.md")))
	d := newDoubles(t)
	conflicts := []magustypes.Conflict{{Path: "CHANGELOG.md", Kind: magustypes.ConflictKindContent}}
	d.vcs.EXPECT().StartMerge(mock.Anything, dir, c.Head, candidateIdentity).Return(nil)
	d.vcs.EXPECT().Conflicts(mock.Anything, dir).Return(conflicts, nil)
	d.facts.EXPECT().Classify(mock.Anything, []string{"CHANGELOG.md"}).Return(genOutput, nil)
	d.sides(base, c.Head, "CHANGELOG.md", "a\n", "a\np\n", "a\nq\n")
	d.allows("CHANGELOG.md", "a\n", "a\np\nq\n", chVerdict, true)

	_, _, err := mergeIn(t.Context(), d.vcs, candidateSpec{clone: clone, facts: d.facts, onto: base, change: c}, dir, base)
	var refused *types.RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, []string{"CHANGELOG.md"}, refused.Paths)
	content, err := os.ReadFile(outside)
	require.NoError(t, err)
	assert.Equal(t, "untouched\n", string(content))
}

// Planning settles the same files from the same versions, so a change auto-resolution
// settles is admitted, and one it does not is the author's conflict. A stacked change's
// merge base is its stack base, as the merge's is.
func TestCheckMergeAdmitsWhatAutoResolutionSettles(t *testing.T) {
	c := change("1")
	tip := head("tip")
	conflicts := []magustypes.Conflict{{Path: "CHANGELOG.md", Kind: magustypes.ConflictKindContent}}
	for name, tc := range map[string]struct {
		theirs string
		want   []string
	}{
		"settled":     {theirs: "a\nq\nz\n"},
		"not settled": {theirs: "b\nz\n", want: []string{"CHANGELOG.md"}},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: tip, Theirs: c.Head}).Return(magustypes.TreeMergeResult{Tree: "t", Conflicts: conflicts}, nil)
			d.facts.EXPECT().Classify(mock.Anything, []string{"CHANGELOG.md"}).Return(genOutput, nil)
			d.sides(tip, c.Head, "CHANGELOG.md", "a\nz\n", "a\np\nz\n", tc.theirs)
			if tc.want == nil {
				d.allows("CHANGELOG.md", "a\nz\n", "a\np\nq\nz\n", chVerdict, true)
			} else {
				d.vcs.EXPECT().RangeCommits(mock.Anything, clone.Root, c.Head, tip, tc.want).Return(nil, nil)
			}
			err := checkMerge(t.Context(), d.vcs, d.facts, clone.Root, tip, c)
			if tc.want == nil {
				require.NoError(t, err)
				return
			}
			conf, ok := asConflict(err)
			require.True(t, ok, "%v", err)
			assert.Equal(t, tc.want, conf.paths)
		})
	}

	t.Run("stacked", func(t *testing.T) {
		d := newDoubles(t)
		below := change("0")
		s := stacked("2", below)
		d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, below.Head, tip).Return(false, nil)
		d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Base: below.Head, Ours: tip, Theirs: s.Head}).
			Return(magustypes.TreeMergeResult{Tree: "t", Conflicts: conflicts}, nil)
		d.facts.EXPECT().Classify(mock.Anything, []string{"CHANGELOG.md"}).Return(genOutput, nil)
		d.vcs.EXPECT().ReadFileAt(mock.Anything, clone.Root, below.Head, "CHANGELOG.md").Return("a\nz\n", nil)
		d.vcs.EXPECT().ReadFileAt(mock.Anything, clone.Root, tip, "CHANGELOG.md").Return("a\np\nz\n", nil)
		d.vcs.EXPECT().ReadFileAt(mock.Anything, clone.Root, s.Head, "CHANGELOG.md").Return("a\nq\nz\n", nil)
		d.allows("CHANGELOG.md", "a\nz\n", "a\np\nq\nz\n", chVerdict, true)
		require.NoError(t, checkMerge(t.Context(), d.vcs, d.facts, clone.Root, tip, s))
	})
}

// A candidate adds only declared writes to what was merged: a regeneration writing
// anything nothing declares is refused, as the change's own doing. The build tool's
// rewrite of a file it maintains is the base's, so the change's version is put back.
func TestRegenerateInCommitsOnlyDeclaredWrites(t *testing.T) {
	c := change("1")
	writes := map[string]types.Writes{"gen/a.go": {Output: true}, "docs/a.md": {Updated: true}, ".gitattributes": {Maintained: true}}
	for name, tc := range map[string]struct {
		written   []string
		committed []string
		want      string
		wantErr   string
	}{
		"nothing rewritten":                 {want: head("cand")},
		"an output rewritten":               {written: []string{"gen/a.go"}, committed: []string{"gen/a.go"}, want: head("regenerated")},
		"a file a target updates rewritten": {written: []string{"docs/a.md", "gen/a.go"}, committed: []string{"docs/a.md", "gen/a.go"}, want: head("regenerated")},
		"a maintained file rewritten":       {written: []string{".gitattributes", "gen/a.go"}, committed: []string{"gen/a.go"}, want: head("regenerated")},
		"only a maintained file rewritten":  {written: []string{".gitattributes"}, want: head("cand")},
		"a source rewritten":                {written: []string{"gen/a.go", "a.go"}, wantErr: "regeneration wrote files nothing declares it writes: a.go"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("the base's rewrite\n"), 0o600))
			b := built{Candidate: types.Candidate{Commit: head("cand"), Dir: dir, Home: "/box/home", TempDir: "/box/tmp"}, touched: []string{"a.go", "gen/a.go"}, changed: []string{"a.go", "gen/a.go"}, date: when}
			regenerate := func(_ context.Context, r types.Regeneration) error {
				assert.Equal(t, types.Regeneration{Dir: dir, Home: "/box/home", TempDir: "/box/tmp", Change: c, Paths: []string{"gen/a.go"}, Units: []string{"gen"}}, r)
				return nil
			}
			d := newDoubles(t)
			s := candidateSpec{clone: clone, facts: d.facts, onto: base, change: c}
			d.facts.EXPECT().Classify(mock.Anything, b.touched).Return(writes, nil)
			d.vcs.EXPECT().DirtyFiles(mock.Anything, dir, []string(nil)).Return(tc.written, nil)
			if len(tc.written) > 0 {
				d.facts.EXPECT().Classify(mock.Anything, tc.written).Return(writes, nil)
			}
			restored := slices.Contains(tc.written, ".gitattributes")
			if restored {
				d.vcs.EXPECT().ReadFileAt(mock.Anything, dir, head("cand"), ".gitattributes").Return("the change's\n", nil)
			}
			if tc.committed != nil {
				d.vcs.EXPECT().Commit(mock.Anything, dir, magustypes.CheckoutCommit{CommitMeta: queueMeta("regenerate generated files", when), Paths: tc.committed}).Return(tc.want, nil)
			}
			got, err := regenerateIn(t.Context(), d.vcs, s, b, regenerate, []string{"gen"})
			if restored {
				content, readErr := os.ReadFile(filepath.Join(dir, ".gitattributes"))
				require.NoError(t, readErr)
				assert.Equal(t, "the change's\n", string(content))
			}
			if tc.wantErr != "" {
				var refused *types.RefusedError
				require.ErrorAs(t, err, &refused)
				assert.Equal(t, tc.wantErr, refused.Reason)
				assert.Equal(t, []string{"a.go"}, refused.Paths)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// A symbolic link the change holds would carry the generator's writes wherever it
// points, so the base's regeneration never runs in a checkout where one sits at, or
// above, a path the change holds or the regeneration writes.
func TestRegenerateInRefusesACheckoutWhereTheChangeHoldsALink(t *testing.T) {
	c := change("1")
	for name, tc := range map[string]struct {
		link, to string
		want     []string
	}{
		"an output":              {link: "gen/a.go", to: "a.go", want: []string{"gen/a.go"}},
		"a directory above one":  {link: "gen", to: "elsewhere", want: []string{"gen/a.go"}},
		"another path it holds":  {link: "tools", to: "elsewhere", want: []string{"tools/x.sh"}},
		"a link pointing inside": {link: "gen/a.go", to: "../a.go", want: []string{"gen/a.go"}},
	} {
		t.Run(name, func(t *testing.T) {
			dir, outside := t.TempDir(), t.TempDir()
			require.NoError(t, os.MkdirAll(filepath.Join(dir, "gen"), 0o755))
			require.NoError(t, os.MkdirAll(filepath.Join(outside, "elsewhere"), 0o755))
			target := tc.to
			if !strings.HasPrefix(target, "..") {
				target = filepath.Join(outside, tc.to)
			}
			require.NoError(t, os.RemoveAll(filepath.Join(dir, tc.link)))
			require.NoError(t, os.Symlink(target, filepath.Join(dir, tc.link)))
			b := built{Candidate: types.Candidate{Commit: head("cand"), Dir: dir}, touched: []string{"gen/a.go"}, changed: []string{"a.go", "gen/a.go", "tools/x.sh"}}
			d := newDoubles(t)
			d.facts.EXPECT().Classify(mock.Anything, b.touched).Return(map[string]types.Writes{"gen/a.go": {Output: true}}, nil)
			regenerate := func(context.Context, types.Regeneration) error {
				t.Error("regenerated through a link")
				return nil
			}
			_, err := regenerateIn(t.Context(), d.vcs, candidateSpec{clone: clone, facts: d.facts, onto: base, change: c}, b, regenerate, []string{"gen"})
			var refused *types.RefusedError
			require.ErrorAs(t, err, &refused)
			assert.Equal(t, tc.want, refused.Paths)
			assert.Contains(t, refused.Reason, "as a symbolic link")
		})
	}
}

// A maintained file is put back through the checkout's root: a link the regeneration
// left there is refused, and what it points at is never written.
func TestRestoreNeverWritesThroughALink(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	victim := filepath.Join(outside, "config")
	require.NoError(t, os.WriteFile(victim, []byte("untouched\n"), 0o600))
	require.NoError(t, os.Symlink(victim, filepath.Join(dir, ".gitattributes")))
	d := newDoubles(t)
	cand := types.Candidate{Commit: head("cand"), Dir: dir}
	d.vcs.EXPECT().ReadFileAt(mock.Anything, dir, head("cand"), ".gitattributes").Return("the change's\n", nil)
	require.ErrorContains(t, restore(t.Context(), d.vcs, cand, ".gitattributes"), ".gitattributes is a symbolic link")
	content, err := os.ReadFile(victim)
	require.NoError(t, err)
	assert.Equal(t, "untouched\n", string(content))

	// A regular file is replaced, keeping its mode.
	require.NoError(t, os.Remove(filepath.Join(dir, ".gitattributes")))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".gitattributes"), []byte("the base's rewrite\n"), 0o600))
	require.NoError(t, restore(t.Context(), d.vcs, cand, ".gitattributes"))
	info, err := os.Lstat(filepath.Join(dir, ".gitattributes"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode())
	content, err = os.ReadFile(filepath.Join(dir, ".gitattributes"))
	require.NoError(t, err)
	assert.Equal(t, "the change's\n", string(content))
}

// A merge of the base into a change is covered by the review beneath it when it adds
// nothing; one differing from the plain merge only in declared outputs is covered once
// the base's regeneration reproduces them, which reviewTarget returns as owed.
func TestReviewTargetLooksThroughMergesOfTheBaseThatAddNothingUnreviewed(t *testing.T) {
	first, second := head("first"), head("on base")
	merge := head("merge")
	for name, tc := range map[string]struct {
		plain    magustypes.TreeMergeResult
		diff     []string
		outputs  map[string]types.Writes
		want     string
		wantOwed []regenerationProof
	}{
		"the plain merge":            {plain: magustypes.TreeMergeResult{Tree: "t"}, want: first},
		"a merge that edited source": {plain: magustypes.TreeMergeResult{Tree: "other"}, diff: []string{"a.go"}, outputs: map[string]types.Writes{}, want: merge},
		"a merge that edited a file a target updates in place": {plain: magustypes.TreeMergeResult{Tree: "other"}, diff: []string{"docs/a.md"},
			outputs: map[string]types.Writes{"docs/a.md": {Updated: true}}, want: merge},
		"a merge that regenerated": {plain: magustypes.TreeMergeResult{Tree: "other"}, diff: []string{"gen/a.go"}, outputs: map[string]types.Writes{"gen/a.go": {Output: true}},
			want: first, wantOwed: []regenerationProof{{Commit: merge, First: first, Onto: second, Paths: []string{"gen/a.go"}}}},
	} {
		t.Run(name, func(t *testing.T) {
			d := newDoubles(t)
			d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, merge).Return(magustypes.Commit{ID: merge, Parents: []string{first, second}}, nil)
			d.vcs.EXPECT().IsAncestor(mock.Anything, clone.Root, second, base).Return(true, nil)
			d.vcs.EXPECT().TreeID(mock.Anything, clone.Root, merge).Return("t", nil)
			d.vcs.EXPECT().MergeTrees(mock.Anything, clone.Root, magustypes.TreeMerge{Ours: second, Theirs: first}).Return(tc.plain, nil)
			if tc.diff != nil {
				d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, "other", "t").Return(tc.diff, nil)
				d.facts.EXPECT().Classify(mock.Anything, tc.diff).Return(tc.outputs, nil)
			}
			if tc.want == first {
				d.vcs.EXPECT().FindCommit(mock.Anything, clone.Root, first).Return(magustypes.Commit{ID: first, Parents: []string{base}}, nil)
			}
			got, owed, err := reviewTarget(t.Context(), d.vcs, d.facts, clone.Root, base, merge, "")
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.wantOwed, owed)
		})
	}
}

func TestForkOf(t *testing.T) {
	commit := func(id string, parents ...string) magustypes.Commit {
		return magustypes.Commit{ID: id, Parents: parents}
	}
	assert.Equal(t, "x", forkOf("x", nil), "a commit the base carries forked from itself")
	assert.Equal(t, "B", forkOf("2", []magustypes.Commit{commit("2", "1"), commit("1", "B")}))
	assert.Equal(t, "", forkOf("m", []magustypes.Commit{commit("m", "1", "B2"), commit("1", "B")}), "merged the base's history in twice")
}

func TestFetchHeadFallsBackToTheCommitWhenTheRefMoved(t *testing.T) {
	c := change("1")
	c.Ref = "refs/pull/1/head"
	d := newDoubles(t)
	d.vcs.EXPECT().FetchRef(mock.Anything, clone.Root, clone.Remote, c.Ref).Return(head("newer"), nil)
	d.vcs.EXPECT().FetchCommit(mock.Anything, clone.Root, clone.Remote, c.Head).Return(errors.New("not found"))
	require.EqualError(t, fetchHead(t.Context(), d.vcs, clone, c), "not found")
}

// gitRepo commits files into a new git repository and returns it and the commit.
func gitRepo(t *testing.T, files map[string]string) (string, string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	root := t.TempDir()
	for path, content := range files {
		abs := filepath.Join(root, filepath.FromSlash(path))
		require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
		require.NoError(t, os.WriteFile(abs, []byte(content), 0o644))
	}
	git := func(args ...string) string {
		cmd := exec.Command("git", append([]string{"-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "git %s: %s", strings.Join(args, " "), out)
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	return root, git("rev-parse", "HEAD")
}

// gitDriver is the git backend the queue runs on, for root.
func gitDriver(t *testing.T, root string) types.BuildVCS {
	t.Helper()
	enabled := true
	res, err := vcs.Resolve(t.Context(), root, "", magustypes.VCSOptions{Enabled: &enabled, Name: "git"})
	require.NoError(t, err)
	return res.VCS
}

// A regeneration runs the change's code in the candidate's checkout, which can point the
// checkout's .git at a repository whose config runs a program under any git that
// discovers it. The queue's own git never does: the change is refused, and nothing ran.
func TestARegenerationThatRewritesTheGitfileIsRefused(t *testing.T) {
	root, commit := gitRepo(t, map[string]string{"gen/a.go": "stale\n"})
	drv := gitDriver(t, root)
	cand, err := checkout(t.Context(), drv, root, t.TempDir(), "candidate-1", commit)
	require.NoError(t, err)
	t.Cleanup(func() { _ = discard(context.Background(), drv, root, cand) })
	marker := filepath.Join(t.TempDir(), "ran")
	hook := CommandRegenerate(script(fmt.Sprintf(`set -e
cp -R %q "$TMPDIR/evil"
printf '#!/bin/sh\ntouch %%s\n' %q > "$TMPDIR/run.sh"
chmod +x "$TMPDIR/run.sh"
git config -f "$TMPDIR/evil/config" core.fsmonitor "$TMPDIR/run.sh"
echo "gitdir: $TMPDIR/evil" > .git
echo fresh > gen/a.go`, filepath.Join(root, ".git"), marker)), HookEnv{}, nil)
	b := built{Candidate: cand, touched: []string{"gen/a.go"}, changed: []string{"gen/a.go"}, date: when}
	d := newDoubles(t)
	d.facts.EXPECT().Classify(mock.Anything, b.touched).Return(map[string]types.Writes{"gen/a.go": {Output: true}}, nil)

	_, err = regenerateIn(t.Context(), drv, candidateSpec{clone: Clone{Root: root, Remote: "origin"}, facts: d.facts, onto: commit, change: change("1")}, b, hook, []string{"gen"})
	var refused *types.RefusedError
	require.ErrorAs(t, err, &refused)
	assert.Equal(t, "the regeneration changed the candidate's `.git`, which tells git where its repository is", refused.Reason)
	assert.NoFileExists(t, marker)

	status := exec.Command("git", "status")
	status.Dir = cand.Dir
	_ = status.Run()
	assert.FileExists(t, marker, "the hook left a working exploit for any git that discovers the repository")
}
