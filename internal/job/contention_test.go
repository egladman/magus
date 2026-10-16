package job

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
)

func TestUnusedWritePaths(t *testing.T) {
	t.Parallel()

	files := map[string]string{".gitattributes": "*.go diff=golang\n", "run.go": claimGo, "a.md": "# A\n", "b.md": "# B\n"}
	root := testkit.NewRepo(t, files).Root()
	holderRepo := testkit.NewRepo(t, files)
	rev := holderRepo.Head()
	holderRepo.WriteTree(map[string]string{"run.go": strings.Replace(claimGo, "a()", "a2()", 1), "a.md": "# A2\n"})
	s := NewStore(tmpLoc(t, root))

	holder := func(paths ...string) types.Job {
		return types.Job{ID: "A", State: types.StateRunning, WritePaths: paths, Checkpoint: rev, CheckoutRoot: holderRepo.Root()}
	}

	t.Run("the paths and claims no changed line falls under", func(t *testing.T) {
		t.Parallel()
		h := holder("a.md", "b.md", "run.go#A", "run.go#C", "docs/**", "*.md")
		assert.Equal(t, []string{"b.md", "run.go#C", "docs/**"}, UnusedWritePaths(t.Context(), s, []types.Job{h}, h))
	})

	t.Run("every path used is empty, not unknown", func(t *testing.T) {
		t.Parallel()
		h := holder("a.md", "run.go")
		got := UnusedWritePaths(t.Context(), s, nil, h)
		assert.NotNil(t, got)
		assert.Empty(t, got)
	})

	t.Run("an unreadable footprint is nil", func(t *testing.T) {
		t.Parallel()
		h := holder("a.md")
		h.Checkpoint = ""
		assert.Nil(t, UnusedWritePaths(t.Context(), s, []types.Job{h}, h))
		assert.Contains(t, describeUnused("A", nil), "could not be read")
	})
}
