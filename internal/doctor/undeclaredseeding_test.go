package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// seedingRepo commits one declared source, one config nothing declares, and one
// untracked build product. The third is the case the check must NOT report: a fresh
// clone does not have it, so reporting it would make doctor's answer depend on
// whether somebody had built the tree.
func seedingRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(repo, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		require.NoError(t, cmd.Run(), "git %v", args)
	}
	write("main.go", "package main\n")
	write(".golangci.yml", "linters:\n")
	run("init", "-q", "-b", "main")
	run("add", "-A")
	run("commit", "-qm", "init")
	write("coverage.out", "mode: set\n") // untracked
	return repo
}

func seedingProject() *types.Project {
	return &types.Project{Path: ".", Name: "root", Sources: []string{"**/*.go"}}
}

// TestUndeclaredSeedingFilesReportsTheStandingSet is MGS1028 asked of the whole tree
// rather than of one changeset. The affected-set diagnostic only ever sees the files
// somebody happened to touch, so the declaration that is missing surfaces one pull
// request at a time; this answers it in one pass.
func TestUndeclaredSeedingFilesReportsTheStandingSet(t *testing.T) {
	repo := seedingRepo(t)
	r := &runner{root: repo, ws: stubWorkspace{}}

	got := r.checkUndeclaredSeedingFiles([]*types.Project{seedingProject()})

	require.Equal(t, types.DoctorAdvice, got.Status, got.Message)
	assert.Equal(t, []string{".golangci.yml"}, got.Details,
		"main.go is declared, and the untracked coverage.out is a different problem")
	assert.Contains(t, got.Message, "MGS1028")
}

// TestUndeclaredSeedingFilesIsAdviceNotFailure pins the doctrine at types/doctor.go:
// which files are build inputs is the workspace's judgement. A LICENSE nobody's cache
// key reads is CORRECTLY undeclared, and a check that failed on it would be dictating
// a layout rather than reporting a cost.
func TestUndeclaredSeedingFilesIsAdviceNotFailure(t *testing.T) {
	repo := seedingRepo(t)
	r := &runner{root: repo, ws: stubWorkspace{}}

	got := r.checkUndeclaredSeedingFiles([]*types.Project{seedingProject()})

	assert.NotEqual(t, types.DoctorFail, got.Status, "this check must never gate a build")
}

// TestUndeclaredSeedingFilesClearsOnceDeclared is the other half of the fix line:
// declaring the file is what makes the check go quiet, so the advice can honestly say
// so.
func TestUndeclaredSeedingFilesClearsOnceDeclared(t *testing.T) {
	repo := seedingRepo(t)
	p := seedingProject()
	p.Sources = []string{"**/*.go", ".golangci.yml"}
	r := &runner{root: repo, ws: stubWorkspace{}}

	got := r.checkUndeclaredSeedingFiles([]*types.Project{p})

	assert.Equal(t, types.DoctorOK, got.Status, got.Message)
}

// TestUndeclaredSeedingFilesWithoutVCS degrades rather than guesses: with nothing to
// enumerate committed files with, the check has no candidate set at all, and a walk of
// the working tree would answer a different question (it would count build residue).
func TestUndeclaredSeedingFilesWithoutVCS(t *testing.T) {
	dir := t.TempDir() // not a repository
	r := &runner{root: dir, ws: stubWorkspace{}}

	got := r.checkUndeclaredSeedingFiles([]*types.Project{seedingProject()})

	assert.Equal(t, types.DoctorOK, got.Status, got.Message)
}
