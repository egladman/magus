package mergequeue

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/merge3"
	"github.com/egladman/magus/libs/mergequeue/types"
	magustypes "github.com/egladman/magus/types"
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
		CommitMeta: magustypes.CommitMeta{Message: "merge queue: #2 is stacked on " + parent.Head[:12], Author: candidateIdentity, Committer: candidateIdentity, Date: candidateDate},
		Tree:       "tip tree", Parents: []string{onto, parent.Head}}).Return(recorded, nil)
	d.vcs.EXPECT().CreateCheckout(mock.Anything, clone.Root, mock.Anything, recorded).Return(nil)
	d.vcs.EXPECT().StartMerge(mock.Anything, mock.Anything, c.Head, candidateIdentity).Return(nil)
	d.vcs.EXPECT().Conflicts(mock.Anything, mock.Anything).Return(nil, nil)
	d.vcs.EXPECT().Commit(mock.Anything, mock.Anything, magustypes.CheckoutCommit{CommitMeta: queueMeta("merge queue: candidate #2")}).Return(head("cand"), nil)
	d.vcs.EXPECT().DiffTrees(mock.Anything, clone.Root, onto, head("cand")).Return([]string{"lib/x.txt"}, nil)

	b, err := buildMerge(t.Context(), d.vcs, candidateSpec{clone: clone, facts: d.facts, onto: onto, change: c, scratch: t.TempDir()})
	require.NoError(t, err)
	assert.Equal(t, head("cand"), b.Commit)
	assert.Equal(t, []string{"lib/x.txt"}, b.touched)
	assert.DirExists(t, b.Scratch, "a private directory for the hooks run on it")
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
			b := built{Candidate: types.Candidate{Commit: head("cand"), Dir: dir, Scratch: "/scratch"}, touched: []string{"a.go", "gen/a.go"}}
			regenerate := func(_ context.Context, r types.Regeneration) error {
				assert.Equal(t, types.Regeneration{Dir: dir, Scratch: "/scratch", Change: c, Paths: []string{"gen/a.go"}, Units: []string{"gen"}}, r)
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
				d.vcs.EXPECT().Commit(mock.Anything, dir, magustypes.CheckoutCommit{CommitMeta: queueMeta("regenerate generated files"), Paths: tc.committed}).Return(tc.want, nil)
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
