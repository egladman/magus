package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestBuildDriftNoticeUnpushedHead pins the amend case's exact wording: the commit is
// still unambiguous to name (it is HEAD), so the notice hands over a plain amend.
func TestBuildDriftNoticeUnpushedHead(t *testing.T) {
	got := buildDriftNotice("abc1234", driftFinding{staleProjects: []string{"api"}}, driftUnpushedHead)
	want := "[MGS4006] commit abc1234 left generated output stale (api changed with no matching regeneration).\n" +
		"Regenerate: magus run generate:rw api\n" +
		"Then fold it into abc1234 (unpushed, still HEAD):\n" +
		"  git commit --amend --no-edit"
	assert.Equal(t, want, got)
}

// TestBuildDriftNoticeUnpushedNotHead pins the fixup case: a plain amend would rewrite
// the wrong commit (HEAD, not abc1234), so the notice names a fixup targeted at the
// drifted hash, folded non-interactively via autosquash.
func TestBuildDriftNoticeUnpushedNotHead(t *testing.T) {
	got := buildDriftNotice("abc1234", driftFinding{staleProjects: []string{"api"}}, driftUnpushedNotHead)
	want := "[MGS4006] commit abc1234 left generated output stale (api changed with no matching regeneration).\n" +
		"Regenerate: magus run generate:rw api\n" +
		"Then fold it into abc1234 (unpushed, but no longer HEAD):\n" +
		"  git commit --fixup=abc1234 && GIT_SEQUENCE_EDITOR=true git rebase -i --autosquash abc1234^"
	assert.Equal(t, want, got)
}

// TestBuildDriftNoticePushed pins the refusal case: NEITHER rewrite form appears,
// anywhere in the string, because abc1234 already left the repository.
func TestBuildDriftNoticePushed(t *testing.T) {
	got := buildDriftNotice("abc1234", driftFinding{staleProjects: []string{"api"}}, driftPushed)
	want := "[MGS4006] commit abc1234 left generated output stale (api changed with no matching regeneration).\n" +
		"Regenerate: magus run generate:rw api\n" +
		"abc1234 is already pushed; do not amend or rebase published history. Commit the fix as a new, follow-up commit instead."
	assert.Equal(t, want, got)
	// The prohibition sentence itself names "rebase" as one of the things not to do;
	// what must never appear is the actual REWRITE COMMAND.
	assert.NotContains(t, got, "--amend")
	assert.NotContains(t, got, "--fixup")
	assert.NotContains(t, got, "git rebase")
}

// TestBuildDriftNoticeMultipleProjects covers the multi-project regenerate command: one
// invocation naming every drifted project, not one per project.
func TestBuildDriftNoticeMultipleProjects(t *testing.T) {
	got := buildDriftNotice("abc1234", driftFinding{staleProjects: []string{"api", "web"}}, driftUnpushedHead)
	assert.Contains(t, got, "magus run generate:rw api web")
	assert.Contains(t, got, "(api web changed with no matching regeneration)")
}

// TestBuildDriftNoticeFormattingOnly pins the formatting-only wording: MGS4009 (a
// sibling of MGS4006, minted for the commit-time question - see
// docs/reference/codes/race/MGS4009.md), a plain sentence naming the files, and
// format:rw as the remedy, distinct from generate:rw.
func TestBuildDriftNoticeFormattingOnly(t *testing.T) {
	got := buildDriftNotice("abc1234", driftFinding{unformatted: []string{"internal/foo.go"}}, driftUnpushedHead)
	want := "[MGS4009] commit abc1234 left formatting stale (gofmt would reformat): internal/foo.go\n" +
		"Reformat: magus run format:rw .\n" +
		"Then fold it into abc1234 (unpushed, still HEAD):\n" +
		"  git commit --amend --no-edit"
	assert.Equal(t, want, got)
	assert.NotContains(t, got, "MGS4006")
	assert.NotContains(t, got, "generate:rw")
}

// TestBuildDriftNoticeBothClasses pins that both classes appear in ONE notice, each
// carrying its own code and its own remedy, sharing the single amend-safety instruction
// at the end rather than repeating it per class.
func TestBuildDriftNoticeBothClasses(t *testing.T) {
	got := buildDriftNotice("abc1234", driftFinding{
		staleProjects: []string{"api"},
		unformatted:   []string{"internal/foo.go", "internal/bar.go"},
	}, driftPushed)
	want := "[MGS4006] commit abc1234 left generated output stale (api changed with no matching regeneration).\n" +
		"Regenerate: magus run generate:rw api\n" +
		"[MGS4009] commit abc1234 also left formatting stale (gofmt would reformat): internal/foo.go, internal/bar.go\n" +
		"Reformat: magus run format:rw .\n" +
		"abc1234 is already pushed; do not amend or rebase published history. Commit the fix as a new, follow-up commit instead."
	assert.Equal(t, want, got)
}
