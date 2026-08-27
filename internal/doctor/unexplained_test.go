package doctor

import (
	"context"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hotWorkspace is a WorkspaceReader that also answers the history lens, which is how the
// check reaches hotspots: by runtime assertion, the same way the MCP handler does.
type hotWorkspace struct {
	types.WorkspaceReader
	files []types.FileHotspot
	err   error
}

func (h hotWorkspace) Root() string { return "" }

func (h hotWorkspace) Hotspots(context.Context, types.InsightOptions) (types.HotspotOutput, error) {
	return types.HotspotOutput{Files: h.files}, h.err
}

func (h hotWorkspace) Affinity(context.Context, types.InsightOptions) (types.AffinityOutput, error) {
	return types.AffinityOutput{}, nil
}

func (h hotWorkspace) Ownership(context.Context, types.InsightOptions) (types.OwnershipOutput, error) {
	return types.OwnershipOutput{}, nil
}

func (h hotWorkspace) Trend(context.Context, types.InsightOptions) (types.TrendOutput, error) {
	return types.TrendOutput{}, nil
}

func (h hotWorkspace) Volatility(context.Context) (types.VolatilityReport, error) {
	return types.VolatilityReport{}, nil
}

func (h hotWorkspace) Unreferenced(context.Context) (types.UnreferencedOutput, error) {
	return types.UnreferencedOutput{}, nil
}

func hot(paths ...string) []types.FileHotspot {
	out := make([]types.FileHotspot, 0, len(paths))
	for _, p := range paths {
		out = append(out, types.FileHotspot{Path: p, Commits: 40, Authors: 3})
	}
	return out
}

func TestUnexplainedHotspots(t *testing.T) {
	t.Run("a workspace keeping no notes declined a feature rather than failing one", func(t *testing.T) {
		r := &runner{ws: hotWorkspace{files: hot("a.go")}}
		r.opts.explanations = &Explanations{}

		got := r.checkUnexplainedHotspots(nil)
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Equal(t, types.EvidenceUnknown, got.Evidence)
	})

	t.Run("no caller-supplied store is unknown, never a clean bill", func(t *testing.T) {
		r := &runner{ws: hotWorkspace{files: hot("a.go")}}

		got := r.checkUnexplainedHotspots(nil)
		assert.Equal(t, types.EvidenceUnknown, got.Evidence)
	})

	t.Run("no history lens is unknown", func(t *testing.T) {
		r := &runner{}
		r.opts.explanations = &Explanations{Notes: 2}

		got := r.checkUnexplainedHotspots(nil)
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Equal(t, types.EvidenceUnknown, got.Evidence)
		assert.Contains(t, got.Message, "no history lens")
	})

	t.Run("a lens that ran and found no history is unknown, not explained", func(t *testing.T) {
		r := &runner{ws: hotWorkspace{}}
		r.opts.explanations = &Explanations{Notes: 2}

		got := r.checkUnexplainedHotspots(nil)
		assert.Equal(t, types.EvidenceUnknown, got.Evidence)
	})

	t.Run("notes that reach the hot files pass", func(t *testing.T) {
		r := &runner{ws: hotWorkspace{files: hot("internal/cache/key.go", "b.go")}}
		r.opts.explanations = &Explanations{Notes: 3, Files: []string{"internal/cache/key.go"}}

		got := r.checkUnexplainedHotspots(nil)
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Equal(t, types.EvidenceInferred, got.Evidence)
		assert.Contains(t, got.Message, "1 of the 2")
	})

	t.Run("notes that reach none of them is advice, and names which", func(t *testing.T) {
		r := &runner{ws: hotWorkspace{files: hot("a.go", "b.go")}}
		r.opts.explanations = &Explanations{Notes: 3, Files: []string{"elsewhere.go"}}

		got := r.checkUnexplainedHotspots(nil)
		assert.Equal(t, types.DoctorAdvice, got.Status)
		require.Len(t, got.Details, 3) // two bare files, then the remedy
		assert.Contains(t, got.Details[0], "a.go")
		assert.Contains(t, got.Details[1], "b.go")
	})

	// The ranking is only as good as the paths matching. An anchor stored absolute and a
	// hotspot reported relative describe the same file and compare unequal, which would
	// report every hot file unexplained - a bug shaped exactly like a real finding.
	t.Run("an absolute anchor matches a relative hotspot", func(t *testing.T) {
		r := &runner{root: "/w", ws: hotWorkspace{files: hot("pkg/a.go")}}
		r.opts.explanations = &Explanations{Notes: 1, Files: []string{"/w/pkg/a.go"}}

		got := r.checkUnexplainedHotspots(nil)
		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Contains(t, got.Message, "1 of the 1")
	})

	t.Run("only the hottest files are asked about", func(t *testing.T) {
		paths := make([]string, 0, unexplainedTop+5)
		for i := range unexplainedTop + 5 {
			paths = append(paths, string(rune('a'+i))+".go")
		}
		r := &runner{ws: hotWorkspace{files: hot(paths...)}}
		r.opts.explanations = &Explanations{Notes: 1}

		got := r.checkUnexplainedHotspots(nil)
		assert.Contains(t, got.Message, "0 of the 10")
	})
}
