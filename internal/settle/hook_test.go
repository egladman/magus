package settle

import (
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
)

// TestGeneratorProjectsCountsMovedOutputs: a project whose output the operation moved
// regenerates too, since that output was made against a base the operation replaced.
func TestGeneratorProjectsCountsMovedOutputs(t *testing.T) {
	got := generatorProjects([]types.FileEntry{
		{Path: "docs/a.md", Role: "source", SourceOf: []string{".", "docs"}},
		{Path: "api/gen/x.go", Role: "output", OutputOf: []string{"api"}},
		{Path: "README", Role: "unclaimed"},
	})
	assert.Equal(t, []string{".", "api", "docs"}, got)
}

// TestSettleInvocationsRunDeepestFirst: one `<target>:rw` run per depth and target,
// nested projects before the ancestors that index their output, the owed records'
// own targets beside the generate convention, and a project with no such target left
// out rather than failing the run.
func TestSettleInvocationsRunDeepestFirst(t *testing.T) {
	defines := func(path, target string) bool {
		return target != "generate" || (path != "api" && path != "gone")
	}
	got := Invocations([]string{".", "api", "docs", "docs/site", "gone"}, []vcs.OwedRegeneration{
		{Project: ".", Target: "index-generate"},
		{Project: "docs", Target: "generate"},
		{Project: "api", Target: "proto"},
	}, defines)
	assert.Equal(t, [][]string{
		{"generate:rw", "docs/site"},
		{"generate:rw", "docs"},
		{"proto:rw", "api"},
		{"generate:rw", "."},
		{"index-generate:rw", "."},
	}, got)
}

// TestOutcomeNotice pins the one line each hook prints, since it is the whole
// account a person gets of what the hook did to their commit.
func TestOutcomeNotice(t *testing.T) {
	ran := [][]string{{"generate:rw", "docs", "."}}
	run := hint.Run.With("generate:rw", "docs", ".")
	cases := []struct {
		name    string
		s       Outcome
		hook    string
		fold    string
		want    string
		pending bool
	}{
		{name: "current", s: Outcome{kind: "merge", Ran: ran}, hook: vcs.HookPreMergeCommit,
			want: "magus: this merge changed generator inputs; ran " + run + "; the generated output was already current"},
		{name: "merge stopped", s: Outcome{kind: "merge", Ran: ran, Staged: []string{"MAGUS.md"}}, hook: vcs.HookPreMergeCommit, pending: true,
			want: "magus: this merge changed generator inputs; ran " + run + "; staged 1 regenerated file(s) into the merge, so the commit is left to you: `git commit` concludes it"},
		{name: "into the commit", s: Outcome{kind: "cherry-pick", Ran: ran, Staged: []string{"MAGUS.md", "docs/gen/a"}}, hook: vcs.HookPreCommit, pending: true,
			want: "magus: this cherry-pick changed generator inputs; ran " + run + "; staged 2 regenerated file(s) into this commit"},
		{name: "fold", s: Outcome{kind: "rebase", Ran: ran, Staged: []string{"MAGUS.md"}}, hook: vcs.HookPostRewrite, fold: "git commit --amend --no-edit",
			want: "magus: this rebase changed generator inputs; ran " + run + "; staged 1 regenerated file(s); fold them into HEAD with `git commit --amend --no-edit`"},
		{name: "pushed", s: Outcome{kind: "rebase", Ran: ran, Staged: []string{"MAGUS.md"}}, hook: vcs.HookPostRewrite,
			want: "magus: this rebase changed generator inputs; ran " + run + "; staged 1 regenerated file(s); HEAD may already be pushed, so commit them as a new commit"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := tc.s
			s.op = vcs.HookOperation{CommitPending: tc.pending}
			assert.Equal(t, tc.want, s.Notice(tc.hook, tc.fold))
		})
	}
}
