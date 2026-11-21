package evidence

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/diagram"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
)

func TestPlanSVGsDrawWithoutARefusalAndRepeatByteForByte(t *testing.T) {
	rows := []jobtypes.Job{{ID: "lead", Created: 1, State: jobtypes.StateRunning}}
	for _, id := range []string{"a", "b"} {
		rows = append(rows, jobtypes.Job{ID: id, Created: 2, State: jobtypes.StateExited, Parent: "lead"})
	}
	p := job.NewPlan(rows)
	holder := func(jobtypes.Job) string { return "" }
	first, second := t.TempDir(), t.TempDir()

	paths, err := WritePlanSVGs(t.Context(), p, p.Diagrams(), first, holder)
	require.NoError(t, err)
	again, err := WritePlanSVGs(t.Context(), p, p.Diagrams(), second, holder)
	require.NoError(t, err)

	require.Len(t, again, len(paths))
	assert.Equal(t, filepath.Join(first, "plan.svg"), paths[0])
	for i := range paths {
		a, err := os.ReadFile(paths[i])
		require.NoError(t, err)
		b, err := os.ReadFile(again[i])
		require.NoError(t, err)
		assert.True(t, strings.HasPrefix(string(a), "<?xml version=\"1.0\" encoding=\"UTF-8\"?>\n<svg"), paths[i])
		assert.Equal(t, string(a), string(b), paths[i])
	}
}

func TestPlanSVGsWriteNothingWhenADiagramIsRefused(t *testing.T) {
	p := job.NewPlan([]jobtypes.Job{{ID: "lead", Created: 1, State: jobtypes.StateRunning}})
	figures := p.Diagrams()
	require.NotEmpty(t, figures)
	invalid := *figures[0]
	invalid.ID = ""
	figures = append(figures, &invalid)
	dir := filepath.Join(t.TempDir(), "svg")

	_, err := WritePlanSVGs(t.Context(), p, figures, dir, job.HolderCell)

	var findings *diagram.Findings
	require.ErrorAs(t, err, &findings)
	assert.NoDirExists(t, dir)
}
