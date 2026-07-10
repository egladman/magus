package mcp

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/types"
)

// whereResult is the single JSON shape returned by magus_where. When matched
// is 1, path and abs_dir are populated. When matched > 1, candidates lists
// all matches and error explains the ambiguity.
type whereResult struct {
	Path       string   `json:"path,omitempty"`
	AbsDir     string   `json:"abs_dir,omitempty"`
	Matched    int      `json:"matched"`
	Candidates []string `json:"candidates,omitempty"`
	Error      string   `json:"error,omitempty"`
}

type whereTool struct {
	ws types.WorkspaceReader
}

func (t *whereTool) Name() string { return "magus_where" }

func (t *whereTool) Invoke(ctx context.Context, req types.InvokeRequest) (types.InvokeResponse, error) {
	filter := paramString(req.Params, "filter", "")
	var filters []string
	if filter != "" {
		filters = strings.Fields(filter)
	}

	all := t.ws.All()
	if len(all) == 0 {
		return types.InvokeResponse{}, errors.New("mcp: no projects in workspace")
	}

	scored := interactive.ScoreProjects(all, filters)
	if len(scored) == 0 {
		return types.InvokeResponse{}, errors.New("mcp: no projects match filter: " + filter)
	}

	// strictly-greater means an unambiguous top match
	if len(scored) == 1 || (len(filters) > 0 && scored[0].Score > scored[1].Score) {
		p := scored[0].P
		return types.InvokeResponse{Data: whereResult{
			Path:    p.Path,
			AbsDir:  filepath.Join(t.ws.Root(), p.Path),
			Matched: 1,
		}}, nil
	}

	candidates := make([]string, len(scored))
	for i, s := range scored {
		candidates[i] = s.P.Path
	}
	return types.InvokeResponse{Data: whereResult{
		Matched:    len(scored),
		Candidates: candidates,
		Error:      "ambiguous filter — multiple projects match; narrow your filter",
	}}, nil
}

var _ types.SpellDriver = (*whereTool)(nil)
