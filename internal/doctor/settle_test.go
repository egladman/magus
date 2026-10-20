package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCheckDeferredSettle: nothing deferred passes; the record the hooks write under
// magus.settle=defer fails naming the files changed since it and the command that settles
// it; a record naming a revision git cannot resolve fails with the error.
func TestCheckDeferredSettle(t *testing.T) {
	repo := testkit.NewRepo(t, map[string]string{"IN.txt": "base\n"})
	root := repo.Root()
	r := &runner{ws: rootedWorkspace{root: root}}

	assert.Equal(t, types.Check{Name: "deferred-settle", Status: types.CheckOK,
		Message: "no settle is deferred"}, r.checkDeferredSettle())

	start := repo.Head()
	repo.Commit("input a", map[string]string{"IN.txt": "a\n"})
	repo.Commit("input b", map[string]string{"IN2.txt": "b\n"})
	record := filepath.Join(repo.Git("rev-parse", "--absolute-git-dir"), "magus-settle-deferred")
	require.NoError(t, os.WriteFile(record, []byte(start+"\n"), 0o644))

	assert.Equal(t, types.Check{
		Name:    "deferred-settle",
		Status:  types.CheckFail,
		Message: "a settle deferred under magus.settle=defer has not run, so output generated from the 2 file(s) it changed may be stale",
		Details: []string{
			"settle it with `" + hint.VCSResolve.String() + "`, which regenerates what changed, stages the result and clears the record",
		},
	}, r.checkDeferredSettle())

	require.NoError(t, os.WriteFile(record, []byte("0000000000000000000000000000000000000000\n"), 0o644))
	got := r.checkDeferredSettle()
	assert.Equal(t, types.CheckFail, got.Status)
	assert.Contains(t, got.Message, "magus-settle-deferred")
}
